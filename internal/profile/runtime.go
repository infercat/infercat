package profile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/infercat/infercat/internal/supervise"
)

type MemberStatus struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Healthy  bool   `json:"healthy"`
	Restarts int    `json:"restarts"`
	Error    string `json:"error,omitempty"`
}
type Runtime struct {
	members map[string]*managed
	cancel  context.CancelFunc
}
type managed struct {
	testPort       func(reset bool) int // Test-only dynamic listener; nil for installed engines.
	mu             sync.Mutex
	ctx            context.Context
	member         Member
	installed      InstalledMember
	installation   Installation
	profile        Profile
	dir            string
	status         MemberStatus
	process        *supervise.Process
	starting       chan struct{}
	done           chan struct{}
	refs, failures int
	idle, retry    time.Time
	backoff        time.Duration
}

func StartInstalled(ctx context.Context, dir string, in Installation) (*Runtime, error) {
	p, e := Builtin(in.Profile)
	if e != nil || in.Digest != profileDigest(p) {
		return nil, fmt.Errorf("profile changed; run infercat setup again")
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &Runtime{members: map[string]*managed{}, cancel: cancel}
	for _, m := range p.Members {
		im := InstalledMember{ID: m.ID, Unavailable: "not installed"}
		for _, v := range in.Members {
			if v.ID == m.ID {
				im = v
			}
		}
		state := "dormant"
		if im.External {
			state = "external"
		}
		if im.Unavailable != "" {
			state = "unavailable"
		}
		v := &managed{ctx: ctx, member: m, installed: im, installation: in, profile: p, dir: dir, status: MemberStatus{ID: m.ID, State: state, Error: im.Unavailable}, done: make(chan struct{})}
		r.members[m.Class] = v
		go v.loop()
	}
	if m := r.members["text"]; m != nil && !m.installed.External {
		release, e := m.acquire(ctx)
		if e != nil {
			r.Close()
			return nil, e
		}
		release()
	}
	return r, nil
}
func (r *Runtime) Close() {
	r.cancel()
	for _, m := range r.members {
		<-m.done
		m.mu.Lock()
		starting := m.starting
		m.mu.Unlock()
		if starting != nil {
			<-starting
		}
		m.mu.Lock()
		if m.process != nil {
			m.process.Close()
			m.process = nil
		}
		m.mu.Unlock()
	}
}
func (r *Runtime) Status() []MemberStatus {
	out := []MemberStatus{}
	for _, m := range r.members {
		m.mu.Lock()
		out = append(out, m.status)
		m.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (r *Runtime) Acquire(ctx context.Context, class string) (func(), error) {
	if m := r.members[class]; m != nil {
		return m.acquire(ctx)
	}
	return nil, fmt.Errorf("member unavailable")
}
func (r *Runtime) Offered(class string) bool {
	m := r.members[class]
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status.State == "dormant" || m.status.State == "starting" || m.status.State == "ready" || m.status.State == "external"
}
func (r *Runtime) Member(class string) (Member, bool) {
	m := r.members[class]
	if m == nil {
		return Member{}, false
	}
	return m.member, m.installed.Unavailable == ""
}
func (m *managed) acquire(ctx context.Context) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	m.mu.Lock()
	if m.installed.Unavailable != "" || m.ctx.Err() != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("member unavailable")
	}
	if m.installed.External {
		m.mu.Unlock()
		return func() {}, nil
	}
	m.refs++
	m.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			m.mu.Lock()
			m.refs--
			if m.refs == 0 {
				m.idle = time.Now()
			}
			m.mu.Unlock()
		})
	}
	for {
		m.mu.Lock()
		if m.status.State == "ready" {
			m.mu.Unlock()
			return release, nil
		}
		if m.starting == nil {
			if time.Now().Before(m.retry) {
				e := m.status.Error
				m.mu.Unlock()
				release()
				return nil, fmt.Errorf("member backoff: %s", e)
			}
			m.startLocked()
		}
		started := m.starting
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case <-m.ctx.Done():
			release()
			return nil, m.ctx.Err()
		case <-started:
		}
		m.mu.Lock()
		failed := m.status.State != "ready"
		detail := m.status.Error
		m.mu.Unlock()
		if failed {
			release()
			return nil, fmt.Errorf("member start: %s", detail)
		}
	}
}
func (m *managed) startLocked() {
	if m.ctx.Err() != nil {
		return
	}
	m.starting = make(chan struct{})
	m.status.State = "starting"
	m.status.Healthy = false
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		e := m.verify(ctx)
		var p *supervise.Process
		if e == nil {
			var cmd, env []string
			var work string
			m.mu.Lock()
			member := m.member
			if m.testPort != nil {
				member.Port = m.testPort(true)
			}
			m.mu.Unlock()
			cmd, env, work, e = Materialize(m.profile, member, m.installed, m.installation.Artifacts, m.dir)
			if e == nil {
				p, e = supervise.Start(cmd, env, work, strings.TrimPrefix(member.URL(), "http://"))
			}
		}
		if e == nil {
			e = p.WaitHealth(ctx, m.probe)
		}
		if e != nil && p != nil {
			p.Close()
			p = nil
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.process = p
		m.status.Healthy = e == nil
		if e == nil {
			m.status.State = "ready"
			m.status.Error = ""
			m.failures = 0
			m.backoff = 0
		} else {
			m.status.State = "backoff"
			m.status.Error = fmt.Sprintf("%.512s", e.Error())
			m.backoff = min(30*time.Second, max(time.Second, 2*m.backoff))
			m.retry = time.Now().Add(m.backoff)
			m.status.Restarts++
		}
		close(m.starting)
		m.starting = nil
	}()
}
func (m *managed) probe(ctx context.Context) error {
	m.mu.Lock()
	member := m.member
	if m.testPort != nil {
		member.Port = m.testPort(false)
		if member.Port > 0 && member.Port != m.member.Port {
			m.member.Port = member.Port
		}
	}
	m.mu.Unlock()
	member.Class = "probe"
	v := Check(ctx, member, nil, nil)
	if v.Err != nil {
		return v.Err
	}
	if v.State != "running" {
		return fmt.Errorf("engine not ready")
	}
	return nil
}
func (m *managed) loop() {
	defer close(m.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		if m.installed.External {
			m.mu.Unlock()
			ctx, cancel := context.WithTimeout(m.ctx, 2*time.Second)
			e := m.probe(ctx)
			cancel()
			m.mu.Lock()
			m.status.Healthy = e == nil
			m.mu.Unlock()
			continue
		}
		if m.installed.Unavailable != "" || m.starting != nil {
			m.mu.Unlock()
			continue
		}
		idle := m.member.Policy.Kind == "on-demand" && m.refs == 0 && !m.idle.IsZero() && time.Since(m.idle) >= time.Duration(m.member.Policy.IdleSeconds)*time.Second
		if idle {
			if m.process != nil {
				m.process.Close()
				m.process = nil
			}
			m.status.State = "dormant"
			m.status.Healthy = false
			m.mu.Unlock()
			continue
		}
		if m.process != nil && m.process.Exited() {
			m.failedLocked(time.Second)
		}
		if m.process == nil && (m.member.Policy.Kind != "on-demand" || m.refs > 0) && !time.Now().Before(m.retry) {
			m.startLocked()
			m.mu.Unlock()
			continue
		}
		probe := m.process != nil && m.refs == 0
		m.mu.Unlock()
		if probe {
			ctx, cancel := context.WithTimeout(m.ctx, 2*time.Second)
			e := m.probe(ctx)
			cancel()
			m.mu.Lock()
			if e == nil {
				m.failures = 0
			} else {
				m.failures++
				if m.failures >= 3 && m.refs == 0 && m.process != nil {
					m.failedLocked(time.Second)
				}
			}
			m.mu.Unlock()
		}
	}
}
func (m *managed) verify(ctx context.Context) error {
	roots := []string{}
	for _, path := range m.installed.Paths {
		roots = append(roots, path)
	}
	for id, path := range m.installation.Artifacts {
		if id == m.member.Artifact || strings.Contains(fmt.Sprint(m.member.Command, m.member.Env), "{artifact:"+id+"}") {
			roots = append(roots, path)
		}
	}
	selected := func(path string) bool {
		for _, root := range roots {
			if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
				return true
			}
		}
		return false
	}
	for path, sha := range m.installation.Files {
		if !selected(path) {
			continue
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		real, e := filepath.EvalSymlinks(path)
		if e != nil || real != path {
			return fmt.Errorf("installation path changed: %s", path)
		}
		got, e := fileHash(path)
		if e != nil || got != sha {
			return fmt.Errorf("installation bytes changed: %s", path)
		}
	}
	for path, target := range m.installation.Links {
		if selected(path) {
			got, e := os.Readlink(path)
			if e != nil || got != target {
				return fmt.Errorf("installation link changed: %s", path)
			}
		}
	}
	return nil
}
func (r *Runtime) Owns(class string) bool {
	m := r.members[class]
	return m != nil && !m.installed.External && m.installed.Unavailable == ""
}

func (m *managed) failedLocked(wait time.Duration) {
	m.process.Close()
	m.process = nil
	m.status.State = "backoff"
	m.status.Healthy = false
	m.retry = time.Now().Add(wait)
	m.status.Restarts++
}
