package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func consoleURL(dataDir, address string) (string, error) {
	if address == "" {
		return "", errors.New("console is off")
	}
	token, err := os.ReadFile(filepath.Join(dataDir, admin.TokenName))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(token)) == "" {
		return "", errors.New("admin token is empty")
	}
	return "http://" + address + "/#token=" + url.QueryEscape(strings.TrimSpace(string(token))), nil
}

func (e *env) cmdConsole(ctx context.Context, pre string, args []string) error {
	fs := flag.NewFlagSet("console", flag.ContinueOnError)
	dd := fs.String("data-dir", pre, dataDirUsage)
	printOnly := fs.Bool("print", false, "print the console URL without opening a browser")
	if err := e.parse(fs, "Usage: infercat console [--print] [--data-dir DIR]\n", args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	dir, err := resolveDataDir(*dd)
	if err != nil {
		return err
	}
	st, err := admin.Fetch(ctx, dir)
	if err != nil {
		return err
	}
	link, err := consoleURL(dir, st.Console)
	if err != nil {
		return err
	}
	fmt.Fprintln(e.out, link)
	if !*printOnly {
		for _, name := range []string{"open", "xdg-open"} {
			if path, err := osexec.LookPath(name); err == nil {
				return osexec.CommandContext(ctx, path, link).Run()
			}
		}
	}
	return nil
}

type consoleSettings struct {
	LogPrompts            bool   `json:"log_prompts"`
	Upstream              string `json:"upstream"`
	DERPMapURL            string `json:"derpmap_url"`
	Region                string `json:"region"`
	ConfiguredWebURL      string `json:"configured_web_url"`
	Name                  string `json:"name"`
	WebURL                string `json:"web_url"`
	Slots                 int    `json:"slots"`
	LogRequests           bool   `json:"log_requests"`
	LogRequestsRemembered bool   `json:"log_requests_remembered"`
	DataDir               string `json:"data_dir"`
}

type consoleKey struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Status      keys.Status  `json:"status"`
	Limits      keys.Limits  `json:"limits"`
	Created     time.Time    `json:"created_at"`
	LastSeen    time.Time    `json:"last_seen"`
	TodayTokens int          `json:"today_tokens"`
	Daily       []consoleDay `json:"daily,omitempty"`
}

type consoleDay struct {
	Date   string      `json:"date"`
	Counts usage.Stats `json:"counts"`
}

