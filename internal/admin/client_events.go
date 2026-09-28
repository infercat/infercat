package admin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Events reads bounded NDJSON records until the host closes the stream or ctx ends.
// It shares the local authentication and no-replay policy, but has no overall stream timeout.
func (c *Client) Events(ctx context.Context, consume func(json.RawMessage), ready func()) error {
	hc := *c.http
	hc.Timeout = 0
	transport := http.DefaultTransport.(*http.Transport)
	if c.http.Transport != nil {
		transport = c.http.Transport.(*http.Transport)
	}
	owned := transport.Clone()
	owned.ResponseHeaderTimeout = 5 * time.Second
	hc.Transport = owned
	defer owned.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := hc.Do(req)
	if err != nil {
		return callError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &APIError{Status: resp.StatusCode, Message: "admin event stream unavailable"}
	}
	if ready != nil {
		ready()
	}
	scan := bufio.NewScanner(resp.Body)
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		var fields map[string]json.RawMessage
		if json.Unmarshal(scan.Bytes(), &fields) != nil || fields == nil {
			return ErrResponse
		}
		var raw bytes.Buffer
		if _, p := fields["prompt"]; p || fields["completion"] != nil {
			delete(fields, "prompt")
			delete(fields, "completion")
			body, err := json.Marshal(fields)
			if err != nil {
				return ErrResponse
			}
			raw.Write(body)
		} else if json.Compact(&raw, scan.Bytes()) != nil {
			return ErrResponse
		}
		consume(json.RawMessage(raw.Bytes()))
	}
	if err := scan.Err(); err != nil {
		return callError(err)
	}
	return nil
}
