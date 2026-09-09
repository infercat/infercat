package upstream

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"
)

// modelsResponse is the OpenAI /v1/models shape plus the fields the engines add: vLLM carries
// max_model_len, LM Studio carries publisher/quantization and loaded_context_length.
type modelsResponse struct {
	Data []struct {
		ContextLength int `json:"context_length"`
		Status        struct {
			Value string `json:"value"`
		} `json:"status"`
		ID                  string `json:"id"`
		OwnedBy             string `json:"owned_by"`
		MaxModelLen         int    `json:"max_model_len"`
		Publisher           string `json:"publisher"`
		Quantization        string `json:"quantization"`
		LoadedContextLength int    `json:"loaded_context_length"`
	} `json:"data"`
}

type propsResponse struct {
	Modalities struct {
		Vision *bool `json:"vision"`
	} `json:"modalities"`
	TotalSlots                int    `json:"total_slots"`
	ModelAlias                string `json:"model_alias"`
	ModelPath                 string `json:"model_path"`
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
}

type tagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// Check the swap signature before probing /props: swap routes it to a model, and discovery
// must never load one. Other engines keep their signature precedence, independent of port.
func sniff(ctx context.Context, c *client) (Kind, error) {
	var models modelsResponse
	modelsErr := c.getJSON(ctx, "/v1/models", &models)
	if modelsErr == nil {
		for _, m := range models.Data {
			if m.OwnedBy == "llama-swap" {
				return LlamaSwap, nil
			}
		}
	}
	var props propsResponse
	if err := c.getJSON(ctx, "/props", &props); err == nil && (props.TotalSlots > 0 || props.ModelPath != "" || props.DefaultGenerationSettings.NCtx > 0) {
		return LlamaCPP, nil
	}
	var ver struct {
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, "/version", &ver); err == nil && ver.Version != "" {
		return VLLM, nil
	}
	var tags tagsResponse
	if err := c.getJSON(ctx, "/api/tags", &tags); err == nil && tags.Models != nil {
		return Ollama, nil
	}
	if modelsErr != nil {
		return Unknown, modelsErr
	}
	for _, m := range models.Data {
		switch {
		case strings.EqualFold(m.OwnedBy, "vllm") || m.MaxModelLen > 0:
			return VLLM, nil
		case m.Publisher != "" || m.Quantization != "" || m.LoadedContextLength > 0 ||
			strings.EqualFold(m.OwnedBy, "organization_owner"):
			return LMStudio, nil
		case strings.EqualFold(m.OwnedBy, "llamacpp"):
			return LlamaCPP, nil
		}
	}
	return Generic, nil
}

// Refresh is the one probe, and every transition goes through it (DESIGN §3.2): Unknown and an
// engine answers a signature → identified, OK, fields filled; Unknown and nothing answers → still
// Unknown, not OK (Err updated, Since kept); identified and the refresh succeeds → fields replaced;
// identified and it fails → fields kept, not OK — /me keeps telling the truth about what it knew.
// An explicit --slots wins; otherwise use engine capacities, vLLM's documented 2, or 1.
func (c *client) Refresh(ctx context.Context) error {
	c.mu.RLock()
	kind, override := c.info.Kind, c.setSlots
	c.mu.RUnlock()
	var err error
	if kind == Unknown {
		kind, err = sniff(ctx, c)
	}
	next := Info{URL: c.base.String(), Kind: kind, Slots: 1, Vision: make(map[string]*bool)}
	if err == nil {
		switch kind {
		case LlamaCPP:
			err = c.refreshLlamaCPP(ctx, &next)
		case Ollama:
			err = c.refreshOllama(ctx, &next)
		case LlamaSwap:
			err = c.refreshLlamaSwap(ctx, &next)
		default: // vLLM, LM Studio, Generic all answer /v1/models
			err = c.refreshOpenAI(ctx, &next)
		}
	}
	if override == 0 && kind == VLLM {
		override = 2 // docs/ARCHITECTURE.md: vLLM does not report slots; --slots overrides, default 2
	}
	if override > 0 {
		next.Slots = override
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.info.ProbedAt = now
	if err != nil {
		if c.info.Health.OK {
			c.info.Health.Since = now
		}
		c.info.Health.OK, c.info.Health.Err = false, err.Error()
		return err
	}
	next.Health = Health{OK: true, Since: c.info.Health.Since}
	if !c.info.Health.OK {
		next.Health.Since = now
	}
	next.ProbedAt = now
	c.info = next
	return nil
}

func (c *client) refreshLlamaCPP(ctx context.Context, in *Info) error {
	var props propsResponse
	if err := c.getJSON(ctx, "/props", &props); err != nil {
		return err
	}
	in.ModelContext = props.DefaultGenerationSettings.NCtx
	if props.TotalSlots > 0 {
		in.Slots = props.TotalSlots
	}
	var models modelsResponse
	if err := c.getJSON(ctx, "/v1/models", &models); err == nil {
		for _, m := range models.Data {
			in.Models = append(in.Models, m.ID)
		}
	}
	if len(in.Models) == 0 && props.ModelAlias != "" {
		in.Models = []string{props.ModelAlias}
	}
	for _, id := range in.Models {
		in.Vision[id] = props.Modalities.Vision
	}
	return nil
}

// llama-swap v255 publishes these fields in internal/server/api.go (commit 7761aa1).
func (c *client) refreshLlamaSwap(ctx context.Context, in *Info) error {
	var models modelsResponse
	if err := c.getJSON(ctx, "/v1/models", &models); err != nil {
		return err
	}
	previous := c.Info()
	in.ModelDetails = make(map[string]ModelInfo, len(models.Data))
	for _, m := range models.Data {
		detail := ModelInfo{Context: max(0, m.ContextLength), State: m.Status.Value, Slots: 1}
		if old := previous.ModelDetails[m.ID]; old.Slots > 0 {
			detail.Slots = old.Slots
		}
		in.Vision[m.ID] = previous.Vision[m.ID]
		if m.Status.Value == "loaded" {
			var props propsResponse
			if err := c.getJSON(ctx, "/props?model="+url.QueryEscape(m.ID), &props); err == nil {
				if props.TotalSlots > 0 {
					detail.Slots = props.TotalSlots
				}
				in.Vision[m.ID] = props.Modalities.Vision
			}
		}
		if detail.Context > 0 && (in.ModelContext == 0 || detail.Context < in.ModelContext) {
			in.ModelContext = detail.Context
		}
		if len(in.Models) == 0 || detail.Slots < in.Slots {
			in.Slots = detail.Slots
		}
		in.Models = append(in.Models, m.ID)
		in.ModelDetails[m.ID] = detail
	}
	return nil
}

func (c *client) refreshOllama(ctx context.Context, in *Info) error {
	var tags tagsResponse
	if err := c.getJSON(ctx, "/api/tags", &tags); err != nil {
		return err
	}
	for _, m := range tags.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		in.Models = append(in.Models, name)
		in.Vision[name] = nil
		var show struct {
			Capabilities []string `json:"capabilities"`
		}
		if err := c.doJSON(ctx, "POST", "/api/show", map[string]string{"model": name}, &show); err == nil && show.Capabilities != nil {
			vision := slices.Contains(show.Capabilities, "vision")
			in.Vision[name] = &vision
		}
	}
	// Ollama does not publish a per-model context on /api/tags; 0 means "the upstream's".
	return nil
}

