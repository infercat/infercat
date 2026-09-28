package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	ErrTimeout  = errors.New("the running host did not answer in time")
	ErrResponse = errors.New("the host returned an invalid response; an action may have applied — check its state before trying again")
	// A lost connection remains an ambiguous reply for mutations; watch can distinguish it from malformed JSON.
	ErrDisconnected = fmt.Errorf("%w: connection ended before a complete reply", ErrResponse)
)

// APIError retains the route's failure sentence. Routes have no common error-code body.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// Client addresses only this data directory's local admin endpoint. The token stays here.
// Requests are never redirected or replayed, including after an ambiguous mutation result.
type Client struct {
	http        *http.Client
	base, token string
}

func NewClient(dir string) (*Client, error) {
	hc, base, token, err := dial(dir)
	if err != nil {
		// A present endpoint with unreadable credentials is not evidence that the host
		// is stopped. In particular, callers must not choose an offline mutation here.
		if errors.Is(err, ErrNoDaemon) {
			for _, name := range []string{SockName, PortName} {
				if _, statErr := os.Stat(filepath.Join(dir, name)); statErr == nil {
					raw, readErr := os.ReadFile(filepath.Join(dir, TokenName))
					if readErr != nil || strings.TrimSpace(string(raw)) == "" {
						return nil, errors.New("admin credentials unavailable; check the running host")
					}
				}
			}
		}
		return nil, err
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: hc, base: base, token: token}, nil
}

const maxResponse = 16 << 20

// Call returns the successful JSON body without decoding/re-encoding it. The bound applies
// to errors too; neither an error nor a partial body becomes a successful machine payload.
func (c *Client) Call(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n#") {
		return nil, errors.New("invalid admin route")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, callError(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, callError(err)
	}
	if len(raw) > maxResponse {
		return nil, ErrResponse
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(raw))
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &failure) == nil && failure.Error != "" {
			message = failure.Error
		}
		if message == "" {
			message = "admin API: " + resp.Status
		}
		return nil, &APIError{Status: resp.StatusCode, Message: message}
	}
	if !json.Valid(raw) {
		return nil, ErrResponse
	}
	return json.RawMessage(raw), nil
}

func callError(err error) error {
	var timeout net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout():
		return ErrTimeout
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED):
		return ErrNoDaemon
	default:
		return ErrDisconnected
	}
}
