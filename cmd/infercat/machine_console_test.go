package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/admin"
)

func TestMachineConsoleOpensWithoutSecretOutput(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	token, err := os.ReadFile(filepath.Join(f.dir, admin.TokenName))
	if err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		var out, errw bytes.Buffer
		calls := 0
		e := &env{out: &out, errw: &errw, openBrowser: func(_ context.Context, link string) error {
			calls++
			if !strings.HasPrefix(link, "http://127.0.0.1:9101/#token=") || !strings.Contains(link, strings.TrimSpace(string(token))) {
				t.Error("opener did not receive authenticated local console URL")
			}
			if fail {
				return errors.New("unsafe opener diagnostic: " + link)
			}
			return nil
		}}
		code, handled := e.machineRead(context.Background(), []string{"--data-dir", f.dir, "console", "--json"})
		if !handled || calls != 1 || fail && code != 1 || !fail && code != 0 {
			t.Fatal(code, handled, calls)
		}
		if !fail && !strings.Contains(out.String(), `"data":{"opened":true}`) {
			t.Fatal(out.String())
		}
		if bytes.Contains(out.Bytes(), bytes.TrimSpace(token)) || strings.Contains(out.String(), "#token") || errw.Len() != 0 {
			t.Fatal("secret-bearing output or stderr")
		}
	}
}

func TestMachineConsolePrintRefusesBeforeClientOrBrowser(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	e := &env{out: &out, openBrowser: func(context.Context, string) error { t.Error("opened on refusal"); return nil }}
	for _, flag := range []string{"--print", "--print=false", "--print=true"} {
		out.Reset()
		code, _ := e.machineRead(context.Background(), []string{"--data-dir", t.TempDir(), "console", flag, "--json"})
		if code != 2 || !strings.Contains(out.String(), `"code":"invalid_arguments"`) {
			t.Fatal(code, out.String())
		}
	}
}
