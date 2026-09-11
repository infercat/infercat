//go:build darwin || linux

package agent

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func sandboxCanary(args []string) int {
	workspace, canary, other, endpoint, cert, node := args[0], args[1], args[2], args[3], args[4], args[5]
	for _, p := range []string{canary, other} {
		if _, err := os.ReadFile(p); !errors.Is(err, os.ErrPermission) {
			fmt.Fprintln(os.Stderr, "read not denied", p, err)
			return 1
		}
		if f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0600); err == nil {
			f.Close()
			fmt.Fprintln(os.Stderr, "truncate allowed")
			return 1
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "made"), []byte("workspace"), 0600); err != nil {
		return 1
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), "made"), []byte("tmp"), 0600); err != nil {
		return 1
	}
	script := filepath.Join(workspace, "tool.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf tool-ok"), 0700); err != nil {
		return 1
	}
	commands := [][]string{
		{script},
		{"/bin/bash", "-c", `if cat "$1" >/dev/null 2>&1; then exit 9; fi; printf bash-ok`, "_", canary},
		{"python3", "-c", "import pathlib,sys\ntry: pathlib.Path(sys.argv[1]).read_text()\nexcept PermissionError: print('python-ok')\nelse: raise Exception('canary read')", canary},
		{node, "-e", `try{require('fs').readFileSync(process.argv[1]);process.exit(9)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e;console.log('node-ok')}`, canary},
		{"curl", "--max-time", "5", "--fail", "--silent", "--cacert", cert, endpoint},
	}
	if os.Getenv("INFERCAT_REQUIRE_SANDBOX") == "1" {
		commands = append(commands, []string{"curl", "--max-time", "10", "--fail", "--silent", "--head", "https://example.com"})
	}
	for _, argv := range commands {
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		if err != nil {
			fmt.Fprintln(os.Stderr, argv[0], err, string(out))
			return 1
		}
		fmt.Print(string(out), "\n")
	}
	child := exec.Command("/bin/sleep", "20")
	if err := child.Start(); err != nil {
		return 1
	}
	if err := child.Process.Kill(); err != nil {
		fmt.Fprintln(os.Stderr, "owned child signal denied", err)
		return 1
	}
	_ = child.Wait()
	if runtime.GOOS == "linux" {
		if _, err := os.ReadFile("/proc/self/status"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	fmt.Println("canaries denied; workspace, tmp, tools, TLS and child cancellation passed")
	return 0
}

func Test161SandboxCanaries(t *testing.T) {
	workspace, err := sandboxWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	protected := t.TempDir()
	canary := filepath.Join(protected, "keys.json")
	other := filepath.Join(protected, ".ssh", "id_fake")
	if err = os.Mkdir(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{canary, other} {
		if err = os.WriteFile(p, []byte("synthetic-private-data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	binary, _ := os.Executable()
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(workspace, "probe")
	if err = os.WriteFile(payload, raw, 0700); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("canary needs node", err)
	}
	installed := filepath.Dir(binary)
	if fixture := os.Getenv("INFERCAT_AGENT_TEST_INSTALL"); fixture != "" {
		installed, err = Installed(fixture)
		if err != nil {
			t.Fatal(err)
		}
		node = filepath.Join(installed, "node/bin/node")
	}
	installed, err = filepath.EvalSymlinks(installed)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "https-ok") }))
	defer server.Close()
	cert := filepath.Join(workspace, "ca.pem")
	if err = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	argv, err := sandboxCommand(workspace, installed, []string{payload, "_sandbox-canary", workspace, canary, other, server.URL, cert, node})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workspace
	cmd.Env = []string{"PATH=" + filepath.Dir(node) + sandboxPath(), "HOME=" + workspace, "TMPDIR=" + filepath.Join(workspace, "tmp"), "INFERCAT_REQUIRE_SANDBOX=" + os.Getenv("INFERCAT_REQUIRE_SANDBOX")}
	output, err := cmd.CombinedOutput()
	if err != nil && runtime.GOOS == "linux" && strings.Contains(string(output), "Landlock ABI 3") && os.Getenv("INFERCAT_REQUIRE_SANDBOX") == "" {
		t.Skip("unsupported kernel correctly refuses confinement")
	}
	if err != nil {
		t.Fatalf("sandbox canaries: %v\n%s", err, output)
	}
	for _, p := range []string{canary, other} {
		content, e := os.ReadFile(p)
		if e != nil || string(content) != "synthetic-private-data" {
			t.Fatal("protected bytes changed", e)
		}
	}
	t.Log(string(output))
}

func Test161LayoutGuard(t *testing.T) {
	for _, root := range sandboxTrees() {
		canonical, e := filepath.EvalSymlinks(root)
		if e != nil {
			continue
		}
		if sandboxLayout(filepath.Join(canonical, "infercat-data"), "/separate/runtime") == nil {
			t.Fatal("readable data layout allowed", root)
		}
	}
	if sandboxLayout("/data/host", "/data") == nil {
		t.Fatal("runtime containing data allowed")
	}
	if sandboxLayout("/data/host", "/data/host/agent/runtime") != nil {
		t.Fatal("ordinary layout refused")
	}
}