// Strict, bounded JSON; a refusal must occur before a store operation.
func decodeConsole(w http.ResponseWriter, r *http.Request, out any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func (e *env) consoleAPI(store *keys.FileStore, addr string, up upstream.Upstream, settings consoleSettings) http.Handler {
	mux := http.NewServeMux()
	var mu sync.Mutex // serialize duplicate checks and patches with other API mutations
	route := func(pattern string, f func(http.ResponseWriter, *http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			value, err := f(w, r)
			if err != nil {
				code := http.StatusBadRequest
				if errors.Is(err, keys.ErrNotFound) {
					code = http.StatusNotFound
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(value)
		})
	}
	today := func() time.Time { return time.Now().UTC().Truncate(24 * time.Hour) }
	readKey := func(r *http.Request) (*keys.Key, error) {
		k, err := store.Find(r.Context(), r.PathValue("id"))
		if err == nil && k.ID != r.PathValue("id") {
			return nil, keys.ErrNotFound
		}
		return k, err
	}
	view := func(k *keys.Key) (consoleKey, error) {
		rep, err := usage.AggregateFile(settings.DataDir, usage.Filter{KeyID: k.ID})
		if err != nil {
			return consoleKey{}, err
		}
		day, err := usage.AggregateFile(settings.DataDir, usage.Filter{KeyID: k.ID, Since: today()})
		if err != nil {
			return consoleKey{}, err
		}
		return consoleKey{ID: k.ID, Name: k.Name, Status: k.Status, Limits: k.Limits, Created: k.CreatedAt,
			LastSeen: rep.LastSeen()[k.ID], TodayTokens: day.Total.PromptTokens + day.Total.CompletionTokens}, err
	}
	route("GET /keys", func(w http.ResponseWriter, r *http.Request) (any, error) {
		list, err := store.List(r.Context())
		if err != nil {
			return nil, err
		}
		out := []consoleKey{}
		for _, k := range list {
			v, err := view(k)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	})
	route("GET /keys/{id}", func(w http.ResponseWriter, r *http.Request) (any, error) {
		k, err := readKey(r)
		if err != nil {
			return nil, err
		}
		v, err := view(k)
		if err != nil {
			return nil, err
		}
		// Daily counts are bounded UTC intervals, oldest first, including zero-usage days.
		for i := 6; i >= 0; i-- {
			start := today().AddDate(0, 0, -i)
			rep, err := usage.AggregateFile(settings.DataDir, usage.Filter{KeyID: k.ID, Since: start, Until: start.AddDate(0, 0, 1)})
			if err != nil {
				return nil, err
			}
			v.Daily = append(v.Daily, consoleDay{Date: start.Format("2006-01-02"), Counts: rep.Total})
		}
		return v, nil
	})
	route("GET /usage", func(w http.ResponseWriter, r *http.Request) (any, error) {
		start := today()
		days := 1
		switch r.URL.Query().Get("window") {
		case "", "today":
		case "week":
			start = start.AddDate(0, 0, -6)
			days = 7
		default:
			return nil, errors.New("window must be today or week")
		}
		return usage.AggregateFile(settings.DataDir, usage.Filter{Since: start, Until: today().AddDate(0, 0, 1), Days: days})
	})
	route("GET /engine", func(w http.ResponseWriter, r *http.Request) (any, error) { return up.Info(), nil })
	route("GET /settings", func(w http.ResponseWriter, r *http.Request) (any, error) { return settings, nil })
	applied := func() (any, error) {
		if err := store.Reload(); err != nil {
			e.logf("admin reload after committed change: %v", err)
		}
		return map[string]bool{"ok": true}, nil
	}
	result := func(k *keys.Key, inv string) any {
		return map[string]string{"key_id": k.ID, "name": k.Name, "invite": inv, "link": inviteLink(settings.DataDir, inv)}
	}
	route("POST /keys", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Name   string      `json:"name"`
			Limits keys.Limits `json:"limits"`
		}
		if err := decodeConsole(w, r, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Name) == "" {
			return nil, errors.New("name is required")
		}
		if err := e.refuseDuplicate(r.Context(), store, settings.DataDir, in.Name); err != nil {
			return nil, err
		}
		if addr == "" {
			return nil, errors.New("host has no address")
		}
		k, inv, err := e.mintKey(r.Context(), store, addr, in.Name, in.Limits)
		if err != nil {
			return nil, err
		}
		// Add already updated this exact gateway store. Do not lose the one-time invite if reload fails.
		if err := store.Reload(); err != nil {
			e.logf("admin reload after mint: %v", err)
		}
		return result(k, inv), nil
	})
	for _, action := range []string{"pause", "resume", "revoke", "rotate"} {
		route("POST /keys/{id}/"+action, func(w http.ResponseWriter, r *http.Request) (any, error) {
			k, err := readKey(r)
			if err != nil {
				return nil, err
			}
			if action == "rotate" {
				if addr == "" {
					return nil, errors.New("host has no address")
				}
				inv, err := e.rotateKey(r.Context(), store, addr, k.ID)
				if err != nil {
					return nil, err
				}
				if err := store.Reload(); err != nil {
					e.logf("admin reload after rotate: %v", err)
				}
				return result(k, inv), nil
			}
			st := map[string]keys.Status{"pause": keys.Paused, "resume": keys.Active, "revoke": keys.Revoked}[action]
			if err := store.SetStatus(r.Context(), k.ID, st); err != nil {
				return nil, err
			}
			return applied()
		})
	}
	route("PATCH /keys/{id}", func(w http.ResponseWriter, r *http.Request) (any, error) {
		k, err := readKey(r)
		if err != nil {
			return nil, err
		}
		// Decoding over the existing value preserves omitted fields and explicit zero limits.
		next := k.Limits
		if err := decodeConsole(w, r, &next); err != nil {
			return nil, err
		}
		if err := store.SetLimits(r.Context(), k.ID, next); err != nil {
			return nil, err
		}
		return applied()
	})
	return mux
}
