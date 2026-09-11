package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/keys"
)

func TestSearchConfigRefusesBeforeStartupAndPreservesPath(t *testing.T) {
	for _, value := range []string{"", "missing", "empty", "multiline"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			path := value
			if value == "empty" || value == "multiline" {
				b := []byte{}
				if value == "multiline" {
					b = []byte("secret\nsecret")
				}
				os.WriteFile(filepath.Join(dir, value), b, 0600)
			}
			if err := saveConfig(dir, config{Search: &searchConfig{KeyFile: path}}); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(configPath(dir))
			result := exec(t, testPlatform(fakeAddr, nil), "serve", "--data-dir", dir, "--console", "off")
			if result.code == 0 || !strings.Contains(result.err, "search key file") || strings.Contains(result.err, dir) || strings.Contains(result.err, "secret") {
				t.Fatal(result)
			}
			after, _ := os.ReadFile(configPath(dir))
			if string(after) != string(before) {
				t.Fatal("refusal changed config")
			}
		})
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "search.key"), []byte("secret"), 0600)
	saveConfig(dir, config{Search: &searchConfig{KeyFile: "search.key"}})
	plat := testPlatform(fakeAddr, nil)
	reached := false
	plat.startTunnel = func(context.Context, tunnelOptions) (tunnelServer, error) {
		reached = true
		return nil, errors.New("fixture stopped")
	}
	result := exec(t, plat, "serve", "--data-dir", dir, "--console", "off", "--upstream", fakeEngine(t))
	if !reached || !strings.Contains(result.err, "fixture stopped") {
		t.Fatal(result)
	}
	cfg, err := loadConfig(dir)
	if err != nil || cfg.Search == nil || cfg.Search.KeyFile != "search.key" {
		t.Fatal(cfg, err)
	}
	raw, _ := os.ReadFile(configPath(dir))
	if strings.Contains(string(raw), "secret") {
		t.Fatal("credential persisted")
	}
}
func TestSearchKeyFlags(t *testing.T) {
	dir := t.TempDir()
	p := testPlatform(fakeAddr, nil)
	r := exec(t, p, "keys", "add", "searcher", "--data-dir", dir, "--search-per-day", "7", "--no-qr")
	if r.code != 0 {
		t.Fatal(r)
	}
	store, _ := keys.NewFileStore(dir)
	all, err := store.List(context.Background())
	if err != nil || len(all) != 1 || all[0].Limits.SearchPerDay != 7 {
		t.Fatal(all, err)
	}
	r = exec(t, p, "keys", "limits", all[0].ID, "--data-dir", dir, "--search-per-day", "-1")
	if r.code != 0 {
		t.Fatal(r)
	}
	store, _ = keys.NewFileStore(dir)
	all, _ = store.List(context.Background())
	if all[0].Limits.SearchPerDay != -1 {
		t.Fatal(all)
	}
}
