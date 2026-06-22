package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func TestIsLocalChanDisabled(t *testing.T) {
	tests := []struct {
		name  string
		flags string
		want  bool
	}{
		{name: "empty", flags: "", want: false},
		{name: "local flag", flags: "ChanStatusLocalChanDisabled", want: true},
		{name: "snake local flag", flags: "local_chan_disabled", want: true},
		{name: "generic disabled", flags: "ChanStatusDisabled", want: true},
		{name: "remote disabled", flags: "ChanStatusRemoteChanDisabled", want: false},
		{
			name:  "remote disabled with another local token",
			flags: "ChanStatusLocalCloseInitiator|ChanStatusRemoteChanDisabled",
			want:  false,
		},
		{
			name:  "tokenized local disabled",
			flags: "ChanStatusLocalCloseInitiator|ChanStatusLocalChanDisabled",
			want:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalChanDisabled(tc.flags); got != tc.want {
				t.Fatalf("isLocalChanDisabled(%q) = %v, want %v", tc.flags, got, tc.want)
			}
		})
	}
}

func TestChanStatusHealerReconnectsInactiveDisconnectedPeerAndEnablesAfterRefresh(t *testing.T) {
	const pubkey = "038607b58550d272ce8a058b77bc7a00e099687531359074bb600477f6bb7d1764"
	fake := &fakeChanStatusLND{
		channelsSeq: [][]lndclient.ChannelInfo{
			{
				{ChannelPoint: "txid:1", RemotePubkey: pubkey, Active: false},
			},
			{
				{ChannelPoint: "txid:1", RemotePubkey: pubkey, Active: true, LocalDisabled: true},
			},
		},
		details: map[string]lndclient.NodeDetails{
			pubkey: {
				PubKey: pubkey,
				Addresses: []lndclient.NodeAddress{
					{Network: "tcp", Addr: "203.0.113.10:9735"},
					{Network: "tcp", Addr: "examplepeer.onion:9735"},
				},
			},
		},
	}

	healer := &ChanStatusHealer{lnd: fake, enabled: true}
	healer.tick()

	if len(fake.connectCalls) != 1 {
		t.Fatalf("expected one reconnect attempt, got %d", len(fake.connectCalls))
	}
	call := fake.connectCalls[0]
	if call.pubkey != pubkey || call.host != "203.0.113.10:9735" || call.perm {
		t.Fatalf("unexpected reconnect call: %+v", call)
	}
	if call.timeoutSec != uint64(chanHealConnectTimeout/time.Second) {
		t.Fatalf("unexpected reconnect timeout: got %d", call.timeoutSec)
	}
	if len(fake.updateCalls) != 1 || fake.updateCalls[0] != "txid:1" {
		t.Fatalf("expected channel enable after reconnect, got %+v", fake.updateCalls)
	}

	snap := healer.Snapshot()
	if snap.LastReconnectAttempted != 1 || snap.LastReconnected != 1 || snap.LastReconnectFailed != 0 {
		t.Fatalf("unexpected reconnect stats: %+v", snap)
	}
	if snap.LastUpdated != 1 || snap.LastError != "" {
		t.Fatalf("unexpected heal snapshot: %+v", snap)
	}
}

func TestChanStatusHealerSkipsReconnectWhenPeerAlreadyConnected(t *testing.T) {
	const pubkey = "038607b58550d272ce8a058b77bc7a00e099687531359074bb600477f6bb7d1764"
	fake := &fakeChanStatusLND{
		channelsSeq: [][]lndclient.ChannelInfo{
			{
				{ChannelPoint: "txid:1", RemotePubkey: pubkey, Active: false},
			},
		},
		peers: []lndclient.PeerInfo{{PubKey: pubkey}},
	}

	healer := &ChanStatusHealer{lnd: fake, enabled: true}
	healer.tick()

	if len(fake.connectCalls) != 0 {
		t.Fatalf("expected no reconnect for already connected peer, got %+v", fake.connectCalls)
	}
	snap := healer.Snapshot()
	if snap.LastReconnectAttempted != 0 || snap.LastReconnected != 0 || snap.LastReconnectFailed != 0 {
		t.Fatalf("unexpected reconnect stats: %+v", snap)
	}
	if snap.LastError != "" {
		t.Fatalf("expected no error, got %q", snap.LastError)
	}
}

