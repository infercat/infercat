package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
)

const responsesEndpoint endpoint = "/v1/responses"

// The mapping lives for one request only. The client owns Responses history;
// neither ciphertext nor a response-id cache belongs to the chat engine.
type responsesAdapter struct {
	tools map[string]responseToolName
}
type responseToolName struct{ Namespace, Name string }

func chatToolName(namespace, name string) (string, *gwError) {
	if !validToolName(name) || (namespace != "" && !validToolName(namespace)) {
		return "", errf(CodeInvalidRequest, 0, "function and namespace names must be 1–64 ASCII letters, digits, underscores or hyphens")
	}
	if namespace == "" {
		return name, nil
	}
	hash := sha256.Sum256([]byte(namespace + "\x00" + name))
	return "ns_" + hex.EncodeToString(hash[:16]), nil
}
func validToolName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func str(m map[string]any, key string) string { s, _ := m[key].(string); return s }

func (a *responsesAdapter) name(namespace, name string) (string, *gwError) {
	alias, err := chatToolName(namespace, name)
	if err != nil {
		return "", err
	}
	original := responseToolName{namespace, name}
	if old, ok := a.tools[alias]; ok && old != original {
		return "", errf(CodeInvalidRequest, 0, "function names collide after namespace translation")
	}
	a.tools[alias] = original
	return alias, nil
}

