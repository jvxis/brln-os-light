package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestClassifyLNDClearnet(t *testing.T) {
	const onion = "02aa@fmvh2654bp7fcoysr5qikkxsbheps4efpsi5cg2mj7csv5c5t4o3brad.onion:9735"
	const clear = "02aa@203.0.113.56:9735"
	// Friendspool, 25/10/2026: listen on all interfaces + externalhosts,
	// announces onion and clearnet, 95 of 113 peers over clearnet.
	routingConf := "[Application Options]\nlisten=0.0.0.0:9735\nexternalhosts=node.example.com:9735\n[tor]\ntor.active=true\n"
	// Installer template: clearnet lines commented out, Tor only.
	templateConf := "[Application Options]\n# listen=0.0.0.0:9735\n# externalip=ip_externo:porta\n[tor]\ntor.active=true\n"
	inboundClear := lndclient.PeerInfo{Address: "198.51.100.7:51234", Inbound: true}
	outboundClear := lndclient.PeerInfo{Address: "198.51.100.8:9735"}
	viaTor := lndclient.PeerInfo{Address: "127.0.0.1:41000"}
	onionPeer := lndclient.PeerInfo{Address: "abcdefghijklmnop.onion:9735", Inbound: true}

	cases := []struct {
		name       string
		conf       string
		uris       []string
		peers      []lndclient.PeerInfo
		known      bool
		wantState  string
		wantListen bool
		wantExt    bool
		wantIn     int
		wantTor    int
	}{
		{name: "routing node, clearnet working", conf: routingConf, uris: []string{onion, clear}, peers: []lndclient.PeerInfo{inboundClear, outboundClear, viaTor}, known: true, wantState: "active", wantListen: true, wantExt: true, wantIn: 1, wantTor: 1},
		{name: "announced but nobody came in", conf: routingConf, uris: []string{onion, clear}, peers: []lndclient.PeerInfo{outboundClear, viaTor}, known: true, wantState: "announced_no_inbound", wantListen: true, wantExt: true, wantTor: 1},
		{name: "configured but LND announces only onion", conf: routingConf, uris: []string{onion}, peers: []lndclient.PeerInfo{viaTor}, known: true, wantState: "configured_not_announced", wantListen: true, wantExt: true, wantTor: 1},
		{name: "installer template is Tor only", conf: templateConf, uris: []string{onion}, peers: []lndclient.PeerInfo{viaTor, onionPeer}, known: true, wantState: "tor_only", wantTor: 2},
		{name: "listen on localhost is not clearnet", conf: "listen=127.0.0.1:9735\n", uris: []string{onion}, known: true, wantState: "tor_only"},
		{name: "nat=true counts as an external address", conf: "nat=true\n", uris: []string{onion}, known: true, wantState: "configured_not_announced", wantExt: true},
		{name: "LND status unavailable", conf: routingConf, known: false, wantState: "unknown", wantListen: true, wantExt: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyLNDClearnet(tc.conf, tc.uris, tc.peers, tc.known)
			if got.State != tc.wantState || got.ListenConfigured != tc.wantListen || got.ExternalConfigured != tc.wantExt || got.InboundClearnetPeers != tc.wantIn || got.TorPeers != tc.wantTor {
				t.Fatalf("got %+v, want state=%s listen=%v external=%v inbound=%d tor=%d", got, tc.wantState, tc.wantListen, tc.wantExt, tc.wantIn, tc.wantTor)
			}
		})
	}
	if got := classifyLNDClearnet(routingConf, []string{onion, clear}, nil, true); len(got.AnnouncedAddresses) != 1 || got.AnnouncedAddresses[0] != "203.0.113.56:9735" {
		t.Fatalf("announced clearnet addresses = %v", got.AnnouncedAddresses)
	}
}
