package main

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/adminkey"
)

type consoleState struct {
	mu     sync.RWMutex
	value  consoleSettings
	remote *adminkey.Store
	logs   atomic.Bool
}

func (s *consoleState) name() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.value.Name }
func (s *consoleState) snapshot() consoleSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.value
	state := s.remote.State()
	v.Remote = &state
	v.LogRequests = s.logs.Load()
	return v
}
func (s *consoleState) patch(p admin.SettingsPatch, remote bool) (consoleSettings, error) {
	if err := p.Validate(remote); err != nil {
		return consoleSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	remembered := p.Name != nil || p.WebURL != nil || p.Slots != nil || p.Console != nil
	next := s.value
	if remembered {
		cfg, err := loadConfig(next.DataDir)
		if err != nil {
			return consoleSettings{}, err
		}
		if p.Name != nil {
			cfg.Name = *p.Name
			next.Name = hostDisplayName(*p.Name)
		}
		if p.WebURL != nil {
			cfg.WebURL = *p.WebURL
			next.ConfiguredWebURL = *p.WebURL
			next.WebURL = webURL(cfg)
		}
		if p.Slots != nil {
			cfg.Slots = *p.Slots
			next.Slots = *p.Slots
		}
		if p.Console != nil {
			cfg.Console = *p.Console
			next.ConfiguredConsole = *p.Console
		}
		if err := saveConfig(next.DataDir, cfg); err != nil {
			return consoleSettings{}, errors.New("not saved — config.json is not writable")
		}
	}
	next.SavedAt = make(map[string]time.Time, len(s.value.SavedAt)+5)
	for k, v := range s.value.SavedAt {
		next.SavedAt[k] = v
	}
	now := time.Now().UTC()
	for key, set := range map[string]bool{"name": p.Name != nil, "web_url": p.WebURL != nil, "slots": p.Slots != nil, "console": p.Console != nil, "log_requests": p.LogRequests != nil} {
		if set {
			next.SavedAt[key] = now
		}
	}
	if p.LogRequests != nil {
		s.logs.Store(*p.LogRequests)
	}
	s.value = next
	next.LogRequests = s.logs.Load()
	state := s.remote.State()
	next.Remote = &state
	return next, nil
}
func (s *consoleState) remoteAction(action, addr string) (any, error) {
	if action == "off" {
		if err := s.remote.Disable(); err != nil {
			return nil, err
		}
		return admin.RemoteResult{OK: true}, nil
	}
	v := s.snapshot()
	if v.ConsoleAddress == "" {
		return nil, errors.New("remote access requires the loopback console listener on")
	}
	secret, err := s.remote.Mint(action == "rotate")
	if err != nil {
		return nil, err
	}
	code := "ia1." + addr + "." + secret
	link := ""
	if v.WebURL != "" {
		link = v.WebURL + "#" + code
	}
	return admin.RemoteResult{KeyID: "admin", Name: v.Name, Invite: code, Link: link}, nil
}
func consoleRemote(r *http.Request) bool { return r.Header.Get("X-Infercat-Remote") == "true" }
