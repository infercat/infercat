package gateway

import (
	"github.com/infercat/infercat/internal/usage"
	"slices"
	"sort"
	"sync/atomic"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
)

// Destination owns capacity; transports retain their own bearer, address and deadlines.
// Text and Audio are alternatives. Audio never needs a pretend tokenizer or Refresh method.
type Destination struct {
	imageAbandons   atomic.Int64
	imageRetryAt    atomic.Int64
	imageBackoff    atomic.Int64
	imageProbeAfter atomic.Int64 // After an abandoned request, require a fresh successful probe.
	ID              string
	Kind            string
	Origin          string
	Up              interface{ Info() upstream.Info }
	Text            upstream.Engine
	Audio           upstream.AudioEngine
	Images          upstream.ImageEngine
	Queue           slotQueue
	model           string
}

type Offers struct {
	Models []string
	Vision map[string]*bool
	Audio  []string
}

// Offers reads current engine state rather than caching a second capability inventory.
func (d *Destination) Offers() Offers {
	i := d.Up.Info()
	o := Offers{Models: append([]string{}, i.Models...), Vision: i.Vision}
	if d.Images != nil && d.model != "" && !slices.Contains(o.Models, d.model) {
		o.Models = append(o.Models, d.model)
	}
	if d.Audio != nil {
		o.Audio = []string{d.ID}
		if d.model != "" && !slices.Contains(o.Models, d.model) {
			o.Models = append(o.Models, d.model)
		}
	}
	return o
}

// DestinationStatus is admin-only: friends receive capability offers, not engine structure.
type DestinationStatus struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Models        []string `json:"models"`
	Slots         int      `json:"slots"`
	InFlight      int      `json:"in_flight"`
	Waiting       int      `json:"waiting"`
	ImageAbandons int64    `json:"image_abandons,omitempty"`
	ImageRetryAt  int64    `json:"image_retry_at,omitempty"`
}

type Router struct {
	text         *Destination
	routes       map[string]*Destination
	destinations []*Destination
	pinned       []string
}

func newRouter(text upstream.Engine, cfg Config) *Router {
	d := &Destination{ID: "text", Kind: "engine", Origin: "local", Up: text, Text: text}
	d.Queue.cap = func() int { return d.Up.Info().Slots }
	r := &Router{text: d, destinations: []*Destination{d}, pinned: cfg.ModelsPinned, routes: map[string]*Destination{}}
	for _, route := range []endpoint{responsesEndpoint, chatEndpoint, embeddingsEndpoint, modelsEndpoint} {
		r.routes[string(route)] = d
	}
	for _, audio := range []struct {
		id    string
		route endpoint
		up    upstream.AudioEngine
		model string
	}{
		{"transcribe", transcribeEndpoint, cfg.Transcribe, cfg.TranscribeModel},
		{"speech", speechEndpoint, cfg.Speech, cfg.SpeechModel},
	} {
		if audio.up == nil {
			continue
		}
		d := &Destination{ID: audio.id, Kind: "engine", Origin: "local", Up: audio.up, Audio: audio.up, model: audio.model}
		d.Queue.cap = func() int { return d.Up.Info().Slots }
		r.routes[string(audio.route)] = d
		r.destinations = append(r.destinations, d)
	}
	if cfg.Images != nil {
		d := &Destination{ID: "images", Kind: "engine", Origin: "local", Up: cfg.Images, Images: cfg.Images, model: cfg.ImageModel}
		d.Queue.cap = func() int { return 1 }
		r.routes[string(imagesEndpoint)] = d
		r.destinations = append(r.destinations, d)
	}
	return r
}

// Route selection is cheap and precedes health/admission. Model checks wait for the body.
func (r *Router) route(route string) *Destination { return r.routes[route] }

func (r *Router) Resolve(key *keys.Key, route, model string) (*Destination, *gwError) {
	if len(model) > usage.MaxModelBytes {
		return nil, errf(CodeInvalidRequest, 0, "model identifier exceeds %d bytes", usage.MaxModelBytes)
	}
	d := r.route(route)
	if d == nil {
		return nil, errf(CodeNotFound, 0, "no route for POST %s", route)
	}
	pin := r.pinned
	if d.Text == nil {
		pin = nil
	}
	if model != "" && !allowsModel(key, pin, model) {
		if d.Audio != nil {
			return nil, errf(CodeModelNotAllowed, 0, "model is not shared with this invite")
		}
		return nil, errf(CodeModelNotAllowed, 0, "model %q is not allowed for this key", model)
	}
	return d, nil
}

func (r *Router) audioModel(route endpoint) *string {
	if d := r.route(string(route)); d != nil {
		return audioModel(d.Audio, d.model)
	}
	return nil
}

func (r *Router) snapshots() []DestinationStatus {
	out := make([]DestinationStatus, 0, len(r.destinations))
	for _, d := range r.destinations {
		models := []string{}
		for _, model := range d.Offers().Models {
			models = append(models, model)
		}
		inFlight, waiting := d.Queue.counts()
		out = append(out, DestinationStatus{d.ID, d.Kind, models, max(1, d.Up.Info().Slots), inFlight, waiting, d.imageAbandons.Load(), d.imageRetryAt.Load()})
	}
	return out
}

func (g *Gateway) Destinations() []DestinationStatus { return g.router.snapshots() }

// Resolution cannot consume a body or a slot: the request already owns its key admission.
func (q *request) resolveDestination(model string) *gwError {
	d, err := q.g.router.Resolve(q.key, string(q.kind), model)
	if err != nil {
		return err
	}
	q.destination, q.ev.Destination = d, d.ID
	return nil
}

// EngineRoutes is the configured engine-facing surface, including model discovery.
func (g *Gateway) EngineRoutes() []string {
	routes := make([]string, 0, len(g.router.routes))
	for path := range g.router.routes {
		routes = append(routes, path)
	}
	sort.Strings(routes)
	return routes
}
