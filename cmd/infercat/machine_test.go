package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/product"
)

type forbiddenInput struct{}

func (forbiddenInput) Read([]byte) (int, error) { panic("machine mode read stdin") }

func machineCall(t *testing.T, ctx context.Context, args ...string) (int, map[string]json.RawMessage, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := run(ctx, args, &out, &errw, forbiddenInput{}, true, newPlatform())
	if errw.Len() != 0 || strings.Contains(out.String(), "█") {
		t.Fatal("non-JSON output", errw.String(), out.String())
	}
	var result map[string]json.RawMessage
	d := json.NewDecoder(&out)
	raw := out.String()
	if err := d.Decode(&result); err != nil {
		t.Fatal(err, raw)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		t.Fatal("extra machine output", err)
	}
	return code, result, raw
}

func TestMachineVersionAndSchema(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"version", "--json"}, {"--json=1", "version"}} {
		code, result, raw := machineCall(t, context.Background(), args...)
		var v struct {
			Version, Commit, Date string
			Schemas               []int
		}
		if err := json.Unmarshal(result["data"], &v); err != nil {
			t.Fatal(err)
		}
		if code != 0 || len(result) != 4 || string(result["schema"]) != "1" || string(result["command"]) != `"version"` || v.Version != product.Version || v.Commit != product.Commit || v.Date != product.Date || len(v.Schemas) != 1 || v.Schemas[0] != 1 {
			t.Fatal(raw)
		}
	}
	for _, args := range [][]string{{"version", "--json=2"}, {"version", "--json", "--bad"}, {"version", "--json", "extra"}} {
		code, result, raw := machineCall(t, context.Background(), args...)
		if code != 2 || len(result) != 3 || result["error"] == nil {
			t.Fatal(code, raw)
		}
	}
	var out bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &out, io.Discard, nil, false, newPlatform()); code != 0 || out.String() != product.Name+" "+product.Version+" ("+product.Commit+", "+product.Date+")\n" {
		t.Fatal("human version changed", out.String())
	}
}

func TestMachineStatusPreservesBodyAndNoHost(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "ic195-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	code, result, raw := machineCall(t, context.Background(), "status", "--json", "--data-dir", dir)
	if code != 69 || !strings.Contains(string(result["error"]), `"host_stopped"`) {
		t.Fatal(code, raw)
	}
	st := admin.Status{Name: "owned fixture", Product: product.Name, Version: product.Version, Keys: []admin.Key{}}
	srv, err := admin.Serve(dir, func() admin.Status { return st }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	code, result, raw = machineCall(t, context.Background(), "--json=1", "--data-dir", dir, "status")
	expected, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || !bytes.Equal(result["data"], expected) {
		t.Fatal("route was changed", code, raw)
	}
	var host struct{ Version, Name string }
	if err := json.Unmarshal(result["host"], &host); err != nil || host.Version != st.Version || host.Name != st.Name {
		t.Fatal(raw)
	}
}

func TestMachineStatusTimeout(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "ic195-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	gate := make(chan struct{})
	srv, err := admin.Serve(dir, func() admin.Status { <-gate; return admin.Status{} }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	defer close(gate)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	code, result, raw := machineCall(t, ctx, "status", "--json", "--data-dir", dir)
	if code != 75 || !strings.Contains(string(result["error"]), `"host_not_responding"`) {
		t.Fatal(code, raw)
	}
}
