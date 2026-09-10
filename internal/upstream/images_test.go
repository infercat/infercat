package upstream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestImageDeadlineCoversHeadersAndBody(t *testing.T) {
	for _, flush := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[flush], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					io.WriteString(w, `{"data":[{"id":"images"}]}`)
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if flush {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			engine, e := OpenImages(context.Background(), server.URL, "")
			if e != nil {
				t.Fatal(e)
			}
			c := engine.(*imageClient).c
			if c.hc.Timeout != 15*time.Minute || c.hc.Transport.(*http.Transport).ResponseHeaderTimeout != 15*time.Minute {
				t.Fatal("image inherited text first-byte deadline")
			}
			c.hc.Timeout = 30 * time.Millisecond
			response, e := engine.ImageDo(context.Background(), []byte(`{}`))
			if e == nil {
				_, e = io.ReadAll(response.Body)
				response.Body.Close()
			}
			if e == nil {
				t.Fatal("generation ceiling did not cover response")
			}
		})
	}
}
