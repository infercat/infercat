package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMissingHarnessIsStatusFailure(t *testing.T) {
	r := StartHarness(context.Background(), t.TempDir())
	defer r.Close()
	if s := r.Status(); s.State != "failed" || s.LastError == "" {
		t.Fatalf("missing runtime: %+v", s)
	}
}

// Explicit live fixture: uses a separate host directory and the installed,
// pinned runtime. No engine or network model provider is used.
func TestPinnedHarnessIPC(t *testing.T) {
	installed := os.Getenv("INFERCAT_AGENT_TEST_INSTALL")
	if installed == "" {
		t.Skip("set INFERCAT_AGENT_TEST_INSTALL for the pinned harness proof")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(runtimeDir(installed), runtimeDir(dir)); err != nil {
		t.Fatal(err)
	}
	options, err := HarnessOptions(dir)
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan map[string]any, 256)
	options.Frame = func(raw json.RawMessage) {
		var frame map[string]any
		if json.Unmarshal(raw, &frame) == nil {
			select {
			case output <- frame:
			default:
			}
		}
	}
	r := StartRuntime(context.Background(), options)
	defer r.Close()
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) && r.Status().State != "healthy" {
		time.Sleep(50 * time.Millisecond)
	}
	if s := r.Status(); s.State != "healthy" {
		log, _ := os.ReadFile(filepath.Join(options.Dir, "runtime.log"))
		t.Fatalf("native readiness: %+v\n%s", s, log)
	}
	start, _ := json.Marshal(map[string]any{"type": "start", "id": "fixture-run", "cwd": options.Dir, "model": "fixture", "text": "Say hello."})
	if err = r.Send(start); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	events, model, settled := 0, false, false
	var cancelledCall any
	for !settled {
		select {
		case frame := <-output:
			switch frame["type"] {
			case "error":
				t.Logf("native error: %v", frame["message"])
			case "event":
				events++
			case "model":
				model = true
				cancelledCall = frame["id"]
				if err = r.Send(json.RawMessage(`{"type":"cancel","id":"fixture-run"}`)); err != nil {
					t.Fatal(err)
				}
			case "settled":
				settled = true
				if frame["cancelled"] != true {
					t.Fatal("cancel was not settled", frame)
				}
			}
		case <-timer.C:
			log, _ := os.ReadFile(filepath.Join(options.Dir, "runtime.log"))
			t.Fatalf("native model/cancel timeout, model=%v events=%d state=%+v\n%s", model, events, r.Status(), log)
		}
	}
	if !model || events == 0 {
		t.Fatalf("model=%v events=%d", model, events)
	}
	t.Logf("native harness: readiness healthy; model request over IPC; %d session events; cancel settled", events)
	pid := r.Status().PID
	late, _ := json.Marshal(map[string]any{"type": "model_result", "id": cancelledCall, "chunks": []any{}})
	if err = r.Send(late); err != nil {
		t.Fatal(err)
	}
	start, _ = json.Marshal(map[string]any{"type": "start", "id": "next-run", "cwd": options.Dir, "model": "fixture", "text": "Say hello."})
	if err = r.Send(start); err != nil {
		t.Fatal(err)
	}
	settled, model = false, false
	hello := false
	for !settled {
		select {
		case frame := <-output:
			if frame["type"] == "event" && frame["run_id"] == "next-run" {
				event, _ := frame["event"].(map[string]any)
				if event["type"] == "assistant/message" {
					data, _ := event["data"].(map[string]any)
					message, _ := data["message"].(map[string]any)
					content, _ := message["content"].([]any)
					for _, part := range content {
						block, _ := part.(map[string]any)
						if block["type"] == "text" && block["text"] == "Hello." {
							hello = true
						}
					}
				}
			}
			if frame["type"] == "model" {
				model = true
				reply, _ := json.Marshal(map[string]any{"type": "model_result", "id": frame["id"], "chunks": []any{
					map[string]any{"type": "block-start", "index": 0, "blockType": "text"},
					map[string]any{"type": "text-delta", "index": 0, "text": "Hello."},
					map[string]any{"type": "block-end", "index": 0, "block": map[string]any{"type": "text", "text": "Hello."}},
					map[string]any{"type": "finish", "reason": map[string]any{"kind": "stop"}},
				}})
				if err = r.Send(reply); err != nil {
					t.Fatal(err)
				}
			}
			if frame["type"] == "settled" && frame["run_id"] == "next-run" {
				settled = true
				if frame["failed"] != false || frame["cancelled"] != false {
					t.Fatal(frame)
				}
			}
		case <-timer.C:
			t.Fatal("successor did not settle")
		}
	}
	if !model || !hello || r.Status().PID != pid || r.Status().Restarts != 0 {
		t.Fatal("runtime not reused", r.Status())
	}
	t.Log("late cancelled reply ignored; successor model call and settlement on the same process")
	entries, err := os.ReadDir(filepath.Join(options.Dir, "harness", "sessions"))
	if (err != nil && !os.IsNotExist(err)) || len(entries) != 0 {
		t.Fatal("adapter created a second trajectory owner", err, entries)
	}
}
