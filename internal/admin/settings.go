package admin

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

// SettingsPatch is a whitelist; absent fields never overwrite remembered host settings.
type SettingsPatch struct {
	Name        *string `json:"name"`
	WebURL      *string `json:"web_url"`
	Slots       *int    `json:"slots"`
	Console     *string `json:"console"`
	LogRequests *bool   `json:"log_requests"`
}

func (p SettingsPatch) Validate(remote bool) error {
	if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
		return errors.New("name is required")
	}
	if p.Slots != nil && *p.Slots < 0 {
		return errors.New("slots must be zero or greater")
	}
	if p.Console != nil {
		if remote {
			return errors.New("console address: not from here — --console, on the host")
		}
		if *p.Console != "off" {
			a, e := netip.ParseAddrPort(*p.Console)
			if e != nil || !a.Addr().IsLoopback() {
				return errors.New("console address must be a loopback IP:port or off")
			}
		}
	}
	if p.WebURL != nil && *p.WebURL != "" {
		u, err := url.Parse(*p.WebURL)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return errors.New("web app URL must be an absolute URL without credentials, query or fragment")
		}
		a, _ := netip.ParseAddr(u.Hostname())
		local := u.Hostname() == "localhost" || a.IsLoopback() || (a.Is4() && a.IsPrivate())
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return errors.New("not https — whoever answers that address can read the code in the link")
		}
	}
	return nil
}
