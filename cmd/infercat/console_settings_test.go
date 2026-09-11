package main

import (
	"context"
	"encoding/json"
	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/adminkey"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSettingsApplyAndRefusal(t *testing.T) {
	dir := t.TempDir()
	saveConfig(dir, config{Search: &searchConfig{KeyFile: "search.key"}, Name: "before", UpstreamKey: "keep-secret", UpstreamTranscribeKey: "keep-audio", Console: "127.0.0.1:9101"})
	remote, _ := adminkey.Open(dir)
	s := &consoleState{remote: remote, value: consoleSettings{Name: "before", DataDir: dir, ConsoleAddress: "127.0.0.1:9101", ConfiguredConsole: "127.0.0.1:9101"}}
	store, _ := keys.NewFileStore(dir)
	up, _ := upstream.Open(context.Background(), fakeEngine(t), "")
	e := &env{plat: testPlatform(fakeAddr, nil), out: io.Discard, errw: io.Discard}
	h := e.consoleAPI(store, fakeAddr, up, s.value, s)
	call := func(body string, remote bool, want int) {
		t.Helper()
		r := httptest.NewRequest("PATCH", "/settings", strings.NewReader(body))
		if remote {
			r.Header.Set("X-Infercat-Remote", "true")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body)
		}
	}
	before, _ := os.ReadFile(configPath(dir))
	for _, body := range []string{`{"name":"after","web_url":"http://example.com"}`, `{"name":"after","slots":-1}`, `{"console":"0.0.0.0:9101"}`, `{"upstream":"http://other"}`, `{"name":""}`, `{} {}`} {
		call(body, false, 400)
	}
	after, _ := os.ReadFile(configPath(dir))
	if string(before) != string(after) || s.name() != "before" {
		t.Fatal("refusal mutated")
	}
	call(`{"console":"127.0.0.1:9102","name":"after"}`, true, 400)
	call(`{"name":"after","web_url":"https://new.example/app/","slots":4,"console":"off","log_requests":true}`, false, 200)
	cfg, _ := loadConfig(dir)
	if cfg.Search == nil || cfg.Search.KeyFile != "search.key" || cfg.Name != "after" || cfg.Slots != 4 || cfg.Console != "off" || cfg.UpstreamKey != "keep-secret" || cfg.UpstreamTranscribeKey != "keep-audio" || s.name() != "after" || !s.logs.Load() {
		t.Fatal(cfg)
	}
	if s.snapshot().ConsoleAddress != "127.0.0.1:9101" {
		t.Fatal("listener changed live")
	}
	raw, _ := os.ReadFile(configPath(dir))
	if strings.Contains(string(raw), "log_requests") {
		t.Fatal("log persisted")
	}
	if err := os.Remove(configPath(dir)); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(configPath(dir), 0700)
	call(`{"name":"lost","log_requests":false}`, false, 400)
	if s.name() != "after" || !s.logs.Load() {
		t.Fatal("failed save changed live state")
	}
	call(`{"log_requests":false}`, false, 200)
	if s.logs.Load() {
		t.Fatal("run-only setting required config write")
	}
}
func TestAdminCodeLocalActions(t *testing.T) {
	dir := t.TempDir()
	store, _ := adminkey.Open(dir)
	s := &consoleState{remote: store, value: consoleSettings{Name: "host", DataDir: dir, WebURL: "https://example.com/"}}
	if _, err := s.remoteAction("enable", fakeAddr); err == nil {
		t.Fatal("enabled without listener")
	}
	s.value.ConsoleAddress = "127.0.0.1:9101"
	result, err := s.remoteAction("enable", fakeAddr)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	var value map[string]string
	json.Unmarshal(b, &value)
	code := value["invite"]
	if !strings.HasPrefix(code, "ia1."+fakeAddr+".") || !store.Authenticate(strings.TrimPrefix(code, "ia1."+fakeAddr+".")) {
		t.Fatal("code")
	}
	if _, err = s.remoteAction("enable", fakeAddr); err == nil {
		t.Fatal("duplicate enable")
	}
	s.remoteAction("off", fakeAddr)
	if store.State().Enabled {
		t.Fatal("off")
	}
	if _, err = os.Stat(filepath.Join(dir, "keys.json")); !os.IsNotExist(err) {
		t.Fatal("admin entered keys")
	}
}
func TestSettingsValidationAndConcurrentName(t *testing.T) {
	for _, url := range []string{"", "https://example.com/app/", "http://127.0.0.1:8080/", "http://[::1]/", "http://10.1.2.3/", "http://172.16.0.1/", "http://192.168.1.1/"} {
		if err := (admin.SettingsPatch{WebURL: &url}).Validate(false); err != nil {
			t.Fatal(url, err)
		}
	}
	for _, url := range []string{"http://example.com/", "http://172.15.0.1/", "https://user:secret@example.com/", "https://example.com/#fragment", "ftp://127.0.0.1/"} {
		if err := (admin.SettingsPatch{WebURL: &url}).Validate(false); err == nil {
			t.Fatal(url)
		}
	}
	dir := t.TempDir()
	remote, _ := adminkey.Open(dir)
	s := &consoleState{remote: remote, value: consoleSettings{Name: "old", DataDir: dir}}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.name()
				s.snapshot()
			}
		}()
	}
	name := "new"
	s.patch(admin.SettingsPatch{Name: &name}, false)
	wg.Wait()
}
