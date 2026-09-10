package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"time"
)

type Result struct {
	Member        Member
	State, Detail string
	Paths         map[string]string
	Missing       []Asset
	Err           error
}

func (m Member) URL() string { return fmt.Sprintf("http://127.0.0.1:%d", m.Port) }
func Check(ctx context.Context, m Member, paths map[string]string, roots []string) Result {
	r := Result{Member: m, State: "missing", Paths: map[string]string{}}
	if m.Pending != "" {
		r.Detail = m.Pending
		for _, c := range m.Candidates {
			r.Missing = append(r.Missing, c.Assets...)
		}
		return r
	}
	for i, a := range m.Model.Assets {
		path := paths[a.ID]
		if i == 0 && paths[m.ID] != "" {
			path = paths[m.ID]
		}
		found, e := Find(ctx, a, path, roots)
		if e != nil {
			r.Err = e
			return r
		}
		if found == "" {
			r.Missing = append(r.Missing, a)
		} else {
			r.Paths[a.ID] = found
		}
	}
	if len(r.Missing) == 0 {
		r.State = "cached"
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, body []byte) ([]byte, int, error) {
		method := "GET"
		if body != nil {
			method = "POST"
		}
		req, _ := http.NewRequestWithContext(ctx, method, m.URL()+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, e := client.Do(req)
		if e != nil {
			return nil, 0, e
		}
		defer res.Body.Close()
		b, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
		if len(b) > 1<<20 {
			return nil, res.StatusCode, fmt.Errorf("engine response exceeds 1 MiB")
		}
		return b, res.StatusCode, e
	}
	b, status, e := get("/v1/models", nil)
	if status == 0 {
		r.Detail = "not answering"
		if ctx.Err() != nil {
			r.Err = ctx.Err()
		}
		return r
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e != nil || status != 200 || json.Unmarshal(b, &models) != nil {
		r.Err = fmt.Errorf("%s health check failed (HTTP %d)", m.ID, status)
		return r
	}
	ids := []string{}
	for _, v := range models.Data {
		ids = append(ids, v.ID)
	}
	if !slices.Contains(ids, m.Model.Name) {
		r.Err = fmt.Errorf("%s port answers but does not advertise %s", m.ID, m.Model.Name)
		return r
	}
	r.State = "running"
	r.Detail = "active weights not attested by health"
	if m.Class == "text" {
		body, _ := json.Marshal(map[string]any{"model": m.Model.Name, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 32, "stream": false})
		b, status, e = get("/v1/chat/completions", body)
		var answer struct {
			Choices []struct {
				Message struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if e != nil || status != 200 || json.Unmarshal(b, &answer) != nil || len(answer.Choices) == 0 || answer.Choices[0].Message.Content+answer.Choices[0].Message.Reasoning == "" {
			r.Err = fmt.Errorf("anchor %s did not answer the dry-check prompt", m.ID)
		}
	}
	return r
}
