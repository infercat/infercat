package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/bridge"
	"github.com/infercat/infercat/internal/product"
	"github.com/rogpeppe/go-internal/testscript"
)

var updateCLI = flag.Bool("update-cli-contract", false, "regenerate CLI contract fixtures")

func TestMachineScripts(t *testing.T) {
	t.Parallel()
	testscript.Run(t, testscript.Params{Dir: "testdata/machine", UpdateScripts: *updateCLI, RequireExplicitExec: true, RequireUniqueNames: true,
		Setup: func(e *testscript.Env) error {
			dir, err := os.MkdirTemp("", "ic195-script-")
			if err != nil {
				return err
			}
			e.Defer(func() { os.RemoveAll(dir) })
			e.Setenv("HOST", dir)
			e.Setenv("NOHOST", filepath.Join(dir, "stopped"))
			e.Setenv("VERSION", product.Version)
			e.Setenv("COMMIT", product.Commit)
			e.Setenv("DATE", product.Date)
			e.Setenv("HOSTNAME", hostDisplayName(""))
			e.Setenv("GORACE", "atexit_sleep_ms=0")
			if err := bridge.Save(dir, bridge.Config{Endpoint: "https://example.test", Host: "test", Token: strings.Repeat("b", 64)}); err != nil {
				return err
			}
			server, err := admin.Serve(dir, func() admin.Status {
				st := admin.Status{Name: "script host", Keys: []admin.Key{}}
				if _, err := os.Stat(filepath.Join(dir, "console")); err == nil {
					st.Console = "127.0.0.1:9101"
				}
				return st
			}, nil, admin.NewEvents(nil), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if control, _ := os.ReadFile(filepath.Join(dir, "failure")); len(control) > 0 {
					w.WriteHeader(409)
					io.WriteString(w, `{"error":"fixture conflict"}`)
					return
				}
				switch {
				case r.URL.Path == "/keys" && r.Method == "GET":
					io.WriteString(w, `[]`)
				case r.URL.Path == "/stored" && r.Method == "GET":
					io.WriteString(w, `{"cursor":"cursor-1","terminal":2,"clear_images":1,"retry_cleanup":0}`)
				default:
					io.WriteString(w, `{ "kept": [null, 0], "empty": [], "future": "<unchanged>" }`)
				}
			}))
			if err != nil {
				return err
			}
			e.Defer(func() { server.Close() })
			return nil
		},
		Cmds: map[string]func(*testscript.TestScript, bool, []string){"exit": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg || len(args) < 2 {
				ts.Fatalf("exit CODE COMMAND [ARGS]")
			}
			want, err := strconv.Atoi(args[0])
			ts.Check(err)
			err = ts.Exec(args[1], args[2:]...)
			got := 0
			if err != nil {
				var status *osexec.ExitError
				if !errors.As(err, &status) {
					ts.Fatalf("%v", err)
					return
				}
				got = status.ExitCode()
			}
			if got != want {
				ts.Fatalf("exit = %d, want %d (%v)", got, want, err)
			}
		}},
	})
}

// Generate the operation scripts from the same argv examples the parity walk executes.
func TestMachineScriptCoverage(t *testing.T) {
	for _, op := range machineOperations {
		// 196 owns the service verbs and their fake-launchctl/systemctl JSON proofs.
		// Never run those verbs against this machine's real supervisor from a golden.
		if strings.HasPrefix(op.Operation, "service.") {
			if _, ok := serviceVerbs[strings.TrimPrefix(op.Operation, "service.")]; !ok {
				t.Fatal("unknown service operation", op.Operation)
			}
			continue
		}
		path := filepath.Join("testdata", "machine", strings.ReplaceAll(op.Operation, ".", "_")+".txtar")
		if _, err := os.Stat(path); err != nil {
			t.Error(fmt.Errorf("missing subprocess golden for %s: %w", op.Operation, err))
		}
	}
}
