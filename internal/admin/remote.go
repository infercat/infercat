package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// RemoteResult is the same one-time result returned by the local console actions.
type RemoteResult struct {
	KeyID  string `json:"key_id,omitempty"`
	Name   string `json:"name,omitempty"`
	Invite string `json:"invite,omitempty"`
	Link   string `json:"link,omitempty"`
	OK     bool   `json:"ok,omitempty"`
}

// RemoteAction calls the running host exactly once. An ambiguous response is never replayed.
func RemoteAction(ctx context.Context, dir, action string) (RemoteResult, error) {
	var result RemoteResult
	if action != "enable" && action != "rotate" && action != "off" {
		return result, errors.New("invalid remote action")
	}
	hc, base, token, err := dial(dir)
	if err != nil {
		return result, err
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/remote/"+action, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := hc.Do(req)
	if err != nil {
		return result, errors.New("remote action outcome unknown; check remote status before trying again")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&failure) == nil && failure.Error != "" {
			return result, errors.New(failure.Error)
		}
		return result, errors.New("admin API: " + resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result); err != nil {
		return RemoteResult{}, errors.New("remote action response unreadable; check remote status before trying again")
	}
	return result, nil
}
