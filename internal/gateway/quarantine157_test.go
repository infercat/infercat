package gateway

import (
	runstate "github.com/infercat/infercat/internal/run"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test157RuntimeQuarantineIsRetryable503(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, runError(runstate.ErrQuarantined))
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), `"code":"upstream_down"`) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
}
