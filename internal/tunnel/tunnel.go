// Package tunnel wraps a tailcat.Server as a net.Listener for exactly one tunnel port.
//
// Protection 1 (pm/BELIEFS.md): OnTCP returns nil for every port but Port, ServedTCPPorts narrows
// the packet filter to Port so other SYNs are dropped before netstack, and OnTCPForward,
// AllowProxy, SSH, and file services are never set.
//
// The host identity lives in <DataDir>/host.key.json as tailcat PrivateKey JSON with the relay
// region pinned at creation, so the address survives restarts and SavedAddr derives it offline.
package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
	"tailscale.com/types/logger"
	"tailscale.com/wgengine/filter"
)

const (
	Port    = 80              // the only tunnel port the host serves
	KeyFile = "host.key.json" // the host key's file name inside Options.DataDir
)

// ErrUnpinned: the saved key has no relay region pinned, so SavedAddr cannot derive an address.
var ErrUnpinned = errors.New("tunnel: saved host key has no relay region pinned")

type Options struct {
	DataDir   string // holds KeyFile; required unless Ephemeral
	Ephemeral bool   // never touch disk: a new identity, and so a new address, every Start
	// DERPMapURL is an alternate DERP map. When set, the address embeds the relay (full form).
	DERPMapURL string
	// Region pins the relay for a NEW key: a region ID ("302"), code ("sfo"), name substring, or
	// comma-separated DERP hostnames for relays outside any map. Empty picks the nearest by
	// latency once. When set, the address is full form. A saved key keeps its region.
	Region string
	Logf   func(string, ...any) // nil = silent
}

type Status struct {
	Addr       string
	RegionName string
	Started    time.Time
	// Clients counts distinct clients with an open connection on Port right now; tailcat
	// exposes no peer list, so a browser between requests counts as zero.
	Clients int
}

// Peer is one client this server has met (ticket 029), as far as a host can see it: tailcat
// 0.4.0 keeps WireGuard peer state — the path, the handshake — on the client side, so what the
// host meters is the TCP payload on Port per client tunnel address (which tailcat derives from
// the client's node key: it is the client's identity), its open connections, and its activity.
type Peer struct {
	Addr     netip.Addr
	Conns    int   // open connections right now
	RxBytes  int64 // received from the client
	TxBytes  int64 // sent to the client
	Since    time.Time
	LastByte time.Time // zero: none yet
}

// Server is a running tunnel. Listener accepts its connections; Close stops it.
type Server struct {
	tc      *tailcat.Server
	addr    string
	region  *tailcfg.DERPRegion
	started time.Time
	ln      *listener
}

// Start loads or creates the host key and starts the tailcat server on the pinned relay. ctx
// bounds relay selection and DERP map fetches; the server outlives it.
func Start(ctx context.Context, o Options) (*Server, error) {
	if !o.Ephemeral && o.DataDir == "" {
		return nil, errors.New("tunnel: DataDir is required unless Ephemeral")
	}
	logf := logger.Discard
	if o.Logf != nil {
		logf = quiet(o.Logf)
	}
	pk, reg, err := identity(ctx, o)
	if err != nil {
		return nil, err
	}
	s := &Server{addr: addrFor(pk), region: reg}
	s.ln = newListener(s)
	s.tc = newTailcatServer(pk, reg, logf, s.onTCP)
	if err := s.tc.Start(); err != nil {
		s.tc.Close()
		return nil, fmt.Errorf("tunnel: starting tailcat server: %w", err)
	}
	s.started = time.Now()
	return s, nil
}

// identity resolves the host identity this server will answer as, together with the relay region
// pinned into it. On return, and for a non-ephemeral host, the key is byte-for-byte the one in
// <DataDir>/KeyFile, so Addr() always equals SavedAddr(DataDir) and every invite minted in the
// first session keeps working.
//
// That guarantee is the fix for ticket 009 promise 1. A fresh data dir used to be filled by
// whichever `serve` renamed its key last, while each process went on serving — and minting
// invites for — the identity it had generated in memory; two hosts starting in the same second
// (the stranger's `serve` plus a second one that only failed later, at the admin socket) left the
// survivor advertising an address whose key no longer existed. The identity file is now created
// once: a loser adopts the winner's key rather than inventing its own.
func identity(ctx context.Context, o Options) (*tailcat.PrivateKey, *tailcfg.DERPRegion, error) {
	pk, err := loadKey(o)
	if err != nil {
		return nil, nil, err
	}
	for adopted := false; ; adopted = true {
		ci := pk.Public
		pin := len(ci.Region) == 0 && ci.RegionID <= 0 // a fresh (or unpinned) key
		if pin {
			if adopted {
				return nil, nil, ErrUnpinned
			}
			if err := chooseRegion(ctx, o, &ci); err != nil {
				return nil, nil, err
			}
		}
		if err := ci.Expand(ctx, expandOpts(o)...); err != nil {
			return nil, nil, fmt.Errorf("tunnel: resolving relay region: %w", err)
		}
		reg := ci.Region[0]
		if !pin {
			return pk, reg, nil // the saved key already names its relay; serve on that one
		}
		pinRegion(pk, reg, o.DERPMapURL != "" || o.Region != "")
		if o.Ephemeral {
			return pk, reg, nil // pinned in memory only; nothing on disk to agree with
		}
		saved, err := saveKey(filepath.Join(o.DataDir, KeyFile), pk)
		if err != nil {
			return nil, nil, err
		}
		if addrFor(saved) == addrFor(pk) {
			return pk, reg, nil
		}
		pk = saved // another host got there first: take its identity, then re-resolve its relay
	}
}

