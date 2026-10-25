package server

import (
	"context"
	"net"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

// lndClearnetStatus reads the announced URIs and the peers from LND. Without
// either the state is unknown: guessing "tor_only" from a missing answer
// would tell an operator to set up clearnet that already works.
func (s *Server) lndClearnetStatus(ctx context.Context, rawConf string) lndClearnetStatus {
	if s == nil || s.lnd == nil {
		return classifyLNDClearnet(rawConf, nil, nil, false)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := s.lnd.GetStatus(ctx)
	if err != nil || !status.InfoKnown {
		return classifyLNDClearnet(rawConf, nil, nil, false)
	}
	peers, err := s.lnd.ListPeers(ctx)
	if err != nil {
		return classifyLNDClearnet(rawConf, status.URIs, nil, false)
	}
	return classifyLNDClearnet(rawConf, status.URIs, peers, true)
}

// Clearnet reachability of the local LND, as shown on the LND config page.
//
// The page's two network presets only change how LND dials out (Tor for
// clearnet targets or not); whether the node can be reached over clearnet is
// decided by listen and externalip/externalhosts/nat, which the presets do
// not touch. A node that announces a public address gains no privacy from
// "Tor only" — the address is already in the graph — and its outbound
// connections to clearnet peers get slower and less reliable through Tor exit
// nodes. A node that announces only an onion address can be reached only
// by Tor-capable peers. The page needs to know which case it is in.
const (
	lndClearnetActive             = "active"               // announced and peers came in over it
	lndClearnetAnnouncedNoInbound = "announced_no_inbound" // announced, nobody came in (port?)
	lndClearnetNotAnnounced       = "configured_not_announced"
	lndClearnetTorOnly            = "tor_only"
	lndClearnetUnknown            = "unknown" // LND status unavailable
)

type lndClearnetStatus struct {
	State                string   `json:"state"`
	ListenConfigured     bool     `json:"listen_configured"`
	ExternalConfigured   bool     `json:"external_configured"`
	AnnouncedAddresses   []string `json:"announced_addresses"`
	ClearnetPeers        int      `json:"clearnet_peers"`
	InboundClearnetPeers int      `json:"inbound_clearnet_peers"`
	TorPeers             int      `json:"tor_peers"`
}

// classifyLNDClearnet combines the user lnd.conf, the URIs LND announces and
// the current peers. "Announced and someone connected in over it" is the
// evidence that clearnet actually works, not only that it is configured.
func classifyLNDClearnet(rawConf string, uris []string, peers []lndclient.PeerInfo, statusKnown bool) lndClearnetStatus {
	out := lndClearnetStatus{AnnouncedAddresses: []string{}}
	out.ListenConfigured, out.ExternalConfigured = lndConfClearnetKeys(rawConf)
	for _, uri := range uris {
		addr := uri
		if at := strings.LastIndex(uri, "@"); at >= 0 {
			addr = uri[at+1:]
		}
		if addr != "" && !isOnionAddress(addr) {
			out.AnnouncedAddresses = append(out.AnnouncedAddresses, addr)
		}
	}
	for _, p := range peers {
		switch {
		case isOnionAddress(p.Address) || isLoopbackPeerAddress(p.Address):
			out.TorPeers++
		case strings.TrimSpace(p.Address) != "":
			out.ClearnetPeers++
			if p.Inbound {
				out.InboundClearnetPeers++
			}
		}
	}
	switch {
	case !statusKnown:
		out.State = lndClearnetUnknown
	case len(out.AnnouncedAddresses) > 0 && out.InboundClearnetPeers > 0:
		out.State = lndClearnetActive
	case len(out.AnnouncedAddresses) > 0:
		out.State = lndClearnetAnnouncedNoInbound
	case out.ListenConfigured || out.ExternalConfigured:
		out.State = lndClearnetNotAnnounced
	default:
		out.State = lndClearnetTorOnly
	}
	return out
}

// lndConfClearnetKeys reports whether the user lnd.conf listens on a
// non-loopback interface and declares a public address (externalip,
// externalhosts or nat=true). Commented lines are ignored.
func lndConfClearnetKeys(raw string) (listen bool, external bool) {
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "listen":
			if value != "" && !isLoopbackPeerAddress(value) {
				listen = true
			}
		case "externalip", "externalhosts":
			if value != "" {
				external = true
			}
		case "nat":
			if strings.EqualFold(value, "true") || value == "1" {
				external = true
			}
		}
	}
	return listen, external
}

func isOnionAddress(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(host)), ".onion")
}

// isLoopbackPeerAddress: peers dialled through the local Tor SOCKS proxy show
// up as 127.0.0.1; a listen on localhost is not reachable from outside.
func isLoopbackPeerAddress(addr string) bool {
	host := strings.TrimSpace(addr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