func translateResponses(body map[string]any) (map[string]any, *responsesAdapter, *gwError) {
	a := &responsesAdapter{tools: map[string]responseToolName{}}
	for _, field := range []string{"previous_response_id", "conversation"} {
		if body[field] != nil {
			return nil, nil, errf(CodeInvalidRequest, 0, "%s is not supported; send store:false and the full input history", field)
		}
	}
	for _, field := range []string{"store", "background", "stream"} {
		if v := body[field]; v != nil {
			b, ok := v.(bool)
			if !ok {
				return nil, nil, errf(CodeInvalidRequest, 0, "%s must be a boolean", field)
			}
			if b && field != "stream" {
				return nil, nil, errf(CodeInvalidRequest, 0, "%s:true is not supported; this route is stateless", field)
			}
		}
	}
	// Select only fields whose chat meaning is known. Never forward Responses state
	// or engine-specific override fields as if they were prompt content.
	chat := map[string]any{}
	for _, key := range []string{"model", "stream", "temperature", "top_p", "parallel_tool_calls"} {
		if v, ok := body[key]; ok {
			chat[key] = v
		}
	}
	if v, ok := body["max_output_tokens"]; ok {
		n, ok := v.(json.Number)
		if !ok {
			return nil, nil, errf(CodeInvalidRequest, 0, "max_output_tokens must be a positive integer")
		}
		count, err := n.Int64()
		if err != nil || count <= 0 {
			return nil, nil, errf(CodeInvalidRequest, 0, "max_output_tokens must be a positive integer")
		}
		chat["max_tokens"] = n
	}
	if v := body["reasoning"]; v != nil {
		r, ok := v.(map[string]any)
		if !ok {
			return nil, nil, errf(CodeInvalidRequest, 0, "reasoning must be an object")
		}
		if effort, ok := r["effort"]; ok {
			chat["reasoning_effort"] = effort
		}
	}
	if v := body["text"]; v != nil {
		text, ok := v.(map[string]any)
		if !ok {
			return nil, nil, errf(CodeInvalidRequest, 0, "text must be an object")
		}
		if v := text["format"]; v != nil {
			format, ok := v.(map[string]any)
			if !ok {
				return nil, nil, errf(CodeInvalidRequest, 0, "text.format must be an object")
			}
			switch str(format, "type") {
			case "text", "json_object":
				chat["response_format"] = format
			case "json_schema":
				schema := maps.Clone(format)
				delete(schema, "type")
				chat["response_format"] = map[string]any{"type": "json_schema", "json_schema": schema}
			default:
				return nil, nil, errf(CodeInvalidRequest, 0, "unsupported text.format type")
			}
		}
		if v, ok := text["verbosity"]; ok {
			chat["verbosity"] = v
		}
	}
	if v := body["tools"]; v != nil {
		tools, ok := v.([]any)
		if !ok {
			return nil, nil, errf(CodeInvalidRequest, 0, "tools must be an array")
		}
		flattened := []any{}
		seen := map[string]bool{}
		add := func(tool map[string]any, namespace string) *gwError {
			if str(tool, "type") != "function" {
				return unsupportedResponseTool(str(tool, "type"))
			}
			name, err := a.name(namespace, str(tool, "name"))
			if err != nil {
				return err
			}
			if seen[name] {
				return errf(CodeInvalidRequest, 0, "duplicate function name")
			}
			seen[name] = true
			function := map[string]any{"name": name}
			for _, key := range []string{"description", "parameters", "strict"} {
				if v, ok := tool[key]; ok {
					function[key] = v
				}
			}
			flattened = append(flattened, map[string]any{"type": "function", "function": function})
			return nil
		}
		for _, v := range tools {
			tool, ok := v.(map[string]any)
			if !ok {
				return nil, nil, errf(CodeInvalidRequest, 0, "tool must be an object")
			}
			if str(tool, "type") != "namespace" {
				if err := add(tool, ""); err != nil {
					return nil, nil, err
				}
				continue
			}
			namespace := str(tool, "name")
			if !validToolName(namespace) {
				return nil, nil, errf(CodeInvalidRequest, 0, "invalid tool namespace")
			}
			children, ok := tool["tools"].([]any)
			if !ok {
				return nil, nil, errf(CodeInvalidRequest, 0, "namespace.tools must be an array")
			}
			for _, child := range children {
				tool, ok := child.(map[string]any)
				if !ok {
					return nil, nil, errf(CodeInvalidRequest, 0, "namespace function must be an object")
				}
				if err := add(tool, namespace); err != nil {
					return nil, nil, err
				}
			}
		}
		chat["tools"] = flattened
	}
	if v := body["tool_choice"]; v != nil {
		switch choice := v.(type) {
		case string:
			if choice != "auto" && choice != "none" && choice != "required" {
				return nil, nil, errf(CodeInvalidRequest, 0, "unsupported tool_choice")
			}
			chat["tool_choice"] = choice
		case map[string]any:
			if str(choice, "type") != "function" {
				return nil, nil, unsupportedResponseTool(str(choice, "type"))
			}
			name, err := chatToolName(str(choice, "namespace"), str(choice, "name"))
			if err != nil {
				return nil, nil, err
			}
			if _, ok := a.tools[name]; !ok {
				return nil, nil, errf(CodeInvalidRequest, 0, "tool_choice names an undeclared function")
			}
			chat["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": name}}
		default:
			return nil, nil, errf(CodeInvalidRequest, 0, "invalid tool_choice")
		}
	}
	messages := []any{}
	if v := body["instructions"]; v != nil {
		s, ok := v.(string)
		if !ok {
			return nil, nil, errf(CodeInvalidRequest, 0, "instructions must be text")
		}
		messages = append(messages, map[string]any{"role": "system", "content": s})
	}
	switch input := body["input"].(type) {
	case string:
		messages = append(messages, map[string]any{"role": "user", "content": input})
	case []any:
		for _, v := range input {
			item, ok := v.(map[string]any)
			if !ok {
				return nil, nil, errf(CodeInvalidRequest, 0, "input item must be an object")
			}
			switch str(item, "type") {
			case "reasoning":
				// Opaque, client-owned bookkeeping. Neither summary nor encrypted_content
				// is prompt text, including when the thread came from another provider.
				continue
			case "", "message":
				role := str(item, "role")
				if role != "user" && role != "assistant" && role != "system" && role != "developer" {
					return nil, nil, errf(CodeInvalidRequest, 0, "unsupported message role")
				}
				if role == "developer" {
					role = "system"
				}
				content, err := responseContent(item["content"])
				if err != nil {
					return nil, nil, err
				}
				messages = append(messages, map[string]any{"role": role, "content": content})
			case "function_call":
				name, err := a.historyName(str(item, "namespace"), str(item, "name"))
				if err != nil {
					return nil, nil, err
				}
				args, ok := item["arguments"].(string)
				if !ok || str(item, "call_id") == "" {
					return nil, nil, errf(CodeInvalidRequest, 0, "function_call needs call_id and string arguments")
				}
				call := map[string]any{"id": item["call_id"], "type": "function", "function": map[string]any{"name": name, "arguments": args}}
				// Adjacent calls are one assistant turn, before any parallel tool results.
				if len(messages) > 0 {
					prev := messages[len(messages)-1].(map[string]any)
					if calls, ok := prev["tool_calls"].([]any); ok {
						prev["tool_calls"] = append(calls, call)
						continue
					}
				}
				messages = append(messages, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{call}})
			case "function_call_output":
				if str(item, "call_id") == "" {
					return nil, nil, errf(CodeInvalidRequest, 0, "function_call_output needs call_id")
				}
				content, err := responseContent(item["output"])
				if err != nil {
					return nil, nil, err
				}
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": item["call_id"], "content": content})
			default:
				return nil, nil, errf(CodeInvalidRequest, 0, "unsupported input item type %q", str(item, "type"))
			}
		}
	default:
		return nil, nil, errf(CodeInvalidRequest, 0, "input must be text or an array of items")
	}
	chat["messages"] = messages
	return chat, a, nil
}