// quiet drops the NetworkMap dump tailcat logs at startup (one JSON line describing this very
// node) from whatever the caller's Logf feeds: it is never useful to a host and never belongs on
// a terminal (ticket 005 fix 10b). Everything else passes through untouched.
func quiet(logf logger.Logf) logger.Logf {
	return func(format string, args ...any) {
		if strings.HasPrefix(format, "NetworkMap:") {
			return
		}
		logf(format, args...)
	}
}

// newTailcatServer is the whole tailcat configuration; a test pins what it must never set.
func newTailcatServer(pk *tailcat.PrivateKey, reg *tailcfg.DERPRegion, logf logger.Logf, onTCP func(uint16) func(net.Conn)) *tailcat.Server {
	return &tailcat.Server{
		Key:            pk.Private,
		Logf:           logf,
		Region:         reg,
		ServedTCPPorts: []filter.PortRange{{First: Port, Last: Port}},
		OnTCP:          onTCP,
	}
}

// SavedAddr derives the address from the key saved under dataDir without starting a server.
// With no saved key the error wraps os.ErrNotExist.
func SavedAddr(dataDir string) (string, error) {
	pk, err := readKey(filepath.Join(dataDir, KeyFile))
	if err != nil {
		return "", err
	}
	if len(pk.Public.Region) == 0 && pk.Public.RegionID <= 0 {
		return "", ErrUnpinned
	}
	return addrFor(pk), nil
}

// Listener yields the connections addressed to Port. Accept blocks; Close stops accepting.
func (s *Server) Listener() net.Listener { return s.ln }

// Addr is the tc… ConnBlob clients connect to: short form (a DERP map region reference) by
// default, full form (relay embedded) when the key was created with a DERPMapURL or Region.
func (s *Server) Addr() string { return s.addr }

func (s *Server) Status() Status {
	return Status{Addr: s.addr, RegionName: regionName(s.region), Started: s.started, Clients: s.ln.clients()}
}

// Peers lists every client that ever reached Port, oldest first. A client is never forgotten
// (a browser that reconnects under a fresh identity is a new peer), so the list grows with
// distinct identities, not with traffic.
func (s *Server) Peers() []Peer { return s.ln.peers() }

// Close stops accepting and shuts the tailcat server down, closing every tunnel connection;
// bounded by closeTimeout like the client's (ticket 035), so a host's exit never waits on tailcat.
func (s *Server) Close() error {
	s.ln.Close()
	return closeWithin(closeTimeout, s.tc.Close)
}

// onTCP is the per-port gate: only Port, and only while the listener is open.
func (s *Server) onTCP(port uint16) func(net.Conn) {
	if port != Port || s.ln.closed() {
		return nil // RST
	}
	return s.ln.deliver
}

func loadKey(o Options) (*tailcat.PrivateKey, error) {
	if o.Ephemeral {
		return tailcat.NewPrivateKey(), nil
	}
	pk, err := readKey(filepath.Join(o.DataDir, KeyFile))
	if errors.Is(err, os.ErrNotExist) {
		return tailcat.NewPrivateKey(), nil
	}
	return pk, err
}

func readKey(path string) (*tailcat.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tunnel: reading host key: %w", err)
	}
	pk := new(tailcat.PrivateKey)
	if err := json.Unmarshal(b, pk); err != nil {
		return nil, fmt.Errorf("tunnel: parsing %s: %w", path, err)
	}
	if pk.Private.IsZero() {
		return nil, fmt.Errorf("tunnel: %s has no private key", path)
	}
	return pk, nil
}

