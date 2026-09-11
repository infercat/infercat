package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/upstream"
)

func TestHostChatInnerRefusalsKeepEnvelopeAndRunReason(t *testing.T) {
	for _, code := range []Code{CodeRateLimited, CodeConcurrencyLimited, CodeBudgetExhausted, CodeContextTooLong, CodeKeyPaused, CodeKeyRevoked, CodeUpstreamDown} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", code, stream), func(t *testing.T) {
				h := hostChatHarness(t, []string{imageCall(`{"prompt":"fox","count":1}`), `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`}, nil)
				if code == CodeRateLimited {
					now := time.Now()
					h.gw.lim.now = func() time.Time { return now }
					h.setKey(func(k *keys.Key) { k.Limits.RPM = 1 })
				}
				original := h.gw.runs.Execute
				captured := make(chan *gwError, 1)
				var calls atomic.Int32
				h.gw.runs.Execute = func(ctx context.Context, key string, step runstate.Step, acquired func() error) (runstate.StepResult, error) {
					second := step.Purpose == "chat" && calls.Add(1) == 2
					if second {
						k := *h.key
						switch code {
						case CodeConcurrencyLimited:
							k.Limits.MaxConcurrent = 2
							for range 2 {
								a, e := h.gw.lim.admit(key, k.Limits)
								if e != nil {
									t.Error(e)
									break
								}
								defer h.gw.lim.settle(a, false, 0)
							}
						case CodeBudgetExhausted:
							k.Limits.DailyTokens = 1
						case CodeContextTooLong:
							k.Limits.MaxContext = 1
						case CodeKeyPaused:
							k.Status = keys.Paused
						case CodeKeyRevoked:
							k.Status = keys.Revoked
						case CodeUpstreamDown:
							h.up.setInfo(func(i *upstream.Info) { i.Health.OK = false })
						}
						h.store.set(testSecret, &k)
					}
					result, err := original(ctx, key, step, acquired)
					if second {
						var inner *gwError
						if !errors.As(err, &inner) {
							t.Errorf("inner type lost: %v", err)
						}
						captured <- inner
					}
					return result, err
				}
				response := h.post(string(chatEndpoint), hostChatBody(stream))
				var inner *gwError
				select {
				case inner = <-captured:
				default:
					t.Fatal("second model call absent", response.status, string(response.body))
				}
				if inner == nil || inner.Code != code {
					t.Fatal(inner, code)
				}
				var actual []byte
				if stream {
					if response.status != 200 || !bytes.Contains(response.body, []byte("data: [DONE]")) {
						t.Fatal(response.status, string(response.body))
					}
					for _, line := range strings.Split(string(response.body), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						raw := []byte(strings.TrimPrefix(line, "data: "))
						var e errorBody
						if json.Unmarshal(raw, &e) == nil && e.Error.Code != "" {
							actual = raw
						}
					}
				} else {
					if response.status != inner.Status() {
						t.Fatal(response.status, inner)
					}
					actual = response.body
					if inner.RetryAfter > 0 && response.header.Get("Retry-After") != fmt.Sprint(inner.RetryAfter) {
						t.Fatal(response.header, inner)
					}
				}
				if !bytes.Equal(actual, errorJSON(inner, stream)) {
					t.Fatalf("wanted %s, got %s", errorJSON(inner, stream), actual)
				}
				if code == CodeRateLimited && inner.RetryAfter != 60 {
					t.Fatal(inner)
				}
				if code == CodeConcurrencyLimited && (inner.Limit != 2 || inner.InFlight != 2) {
					t.Fatal(inner)
				}
				r, e := h.gw.runs.Store.Get(h.key.ID, chatRunID(t, h))
				want := fmt.Sprintf("%s retry_after=%d", code, inner.RetryAfter)
				if e != nil || r.State != runstate.Failed || r.Reason != want || len(r.Attempts) != 2 {
					t.Fatal(r, e, want)
				}
				for _, m := range r.Attempts[1].Usage.Meters {
					if m.Charged != 0 {
						t.Fatal("rejection charged", m)
					}
				}
				t.Logf("%s; retry=%d; limit=%d in_flight=%d; reason=%s", code, inner.RetryAfter, inner.Limit, inner.InFlight, r.Reason)
			})
		}
	}
}