func (c *client) refreshOpenAI(ctx context.Context, in *Info) error {
	var models modelsResponse
	if err := c.getJSON(ctx, "/v1/models", &models); err != nil {
		return err
	}
	for _, m := range models.Data {
		in.Models = append(in.Models, m.ID)
		in.Vision[m.ID] = nil
		if in.Kind == VLLM {
			vision := true // Optimistic: /v1/models does not advertise multimodal support.
			in.Vision[m.ID] = &vision
		}
		if n := m.MaxModelLen; n > 0 && (in.ModelContext == 0 || n < in.ModelContext) {
			in.ModelContext = n
		}
		if n := m.LoadedContextLength; n > 0 && (in.ModelContext == 0 || n < in.ModelContext) {
			in.ModelContext = n
		}
	}
	if in.Kind == LMStudio {
		var native struct {
			Data []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"data"`
		}
		if err := c.getJSON(ctx, "/api/v0/models", &native); err == nil {
			for _, m := range native.Data {
				if _, served := in.Vision[m.ID]; served && m.Type != "" {
					vision := m.Type == "vlm"
					in.Vision[m.ID] = &vision
				}
			}
		}
	}
	return nil
}

// CountTokens is exact where the engine offers /tokenize (llama.cpp and vLLM) and an estimate of
// ceil(len/4) otherwise. A chat (messages != nil) is counted the way the chat endpoint counts it:
// under the chat template — roles, turn markers, BOS, the generation prompt — which a bare count
// misses (036: +14 on llama.cpp, +17 on vLLM for two short messages) and which at the ceiling is
// the difference between the gateway's own 422 and the engine's 400. llama.cpp renders the
// template (/apply-template) and tokenizes the result with its special tokens and BOS
// (add_special; equals usage.prompt_tokens, checked on b9553); vLLM's /tokenize takes the messages
// itself (equals usage.prompt_tokens, checked on 0.25.0); an estimating engine adds
// TemplateAllowance. A failed exact count silently degrades to the estimate: the gateway's
// context pre-check must not turn a tokenizer hiccup into a 5xx.
func (c *client) CountTokens(ctx context.Context, text string, messages []byte) (int, bool, error) {
	info := c.Info()
	switch info.Kind {
	case LlamaCPP:
		content, special := text, false
		if messages != nil {
			var tpl struct {
				Prompt string `json:"prompt"`
			}
			if err := c.doJSON(ctx, "POST", "/apply-template", map[string]any{"messages": json.RawMessage(messages)}, &tpl); err != nil || tpl.Prompt == "" {
				break
			}
			content, special = tpl.Prompt, true
		}
		var out struct {
			Tokens []int `json:"tokens"`
		}
		if err := c.doJSON(ctx, "POST", "/tokenize", map[string]any{"content": content, "add_special": special}, &out); err == nil {
			return len(out.Tokens), true, nil
		}
	case VLLM:
		if len(info.Models) > 0 {
			req := map[string]any{"model": info.Models[0], "prompt": text}
			if messages != nil {
				req = map[string]any{"model": info.Models[0], "messages": json.RawMessage(messages), "add_generation_prompt": true}
			}
			var out struct {
				Count  int   `json:"count"`
				Tokens []int `json:"tokens"`
			}
			if err := c.doJSON(ctx, "POST", "/tokenize", req, &out); err == nil {
				return max(out.Count, len(out.Tokens)), true, nil
			}
		}
	}
	return EstimateTokens(text) + TemplateAllowance(messages), false, nil
}

// EstimateTokens is the ceil(chars/4) fallback docs/ARCHITECTURE.md specifies.
func EstimateTokens(text string) int { return (len(text) + 3) / 4 }

// TemplateAllowance is what an estimate adds for a chat template: 4 tokens a message and 16 for
// the chat (roles, turn markers, BOS, the generation prompt; 13–17 measured on Gemma 4). 0 when
// messages is nil or not an array.
func TemplateAllowance(messages []byte) int {
	var ms []json.RawMessage
	if messages == nil || json.Unmarshal(messages, &ms) != nil {
		return 0
	}
	return 4*len(ms) + 16
}
