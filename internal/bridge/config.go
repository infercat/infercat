// Package bridge connects a running host's single gateway to the public bridge.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/usage"
)

const FileName = "bridge.json"
const TrustLine = "a public URL decrypts TLS at the edge and in our object; the tunnel mode's \"nobody in the middle\" does not carry over."

var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var tokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Config struct {
	Disabled bool   `json:"disabled,omitempty"` // Old registrations remain enabled.
	Endpoint string `json:"endpoint"`
	Host     string `json:"host"`
	Token    string `json:"token"`
}

func (c Config) URL() string { return c.Endpoint + "/h/" + c.Host + "/v1" }
func (c Config) Validate() error {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !hostPattern.MatchString(c.Host) || !tokenPattern.MatchString(c.Token) {
		return errors.New("invalid bridge configuration")
	}
	return nil
}
func Load(dir string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return Config{}, errors.New("invalid bridge.json")
	}
	return c, c.Validate()
}
func Save(dir string, c Config) error {
	return save(dir, c, false)
}

// SetEnabled preserves the registered identity; changing the switch publishes one complete file.
func SetEnabled(dir string, enabled bool) error {
	c, err := Load(dir)
	if err != nil {
		return err
	}
	if c == (Config{}) {
		return errors.New("not registered; use expose --register CODE")
	}
	c.Disabled = !enabled
	return save(dir, c, true)
}

func save(dir string, c Config, replace bool) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(c)
	// Temp files are 0600. Registration may never replace an existing identity.
	f, err := os.CreateTemp(dir, ".bridge-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if replace {
		return os.Rename(f.Name(), filepath.Join(dir, FileName))
	}
	return os.Link(f.Name(), filepath.Join(dir, FileName))
}

type viaKey struct{}
type Recorder struct{ Next usage.Recorder }

func (r Recorder) Record(ctx context.Context, e usage.Event) {
	if ctx.Value(viaKey{}) == true {
		e.Via = "bridge"
	}
	r.Next.Record(ctx, e)
}

// Manager serializes reloads. A cancelled client is joined before its replacement starts.
type Manager struct {
	state   connectionState
	Keys    *keys.FileStore
	updates chan struct{}
	mu      sync.Mutex
	cfg     Config
	cancel  context.CancelFunc
	done    chan struct{}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stop()
}
func (m *Manager) stop() {
	if m.cancel != nil {
		m.cancel()
		<-m.done
		m.cancel = nil
	}
	m.state.connection(false, "")
	m.cfg = Config{}
	m.updates = nil
}
func (m *Manager) Reload(ctx context.Context, dir string, h http.Handler, logf func(string, ...any)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := Load(dir)
	if err != nil {
		return err
	}
	if c == m.cfg {
		if m.updates != nil {
			select {
			case m.updates <- struct{}{}:
			default:
			}
		}
		return nil
	}
	m.stop()
	m.cfg = c
	m.state.configure(c)
	if c == (Config{}) || c.Disabled {
		return nil
	}
	child, cancel := context.WithCancel(ctx)
	m.cfg, m.cancel, m.done = c, cancel, make(chan struct{})
	m.updates = make(chan struct{}, 1)
	syncKeys := keySync{store: m.Keys, reload: m.updates, state: &m.state}
	if m.Keys != nil {
		syncKeys.changed = m.Keys.Changes()
	}
	logf("public endpoint: %s\n%s", c.URL(), TrustLine)
	go func() { defer close(m.done); Run(child, c, h, logf, syncKeys) }()
	return nil
}
func refusal(status int) error {
	return fmt.Errorf("bridge refused (%d); registration is never automatically replayed", status)
}
