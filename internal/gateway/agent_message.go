package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	runstate "github.com/infercat/infercat/internal/run"
	"log"
	"strings"
)

// Retain a bounded final message, not the transport's per-token SSE envelopes.
func agentMessage(raw []byte) (json.RawMessage, error) {
	type function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	type call struct {
		ID       string   `json:"id"`
		Type     string   `json:"type"`
		Function function `json:"function"`
	}
	calls := []call{}
	text := ""
	finish := ""
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 4096), runstate.MaxOutput)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			continue
		}
		var frame struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content   string
					ToolCalls []struct {
						Index    int
						ID, Type string
						Function function
					} `json:"tool_calls"`
				}
				Finish string `json:"finish_reason"`
			}
		}
		if json.Unmarshal([]byte(data), &frame) != nil || len(frame.Error) > 0 {
			return nil, runstate.ErrInvalid
		}
		for _, choice := range frame.Choices {
			text += choice.Delta.Content
			for _, c := range choice.Delta.ToolCalls {
				if c.Index < 0 || c.Index > 127 {
					return nil, runstate.ErrLimit
				}
				for len(calls) <= c.Index {
					calls = append(calls, call{})
				}
				out := &calls[c.Index]
				if out.ID == "" {
					out.ID = c.ID
				}
				if out.Type == "" {
					out.Type = c.Type
				}
				if out.Function.Name == "" {
					out.Function.Name = c.Function.Name
				}
				out.Function.Arguments += c.Function.Arguments
			}
			if choice.Finish != "" {
				finish = choice.Finish
			}
		}
	}
	if scan.Err() != nil || finish == "" {
		return nil, runstate.ErrInvalid
	}
	result, err := json.Marshal(struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Calls   []call `json:"tool_calls,omitempty"`
	}{"assistant", text, calls})
	if len(result) > runstate.MaxOutput {
		return nil, runstate.ErrLimit
	}
	return result, err
}

func agentRecord(raw []byte) json.RawMessage {
	result, err := agentMessage(raw)
	if err == nil {
		return result
	}
	log.Printf("agent model record conversion: bounded raw fallback (%v)", err)
	raw = raw[max(0, len(raw)-(64<<10)):]
	result, _ = json.Marshal(map[string]any{"raw_fallback": true, "raw": string(raw)})
	return result
}
