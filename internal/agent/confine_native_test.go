//go:build darwin || linux

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPinned161ReadCanariesAndEphemeralWorkspace(t *testing.T) {
	installed := os.Getenv("INFERCAT_AGENT_TEST_INSTALL")
	if installed == "" {
		t.Skip("explicit pinned sandbox fixture")
	}
	t.Setenv("EXA_API_KEY", "legacy-env-must-not-be-used")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(runtimeDir(installed), runtimeDir(dir)); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(dir, "keys-canary.json")
	if err := os.WriteFile(canary, []byte("SYNTHETIC_HOST_CONTENT_161"), 0600); err != nil {
		t.Fatal(err)
	}
	fakeHome := t.TempDir()
	if err := os.Mkdir(filepath.Join(fakeHome, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(fakeHome, ".ssh", "id_fake")
	if err := os.WriteFile(ssh, []byte("SYNTHETIC_SSH_CONTENT_161"), 0600); err != nil {
		t.Fatal(err)
	}
	ks, _ := keys.NewFileStore(dir)
	k, _, err := ks.Add(context.Background(), "fixture", keys.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if err = ks.SetLimitsAndAgent(context.Background(), k.ID, k.Limits, &yes); err != nil {
		t.Fatal(err)
	}
	store, _ := runstate.NewStore(dir)
	var calls atomic.Int32
	m, _ := runstate.New(store, func(ctx context.Context, _ string, step runstate.Step, acquired func() error) (runstate.StepResult, error) {
		if e := acquired(); e != nil {
			return runstate.StepResult{Settled: true}, e
		}
		var req struct{ Tools []any }
		_ = json.Unmarshal(step.Input, &req)
		n := int32(0)
		if len(req.Tools) > 0 {
			n = calls.Add(1)
		}
		delta := map[string]any{"content": "Canaries checked."}
		reason := "stop"
		var tool string
		var args map[string]string
		switch n {
		case 1:
			tool = "read"
			args = map[string]string{"file_path": canary}
		case 2:
			tool = "bash"
			args = map[string]string{"command": "cat " + shellQuote(ssh)}
		case 3:
			tool = "bash"
			args = map[string]string{"command": "python3 -c " + shellQuote("import pathlib;print(pathlib.Path("+fmt.Sprintf("%q", canary)+").read_text())")}
		case 4:
			tool = "bash"
			args = map[string]string{"command": `test -z "$EXA_API_KEY" || exit 9; if [ -d /proc ]; then cat "/proc/$PPID/environ" > initial-env || exit 8; if grep -aq EXA_API_KEY initial-env; then exit 7; fi; rm initial-env; fi; printf CREDENTIAL_ABSENT`}
		case 5:
			tool = "write"
			args = map[string]string{"file_path": "proof.txt", "content": "captured before workspace removal\n"}
		}
		if tool == "bash" {
			args["description"] = "Check the isolated synthetic canary"
		}
		if tool != "" {
			raw, _ := json.Marshal(args)
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("call_", n), "type": "function", "function": map[string]string{"name": tool, "arguments": string(raw)}}}}
			reason = "tool_calls"
		}
		event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
		e := step.Observe([]byte("data: " + string(event) + "\n\ndata: [DONE]\n\n"))
		return runstate.StepResult{Output: json.RawMessage(`{}`), Dispatched: true, Settled: true}, e
	}, nil)
	keyPath := filepath.Join(dir, "search.key")
	if err = os.WriteFile(keyPath, []byte("synthetic-161-secret-descriptor"), 0600); err != nil {
		t.Fatal(err)
	}
	searchKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	a := StartAdapter(context.Background(), dir, m, ks, string(searchKey))
	defer a.Close()
	defer m.Close()
	until := time.Now().Add(10 * time.Second)
	for a.Status().State != "healthy" && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	r, err := m.Submit(k.ID, "agent", "", json.RawMessage(`{"model":"fixture","messages":[{"role":"user","content":"Check the synthetic canaries."}]}`))
	if err != nil {
		t.Fatal(err, a.Status())
	}
	until = time.Now().Add(20 * time.Second)
	for time.Now().Before(until) {
		r, _ = store.Get(k.ID, r.ID)
		if r.State == runstate.Done || r.State == runstate.Failed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.State != runstate.Done {
		t.Fatal(r.State, r.Reason, a.Status())
	}
	held, err := store.Retained(k.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(held)
	for _, secret := range []string{"SYNTHETIC_HOST_CONTENT_161", "SYNTHETIC_SSH_CONTENT_161", "synthetic-161-secret-descriptor"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("canary leaked into retained output")
		}
	}
	failed, credential, captured := 0, false, false
	for _, step := range held.Steps {
		if step.Kind == "read" || step.Kind == "run" {
			text := strings.ToLower(string(held.Outputs[step.OutputID].Data))
			if strings.Contains(text, "operation not permitted") || strings.Contains(text, "permission denied") || strings.Contains(text, "permissionerror") || strings.Contains(text, "eperm") {
				failed++
			}
		}
		if step.Result == "CREDENTIAL_ABSENT" {
			credential = true
		}
	}
	for _, output := range held.Outputs {
		if output.Name == "proof.txt" && string(output.Data) == "captured before workspace removal\n" {
			captured = true
		}
	}
	if failed != 3 || !credential || !captured {
		t.Fatalf("denials=%d credential=%v captured=%v steps=%+v", failed, credential, captured, held.Steps)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "agent", "workspaces"))
	if err != nil || len(entries) != 0 {
		t.Fatal("ephemeral workspace survived", entries, err)
	}
	if a.currentRuntime() != nil {
		t.Fatal("child retained after terminal settlement")
	}
	t.Log("native read/bash/Python denied host/home canaries; descriptor absent from subprocess and initial environment; captured output survived workspace deletion")
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func TestPinned161InheritedProviderRefusesOtherPolicies(t *testing.T) {
	installed := os.Getenv("INFERCAT_AGENT_TEST_INSTALL")
	if installed == "" {
		t.Skip("explicit pinned sandbox fixture")
	}
	root, err := Installed(installed)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(workspace, "provider.mjs")
	if err = os.WriteFile(provider, inheritedSandbox, 0600); err != nil {
		t.Fatal(err)
	}
	script := `import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
const require=createRequire(process.env.INFERCAT_AGENT_RUNTIME+'/package.json');
const {Context}=await import(pathToFileURL(require.resolve('@deepseek-ai/cordis')).href);
const {default:Provider}=await import(pathToFileURL(process.argv[1]).href);
const workspace=process.argv[2];
assert.throws(()=>new Provider(new Context(),{workspace}),/outer confinement/);
process.env.INFERCAT_CONFINED_WORKSPACE=workspace;
const p=new Provider(new Context(),{workspace});
const argv=['python3','-c','print("ok")'];
assert.deepEqual(p.confine(argv,{mode:'workspace-write',workspaceRoot:workspace}).argv,argv);
for(const mode of ['read-only','danger-full-access']) assert.throws(()=>p.confine(argv,{mode,workspaceRoot:workspace}),/unsupported policy/);
assert.throws(()=>p.confine(argv,{mode:'workspace-write',workspaceRoot:'/'}),/unsupported policy/);
console.log('exact root accepted; missing outer marker and other policies refused');`
	cmd := exec.Command(filepath.Join(root, "node/bin/node"), "--input-type=module", "-e", script, provider, workspace)
	cmd.Env = []string{"INFERCAT_AGENT_RUNTIME=" + root}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	} else {
		t.Log(string(out))
	}
}
