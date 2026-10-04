package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

type recoveryFake struct {
	fakeChanStatusLND
	peerReads                              int
	peersAfterFirst                        []lndclient.PeerInfo
	changePeers                            bool
	pending                                []lndclient.PendingChannelInfo
	pendingErr, channelsErr, disconnectErr error
	disconnected                           []string
}

func (f *recoveryFake) ListPeers(ctx context.Context) ([]lndclient.PeerInfo, error) {
	f.peerReads++
	if f.changePeers && f.peerReads > 1 {
		return f.peersAfterFirst, nil
	}
	return f.fakeChanStatusLND.ListPeers(ctx)
}

func (f *recoveryFake) ListPendingChannels(context.Context) ([]lndclient.PendingChannelInfo, error) {
	return f.pending, f.pendingErr
}
func (f *recoveryFake) ListChannels(ctx context.Context) ([]lndclient.ChannelInfo, error) {
	if f.channelsErr != nil {
		return nil, f.channelsErr
	}
	return f.fakeChanStatusLND.ListChannels(ctx)
}
func (f *recoveryFake) DisconnectPeer(_ context.Context, key string) error {
	f.disconnected = append(f.disconnected, key)
	return f.disconnectErr
}

func recoveryFixture(t *testing.T) (*chanPeerRecovery, *recoveryFake, []lndclient.ChannelInfo, time.Time) {
	t.Helper()
	channels := []lndclient.ChannelInfo{{RemotePubkey: "peer", ChannelPoint: "tx:0"}}
	f := &recoveryFake{fakeChanStatusLND: fakeChanStatusLND{channelsSeq: [][]lndclient.ChannelInfo{channels}, peers: []lndclient.PeerInfo{{PubKey: "peer"}}}}
	r := newChanPeerRecovery(f, nil, filepath.Join(t.TempDir(), "state.json"), 15*time.Minute)
	return r, f, channels, time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
}
func recoveryStep(t *testing.T, r *chanPeerRecovery, at time.Time, channels []lndclient.ChannelInfo) map[string]bool {
	t.Helper()
	blocked, _, err := r.step(at, 5*time.Minute, channels, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	return blocked
}
func observeRecovery(t *testing.T, r *chanPeerRecovery, at time.Time, channels []lndclient.ChannelInfo) {
	t.Helper()
	for i := 0; i < 4; i++ {
		recoveryStep(t, r, at.Add(time.Duration(i)*5*time.Minute), channels)
	}
}

func TestChanRecoveryWaitVerifyAndBudgetAcrossRestart(t *testing.T) {
	r, f, ch, at := recoveryFixture(t)
	for i := 0; i < 3; i++ {
		recoveryStep(t, r, at.Add(time.Duration(i)*5*time.Minute), ch)
	}
	if len(f.disconnected) != 0 {
		t.Fatal("disconnected before persistence threshold")
	}
	if !recoveryStep(t, r, at.Add(15*time.Minute), ch)["peer"] || len(f.disconnected) != 1 {
		t.Fatal("expected one disconnect and legacy suppression")
	}
	loaded := newChanPeerRecovery(f, nil, r.path, r.wait)
	if len(loaded.peers["peer"].Attempts) != 1 || !loaded.peers["peer"].Since.IsZero() {
		t.Fatal("restart lost budget or restored observation")
	}
	recoveryStep(t, loaded, at.Add(20*time.Minute), ch)
	if loaded.peers["peer"].State != "verifying" {
		t.Fatal("premature verification failure")
	}
	recoveryStep(t, loaded, at.Add(25*time.Minute), ch)
	if loaded.peers["peer"].State != "failed" {
		t.Fatal("missing channel-inactive failure")
	}
	observeRecovery(t, loaded, at.Add(time.Hour), ch)
	if len(f.disconnected) != 1 {
		t.Fatal("cooldown bypassed")
	}
	observeRecovery(t, loaded, at.Add(80*time.Minute), ch)
	if len(f.disconnected) != 2 {
		t.Fatal("expected second bounded attempt")
	}
	recoveryStep(t, loaded, at.Add(110*time.Minute), ch)
	loaded = newChanPeerRecovery(f, nil, r.path, r.wait)
	observeRecovery(t, loaded, at.Add(3*time.Hour), ch)
	if len(f.disconnected) != 2 || loaded.peers["peer"].State != "exhausted" {
		t.Fatal("attempt budget bypassed after restart")
	}
}

func TestChanRecoveryCancellationAndUnsafeConditions(t *testing.T) {
	for _, name := range []string{"active-sibling", "pending-htlc", "htlc-detail", "unsettled", "pending-channel", "unknown-pending-peer", "closing-flag", "missing-point", "disconnected"} {
		t.Run(name, func(t *testing.T) {
			r, f, ch, at := recoveryFixture(t)
			switch name {
			case "active-sibling":
				ch = append(ch, lndclient.ChannelInfo{RemotePubkey: "peer", ChannelPoint: "tx:1", Active: true})
			case "pending-htlc":
				ch[0].PendingHtlcCount = 1
			case "htlc-detail":
				ch[0].PendingHtlcs = []lndclient.ChannelPendingHtlcInfo{{}}
			case "unsettled":
				ch[0].UnsettledBalanceSat = 1
			case "pending-channel":
				f.pending = []lndclient.PendingChannelInfo{{RemotePubkey: "peer"}}
			case "unknown-pending-peer":
				f.pending = []lndclient.PendingChannelInfo{{}}
			case "closing-flag":
				ch[0].ChanStatusFlags = "ChanStatusCoopBroadcasted"
			case "missing-point":
				ch[0].ChannelPoint = ""
			case "disconnected":
				f.peers = nil
			}
			observeRecovery(t, r, at, ch)
			if len(f.disconnected) != 0 {
				t.Fatal("unsafe disconnect")
			}
		})
	}
}

func TestChanRecoveryRevalidatesImmediatelyBeforeAction(t *testing.T) {
	for _, name := range []string{"active", "peer-gone", "pending", "channels-error", "peers-error", "pending-error", "disabled", "new-channel"} {
		t.Run(name, func(t *testing.T) {
			r, f, ch, at := recoveryFixture(t)
			for i := 0; i < 3; i++ {
				recoveryStep(t, r, at.Add(time.Duration(i)*5*time.Minute), ch)
			}
			enabled := true
			switch name {
			case "active":
				f.channelsSeq = [][]lndclient.ChannelInfo{{{RemotePubkey: "peer", ChannelPoint: "tx:0", Active: true}}}
			case "peer-gone":
				f.peers = nil
			case "pending":
				f.pending = []lndclient.PendingChannelInfo{{RemotePubkey: "peer"}}
			case "channels-error":
				f.channelsErr = errors.New("RPC error")
			case "peers-error":
				f.listPeersErr = errors.New("RPC error")
			case "pending-error":
				f.pendingErr = errors.New("RPC error")
			case "disabled":
				enabled = false
			case "new-channel":
				f.channelsSeq = [][]lndclient.ChannelInfo{{{RemotePubkey: "peer", ChannelPoint: "new:0"}}}
			}
			_, _, err := r.step(at.Add(15*time.Minute), 5*time.Minute, ch, func() bool { return enabled })
			if (name == "channels-error" || name == "peers-error" || name == "pending-error") && err == nil {
				t.Fatal("query error hidden")
			}
			if len(f.disconnected) != 0 {
				t.Fatal("stale snapshot caused disconnect")
			}
		})
	}
}

func TestChanRecoveryResetsObservationAfterGapOrChangedChannels(t *testing.T) {
	for _, name := range []string{"gap", "rpc-error", "channel-change", "disable"} {
		t.Run(name, func(t *testing.T) {
			r, f, ch, at := recoveryFixture(t)
			for i := 0; i < 3; i++ {
				recoveryStep(t, r, at.Add(time.Duration(i)*5*time.Minute), ch)
			}
			when := at.Add(15 * time.Minute)
			switch name {
			case "gap":
				when = at.Add(time.Hour)
			case "rpc-error":
				f.listPeersErr = errors.New("RPC error")
				_, _, _ = r.step(when, 5*time.Minute, ch, func() bool { return true })
				f.listPeersErr = nil
				when = when.Add(5 * time.Minute)
			case "channel-change":
				ch[0].ChannelPoint = "new:0"
			case "disable":
				r.resetObservation()
			}
			recoveryStep(t, r, when, ch)
			if len(f.disconnected) != 0 {
				t.Fatal("observation was not reset")
			}
		})
	}
}

func TestChanRecoveryResultAndNotification(t *testing.T) {
	for _, name := range []string{"success", "partial", "peer-offline", "disconnect-failure"} {
		t.Run(name, func(t *testing.T) {
			r, f, ch, at := recoveryFixture(t)
			ch = append(ch, lndclient.ChannelInfo{RemotePubkey: "peer", ChannelPoint: "tx:1"})
			f.channelsSeq = [][]lndclient.ChannelInfo{ch}
			var events []string
			r.notify = func(_, state, _ string, _ time.Time) { events = append(events, state) }
			if name == "disconnect-failure" {
				f.disconnectErr = errors.New("LND refused")
			}
			observeRecovery(t, r, at, ch)
			if len(f.disconnected) != 1 {
				t.Fatal("must deduplicate by peer")
			}
			want := "failed"
			switch name {
			case "success":
				ch[0].Active = true
				ch[1].Active = true
				want = "recovered"
			case "partial":
				ch[0].Active = true
				want = "cancelled"
			case "peer-offline":
				f.peers = nil
			}
			recoveryStep(t, r, at.Add(25*time.Minute), ch)
			if r.peers["peer"].State != want {
				t.Fatalf("want %s got %s", want, r.peers["peer"].State)
			}
			if want != "cancelled" && (len(events) != 1 || events[0] != want) {
				t.Fatalf("bad notifications: %v", events)
			}
		})
	}
}

func TestChanRecoveryCannotDisconnectWithoutDurableBudget(t *testing.T) {
	r, f, ch, at := recoveryFixture(t)
	for i := 0; i < 3; i++ {
		recoveryStep(t, r, at.Add(time.Duration(i)*5*time.Minute), ch)
	}
	r.path = filepath.Join(t.TempDir(), "missing-directory", "state.json")
	if _, _, err := r.step(at.Add(15*time.Minute), 5*time.Minute, ch, func() bool { return true }); err == nil {
		t.Fatal("save failure hidden")
	}
	if len(f.disconnected) != 0 {
		t.Fatal("disconnect before saving budget")
	}
	r.path = filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(r.path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	r = newChanPeerRecovery(f, nil, r.path, r.wait)
	if _, _, err := r.step(at.Add(time.Hour), 5*time.Minute, ch, func() bool { return true }); err == nil {
		t.Fatal("invalid state reset budget")
	}
}

func TestChanRecoveryOnePeerPerCycle(t *testing.T) {
	r, f, ch, at := recoveryFixture(t)
	ch = append(ch, lndclient.ChannelInfo{RemotePubkey: "peer2", ChannelPoint: "tx:2"})
	f.channelsSeq = [][]lndclient.ChannelInfo{ch}
	f.peers = append(f.peers, lndclient.PeerInfo{PubKey: "peer2"})
	observeRecovery(t, r, at, ch)
	if len(f.disconnected) != 1 {
		t.Fatal("more than one peer disconnected in cycle")
	}
}

func TestChanRecoveryVerificationSuppressesLegacyReconnect(t *testing.T) {
	r, f, _, at := recoveryFixture(t)
	f.peers = nil
	r.peers["peer"] = &chanRecoveryPeer{Pubkey: "peer", State: "verifying", VerifyUntil: time.Now().Add(time.Hour), Points: []string{"tx:0"}, Attempts: []time.Time{at}}
	healer := &ChanStatusHealer{lnd: f, enabled: true, recovery: r}
	healer.tick()
	if len(f.connectCalls) != 0 || len(f.disconnected) != 0 {
		t.Fatal("verification invoked a connect/disconnect RPC")
	}
}

func TestChanRecoveryPeerLeavesDuringFinalRevalidation(t *testing.T) {
	r, f, ch, at := recoveryFixture(t)
	for i := 0; i < 3; i++ {
		recoveryStep(t, r, at.Add(time.Duration(i)*5*time.Minute), ch)
	}
	f.peerReads = 0
	f.changePeers = true
	recoveryStep(t, r, at.Add(15*time.Minute), ch)
	if len(f.disconnected) != 0 || !r.peers["peer"].Since.IsZero() {
		t.Fatal("peer disappeared but recovery was not cancelled")
	}
}

func TestChanRecoveryErrorDoesNotDisableExistingHeal(t *testing.T) {
	r, f, ch, _ := recoveryFixture(t)
	r.loadErr = errors.New("unreadable state")
	ch[0].Active = true
	ch[0].LocalDisabled = true
	f.channelsSeq = [][]lndclient.ChannelInfo{ch}
	healer := &ChanStatusHealer{lnd: f, enabled: true, recovery: r}
	healer.tick()
	if len(f.updateCalls) != 1 || len(f.disconnected) != 0 {
		t.Fatal("new recovery failure blocked the existing healer")
	}
	if healer.Snapshot().Status != "warn" {
		t.Fatal("recovery error hidden")
	}
}

func TestChanRecoveryWaitConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"60", time.Minute}, {"900", 15 * time.Minute}, {"86400", 24 * time.Hour}, {"0", 15 * time.Minute}, {"59", 15 * time.Minute}, {"86401", 15 * time.Minute}, {"invalid", 15 * time.Minute}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(chanRecoveryWaitEnv, tc.value)
			if got := readChanRecoveryWait(); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestChanRecoveryWarningClearsWhenChannelsRecoverLater(t *testing.T) {
	r, f, ch, at := recoveryFixture(t)
	f.disconnectErr = errors.New("refused")
	observeRecovery(t, r, at, ch)
	ch[0].Active = true
	recoveryStep(t, r, at.Add(20*time.Minute), ch)
	if r.peers["peer"].State != "recovered" {
		t.Fatal("stale failure after recovery")
	}
}

func TestChanRecoveryTelegramIsOperationalNotFinancial(t *testing.T) {
	msg := telegramActivityMirrorMessage(Notification{Type: "channel", Action: "recovery", Status: "FAILED", PeerPubkey: "peer", Memo: "manual review required"})
	if !strings.Contains(msg, "FAILED") || !strings.Contains(msg, "manual review required") || strings.Contains(msg, "sats") {
		t.Fatalf("bad recovery notification: %s", msg)
	}
}

func TestChanRecoveryBudgetExpiresAfter24Hours(t *testing.T) {
	r, f, ch, at := recoveryFixture(t)
	r.peers["peer"] = &chanRecoveryPeer{Pubkey: "peer", State: "exhausted", Attempts: []time.Time{at.Add(-25 * time.Hour), at.Add(-24 * time.Hour)}}
	observeRecovery(t, r, at, ch)
	if len(f.disconnected) != 1 || len(r.peers["peer"].Attempts) != 1 {
		t.Fatal("rolling window did not expire")
	}
}