func TestChanStatusHealerReconnectFailureDoesNotBlockActiveEnable(t *testing.T) {
	const pubkey = "038607b58550d272ce8a058b77bc7a00e099687531359074bb600477f6bb7d1764"
	fake := &fakeChanStatusLND{
		channelsSeq: [][]lndclient.ChannelInfo{
			{
				{ChannelPoint: "offline:1", RemotePubkey: pubkey, Active: false},
				{ChannelPoint: "active:1", RemotePubkey: "02aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Active: true, LocalDisabled: true},
			},
		},
		details: map[string]lndclient.NodeDetails{
			pubkey: {PubKey: pubkey},
		},
	}

	healer := &ChanStatusHealer{lnd: fake, enabled: true}
	healer.tick()

	if len(fake.updateCalls) != 1 || fake.updateCalls[0] != "active:1" {
		t.Fatalf("expected active disabled channel to be enabled, got %+v", fake.updateCalls)
	}
	snap := healer.Snapshot()
	if snap.LastReconnectAttempted != 1 || snap.LastReconnected != 0 || snap.LastReconnectFailed != 1 {
		t.Fatalf("unexpected reconnect stats: %+v", snap)
	}
	if snap.LastUpdated != 1 {
		t.Fatalf("expected one enabled channel despite reconnect failure, got %+v", snap)
	}
	if snap.LastError == "" {
		t.Fatalf("expected reconnect failure to be reported")
	}
}

type fakeChanStatusLND struct {
	channelsSeq       [][]lndclient.ChannelInfo
	listChannelsCalls int
	peers             []lndclient.PeerInfo
	listPeersErr      error
	details           map[string]lndclient.NodeDetails
	detailsErr        error
	connectErr        error
	updateErr         error
	connectCalls      []fakeConnectCall
	updateCalls       []string
}

type fakeConnectCall struct {
	pubkey     string
	host       string
	perm       bool
	timeoutSec uint64
}

func (f *fakeChanStatusLND) ListChannels(ctx context.Context) ([]lndclient.ChannelInfo, error) {
	f.listChannelsCalls++
	if len(f.channelsSeq) == 0 {
		return nil, nil
	}
	idx := f.listChannelsCalls - 1
	if idx >= len(f.channelsSeq) {
		idx = len(f.channelsSeq) - 1
	}
	return f.channelsSeq[idx], nil
}

func (f *fakeChanStatusLND) ListPeers(ctx context.Context) ([]lndclient.PeerInfo, error) {
	if f.listPeersErr != nil {
		return nil, f.listPeersErr
	}
	return f.peers, nil
}

func (f *fakeChanStatusLND) GetNodeDetails(ctx context.Context, pubkey string) (lndclient.NodeDetails, error) {
	if f.detailsErr != nil {
		return lndclient.NodeDetails{}, f.detailsErr
	}
	if f.details == nil {
		return lndclient.NodeDetails{}, errors.New("node not found")
	}
	item, ok := f.details[normalizeChanHealPubkey(pubkey)]
	if !ok {
		return lndclient.NodeDetails{}, errors.New("node not found")
	}
	return item, nil
}

func (f *fakeChanStatusLND) ConnectPeerWithTimeout(ctx context.Context, pubkey string, host string, perm bool, timeoutSec uint64) error {
	f.connectCalls = append(f.connectCalls, fakeConnectCall{
		pubkey:     pubkey,
		host:       host,
		perm:       perm,
		timeoutSec: timeoutSec,
	})
	return f.connectErr
}

func (f *fakeChanStatusLND) UpdateChanStatus(ctx context.Context, channelPoint string, enable bool) error {
	if enable {
		f.updateCalls = append(f.updateCalls, channelPoint)
	}
	return f.updateErr
}
