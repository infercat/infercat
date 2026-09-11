package gateway

import (
	"context"
	"net/http"
	"time"
)

type ManagedMember struct {
	Acquire func(context.Context) (func(), error)
	Offered func() bool
	Model   string
}

func (q *request) prepareManaged() *gwError {
	if q.destination == nil {
		return nil
	}
	m, ok := q.g.cfg.Managed[q.destination.ID]
	if !ok {
		return nil
	}
	if q.adm == nil {
		var e *gwError
		if q.kind == imagesEndpoint {
			e = q.admitImageKey()
		} else {
			e = q.admitKey()
		}
		if e != nil {
			return e
		}
	}
	// Startup owns no body bytes; give the admitted caller a fresh body window afterward.
	rc := http.NewResponseController(q.w)
	_ = rc.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(q.r.Context(), 2*time.Minute)
	release, e := m.Acquire(ctx)
	cancel()
	_ = rc.SetReadDeadline(time.Now().Add(q.g.readTimeout))
	if e != nil {
		return errf(CodeUpstreamDown, 3, "member unavailable: %v", e)
	}
	q.releaseManaged = release
	return nil
}