func unsupportedResponseTool(kind string) *gwError {
	if strings.HasPrefix(kind, "web_search") {
		return errf(CodeInvalidRequest, 0, "hosted web search is not supported; in Codex 0.154.0 set web_search = \"disabled\"")
	}
	return errf(CodeInvalidRequest, 0, "unsupported tool type %q; only client function tools are supported", kind)
}

func responseContent(v any) (any, *gwError) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	parts, ok := v.([]any)
	if !ok {
		return nil, errf(CodeInvalidRequest, 0, "message or tool output content must be text or an array")
	}
	out := []any{}
	for _, v := range parts {
		part, ok := v.(map[string]any)
		if !ok {
			return nil, errf(CodeInvalidRequest, 0, "content part must be an object")
		}
		switch str(part, "type") {
		case "input_text", "output_text":
			text, ok := part["text"].(string)
			if !ok {
				return nil, errf(CodeInvalidRequest, 0, "text part needs text")
			}
			out = append(out, map[string]any{"type": "text", "text": text})
		case "input_image":
			if str(part, "image_url") == "" {
				return nil, errf(CodeInvalidRequest, 0, "input_image needs image_url; file ids are not supported")
			}
			image := map[string]any{"url": part["image_url"]}
			if d, ok := part["detail"]; ok {
				image["detail"] = d
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": image})
		default:
			return nil, errf(CodeInvalidRequest, 0, "unsupported content part %q", str(part, "type"))
		}
	}
	return out, nil
}

func responseID(prefix string) string { return fmt.Sprintf("%s_%s", prefix, newResponseID()) }

// Codex 0.154.0 serializes FunctionCall namespace/name separately. A legacy bare
// name can resolve only when exactly one declaration matches; history adds none.
func (a *responsesAdapter) historyName(namespace, name string) (string, *gwError) {
	found := ""
	for alias, tool := range a.tools {
		if tool.Name == name && (namespace == "" || tool.Namespace == namespace) {
			if found != "" {
				return "", errf(CodeInvalidRequest, 0, "function %q is ambiguous across namespaces; include its namespace", name)
			}
			found = alias
		}
	}
	if found == "" {
		return "", errf(CodeInvalidRequest, 0, "function %q in namespace %q has no matching declared function; check namespace history", name, namespace)
	}
	return found, nil
}
