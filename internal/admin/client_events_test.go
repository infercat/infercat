package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientEventsRemoveContentAndKeepFutureFields(t *testing.T) {
	t.Parallel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("wrong event request")
		}
		io.WriteString(w, "{\"prompt\":\"PRIVATE\",\"completion\":\"PRIVATE\",\"future\":9007199254740993,\"status\":200}\n{ \"future\": null, \"status\": 0 }\n")
	}))
	defer s.Close()
	c := &Client{http: s.Client(), base: s.URL, token: "fixture"}
	ready := false
	var rows []json.RawMessage
	err := c.Events(context.Background(), func(row json.RawMessage) {
		if !ready {
			t.Error("event preceded readiness")
		}
		rows = append(rows, row)
	}, func() { ready = true })
	if err != nil || len(rows) != 2 {
		t.Fatal(err, len(rows))
	}
	if strings.Contains(string(rows[0]), "PRIVATE") || strings.Contains(string(rows[0]), "prompt") || strings.Contains(string(rows[0]), "completion") || !strings.Contains(string(rows[0]), "9007199254740993") {
		t.Fatal(string(rows[0]))
	}
	if string(rows[1]) != `{"future":null,"status":0}` {
		t.Fatal(string(rows[1]))
	}
}

func TestClientEventsBoundAndMalformedRecord(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"{\n", strings.Repeat(" ", 1<<20) + "{}\n"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		c := &Client{http: s.Client(), base: s.URL}
		err := c.Events(context.Background(), func(json.RawMessage) { t.Error("invalid event emitted") }, nil)
		s.Close()
		if !errors.Is(err, ErrResponse) {
			t.Fatal(err)
		}
	}
}
