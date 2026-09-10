package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	runstate "github.com/infercat/infercat/internal/run"
)

// Three descriptions, no task-specific instruction or permission fiction.
const makeImageDescription = "Make an image when the person asks for one. Write a clear image prompt and call this tool once. It starts background image jobs and returns their ids; the images appear separately. After the result, briefly tell the person what is being made. Do not call again in this turn."
const imagePromptDescription = "Describe the image to make, including the subject and any requested style. Use the person's request to write the prompt. Do not include commands, tool syntax, or job ids."
const imageCountDescription = "How many images to make from this prompt. Use 1 unless the person asks for more. Send an integer within the stated maximum."

func makeImageTool(cap int) map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name": "make_image", "description": makeImageDescription,
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"prompt": map[string]any{"type": "string", "description": imagePromptDescription},
			"count":  map[string]any{"type": "integer", "minimum": 1, "maximum": cap, "description": imageCountDescription},
		}, "required": []string{"prompt", "count"}, "additionalProperties": false},
	}}
}

func asksHostTools(body map[string]any) (bool, *gwError) {
	value, present := body["host_tools"]
	if !present {
		return false, nil
	}
	tools, ok := value.([]any)
	if !ok {
		return false, errf(CodeInvalidRequest, 0, "host_tools must be an array containing make_image")
	}
	if len(tools) == 0 {
		return false, nil
	}
	if len(tools) != 1 || tools[0] != "make_image" {
		return false, errf(CodeInvalidRequest, 0, "the only host tool is make_image, once per turn")
	}
	if _, ok := body["tools"]; ok {
		return false, errf(CodeInvalidRequest, 0, "host_tools cannot be combined with caller tools")
	}
	if _, ok := body["tool_choice"]; ok {
		return false, errf(CodeInvalidRequest, 0, "host_tools cannot be combined with caller tool_choice")
	}
	return true, nil
}

type imageToolArgs struct {
	Prompt string `json:"prompt"`
	Count  int    `json:"count"`
}

func parseImageTool(raw string, cap int) (imageToolArgs, error) {
	var args imageToolArgs
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return args, fmt.Errorf("make_image needs a prompt and an integer count")
	}
	if decoder.Decode(new(any)) != io.EOF || args.Count < 1 || args.Count > cap {
		return args, fmt.Errorf("make_image count must be between 1 and %d", cap)
	}
	input, _ := json.Marshal(runstate.ImageInput{Prompt: args.Prompt})
	if err := runstate.ValidateImage(input); err != nil {
		return args, fmt.Errorf("make_image needs a nonempty bounded image prompt without engine directives")
	}
	return args, nil
}

// One accumulator consumes original engine SSE, while its observer forwards only
// assistant text/reasoning. Tool calls never become client-executable instructions.
type hostToolReply struct {
	pending      []byte
	content      strings.Builder
	reasoning    strings.Builder
	calls        []chatToolCall
	finished     bool
	finishReason string
}

func (p *hostToolReply) consume(raw []byte, emit func(chatDelta) error) error {
	p.pending = append(p.pending, raw...)
	if len(p.pending) > runstate.MaxOutput {
		return runstate.ErrLimit
	}
	for {
		line, rest, ok := bytes.Cut(p.pending, []byte{'\n'})
		if !ok {
			return nil
		}
		p.pending = rest
		value, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
		if !ok {
			continue
		}
		value = bytes.TrimSpace(value)
		if bytes.Equal(value, []byte("[DONE]")) {
			p.finished = p.finishReason != ""
			continue
		}
		var chunk sseChunk
		if json.Unmarshal(value, &chunk) != nil {
			return fmt.Errorf("model returned invalid chat stream")
		}
		if len(chunk.Error) > 0 && !bytes.Equal(chunk.Error, []byte("null")) {
			return fmt.Errorf("model stream failed")
		}
		if len(chunk.Choices) > 1 {
			return fmt.Errorf("model returned more than one reply")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				return fmt.Errorf("model returned an unexpected reply index")
			}
			if choice.FinishReason != nil {
				p.finishReason = *choice.FinishReason
			}
			d := choice.Delta
			p.content.WriteString(d.Content)
			p.reasoning.WriteString(d.reasoning())
			for _, delta := range d.ToolCalls {
				if delta.Index < 0 || delta.Index >= 16 {
					return fmt.Errorf("too many tool calls")
				}
				for len(p.calls) <= delta.Index {
					p.calls = append(p.calls, chatToolCall{})
				}
				call := &p.calls[delta.Index]
				if delta.ID != "" && delta.ID != call.ID {
					call.ID += delta.ID
				}
				if delta.Function.Name != "" && delta.Function.Name != call.Function.Name {
					call.Function.Name += delta.Function.Name
				}
				call.Function.Arguments += delta.Function.Arguments
				call.Type = "function"
			}
			if d.Content != "" || d.reasoning() != "" {
				d.ToolCalls = nil
				if err := emit(d); err != nil {
					return err
				}
			}
		}
	}
}
func (p *hostToolReply) message() map[string]any {
	message := map[string]any{"role": "assistant", "content": p.content.String()}
	if len(p.calls) > 0 {
		calls := make([]any, 0, len(p.calls))
		for _, call := range p.calls {
			calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Function.Name, "arguments": call.Function.Arguments}})
		}
		message["tool_calls"] = calls
	}
	return message
}
