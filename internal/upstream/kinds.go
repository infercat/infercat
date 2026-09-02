package upstream

import (
	"context"
	"strings"
	"time"
)

// modelsResponse is the OpenAI /v1/models shape plus the fields the engines add: vLLM carries
// max_model_len, LM Studio carries publisher/quantization and loaded_context_length.
type modelsResponse struct {
	Data []struct {
		ID                  string `json:"id"`
		OwnedBy             string `json:"owned_by"`
		MaxModelLen         int    `json:"max_model_len"`
		Publisher           string `json:"publisher"`
		Quantization        string `json:"quantization"`
		LoadedContextLength int    `json:"loaded_context_length"`
	} `json:"data"`
}

type propsResponse struct {
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

// sniff decides which engine answers at c.base. Order matters: llama.cpp is the only one with
// /props, vLLM is the only one of the four with /version, Ollama is the only one with /api/tags,
// and anything else that speaks /v1/models is LM Studio (by shape) or Generic. Nothing answering
// is Unknown with the last probe's error.
func sniff(ctx context.Context, c *client) (Kind, error) {
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
	var models modelsResponse
	if err := c.getJSON(ctx, "/v1/models", &models); err != nil {
		return Unknown, err
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
// Slots are the engine's own where it reports them (llama.cpp), else the host's --slots, else
// vLLM's documented 2, else 1.
func (c *client) Refresh(ctx context.Context) error {
	c.mu.RLock()
	kind, override := c.info.Kind, c.setSlots
	c.mu.RUnlock()
	var err error
	if kind == Unknown {
		kind, err = sniff(ctx, c)
	}
	next := Info{URL: c.base.String(), Kind: kind, Slots: 1}
	if err == nil {
		switch kind {
		case LlamaCPP:
			err = c.refreshLlamaCPP(ctx, &next)
		case Ollama:
			err = c.refreshOllama(ctx, &next)
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
		if n := m.MaxModelLen; n > 0 && (in.ModelContext == 0 || n < in.ModelContext) {
			in.ModelContext = n
		}
		if n := m.LoadedContextLength; n > 0 && (in.ModelContext == 0 || n < in.ModelContext) {
			in.ModelContext = n
		}
	}
	return nil
}

// CountTokens is exact where the engine offers /tokenize (llama.cpp and vLLM) and an estimate of
// ceil(len/4) otherwise. A failed exact count silently degrades to the estimate: the gateway's
// context pre-check must not turn a tokenizer hiccup into a 5xx.
func (c *client) CountTokens(ctx context.Context, text string) (int, bool, error) {
	switch c.Info().Kind {
	case LlamaCPP:
		var out struct {
			Tokens []int `json:"tokens"`
		}
		if err := c.doJSON(ctx, "POST", "/tokenize", map[string]any{"content": text}, &out); err == nil {
			return len(out.Tokens), true, nil
		}
	case VLLM:
		model := ""
		if ms := c.Info().Models; len(ms) > 0 {
			model = ms[0]
		}
		if model != "" {
			var out struct {
				Count  int   `json:"count"`
				Tokens []int `json:"tokens"`
			}
			if err := c.doJSON(ctx, "POST", "/tokenize", map[string]any{"model": model, "prompt": text}, &out); err == nil {
				if out.Count > 0 {
					return out.Count, true, nil
				}
				return len(out.Tokens), true, nil
			}
		}
	}
	return EstimateTokens(text), false, nil
}

// EstimateTokens is the ceil(chars/4) fallback docs/ARCHITECTURE.md specifies.
func EstimateTokens(text string) int { return (len(text) + 3) / 4 }