// saveKey publishes pk as the host identity and returns the identity that is on disk afterwards —
// pk itself, or the one another host published first. The host key is created once and never
// overwritten: the content is staged in a fresh O_EXCL temp file beside the target (never a
// predictable name; ticket 005 fix 10f) and claimed with a hard link, which is both atomic and
// exclusive, so a reader never sees a half file and a second host never replaces a live identity
// (ticket 009 promise 1). Mode 0600 in a 0700 directory.
//
// A filesystem without hard links falls back to rename and re-reads what landed, which keeps the
// "the address is what is on disk" guarantee even though the last writer then wins the file.
func saveKey(path string, pk *tailcat.PrivateKey) (*tailcat.PrivateKey, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("tunnel: creating data dir: %w", err)
	}
	b, err := json.MarshalIndent(pk, "", "\t")
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*") // 0600, O_EXCL
	if err != nil {
		return nil, fmt.Errorf("tunnel: writing host key: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op only if the temp file was renamed away
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("tunnel: writing host key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("tunnel: writing host key: %w", err)
	}
	switch err := os.Link(tmp.Name(), path); {
	case err == nil:
		return pk, nil
	case errors.Is(err, os.ErrExist):
		return readKey(path) // another host published first; its key is the host's identity
	default:
		if err := os.Rename(tmp.Name(), path); err != nil {
			return nil, fmt.Errorf("tunnel: writing host key: %w", err)
		}
		return readKey(path)
	}
}

// addrFor builds the ConnBlob from the private key and the pinned region. The public keys are
// derived from the private key, not read from the file, so the address always matches the server.
func addrFor(pk *tailcat.PrivateKey) string {
	ci := tailcat.ConnInfo{
		ServerPublic:      tailcat.NodePublic{NodePublic: pk.Private.Public()},
		ServerDiscoPublic: tailcat.DiscoPublicForNode(pk.Private),
		Region:            pk.Public.Region,
		RegionID:          pk.Public.RegionID,
	}
	return string(ci.ConnBlob())
}

// pinRegion records the relay in the key: embedded (full form; two relay nodes suffice, as in
// tailcat's ConnBlob.Resolve) or as a DERP map region ID (short form).
func pinRegion(pk *tailcat.PrivateKey, reg *tailcfg.DERPRegion, full bool) {
	pk.Public.Region, pk.Public.RegionID = nil, 0
	if !full {
		pk.Public.RegionID = reg.RegionID
		return
	}
	r := *reg
	r.Nodes = slices.Clone(reg.Nodes[:min(2, len(reg.Nodes))])
	pk.Public.Region = []*tailcfg.DERPRegion{&r}
}

func expandOpts(o Options) []any {
	opts := []any{tailcat.ExpandForServer}
	if o.DERPMapURL != "" {
		opts = append(opts, tailcat.DERPMapURL(o.DERPMapURL))
	}
	return opts
}

// chooseRegion sets ci.RegionID or ci.Region from Options.Region for a new key.
func chooseRegion(ctx context.Context, o Options, ci *tailcat.ConnInfo) error {
	spec := strings.TrimSpace(o.Region)
	switch {
	case spec == "":
		ci.RegionID = -1 // nearest by latency, chosen once by Expand
	case strings.Contains(spec, "."):
		reg := &tailcfg.DERPRegion{}
		for _, h := range strings.Split(spec, ",") {
			reg.Nodes = append(reg.Nodes, &tailcfg.DERPNode{HostName: strings.TrimSpace(h)})
		}
		ci.Region = []*tailcfg.DERPRegion{reg}
	default:
		if n, err := strconv.Atoi(spec); err == nil {
			if n <= 0 {
				return fmt.Errorf("tunnel: invalid relay region %q", spec)
			}
			ci.RegionID = tailcfg.DERPRegionID(n)
			return nil
		}
		dm, err := tailcat.FetchDERPMap(ctx, expandOpts(o)...)
		if err != nil {
			return fmt.Errorf("tunnel: fetching DERP map: %w", err)
		}
		var codes []string
		for _, r := range dm.Regions {
			if strings.EqualFold(r.RegionCode, spec) {
				ci.RegionID = r.RegionID
				return nil
			}
			codes = append(codes, r.RegionCode)
		}
		for _, r := range dm.Regions {
			if strings.Contains(strings.ToLower(r.RegionName), strings.ToLower(spec)) {
				ci.RegionID = r.RegionID
				return nil
			}
		}
		slices.Sort(codes)
		return fmt.Errorf("tunnel: no relay region matches %q (codes: %s)", spec, strings.Join(codes, ", "))
	}
	return nil
}

func regionName(r *tailcfg.DERPRegion) string {
	switch {
	case r.RegionName != "":
		return r.RegionName
	case r.RegionCode != "" && r.RegionCode != strconv.Itoa(int(r.RegionID)):
		return r.RegionCode
	case len(r.Nodes) > 0 && r.Nodes[0].HostName != "":
		return r.Nodes[0].HostName
	}
	return strconv.Itoa(int(r.RegionID))
}
