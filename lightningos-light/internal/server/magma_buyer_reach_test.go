package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

// magmaReachLND is the smallest fake that satisfies magmaLND. Only the peer
// calls carry behaviour; the rest exist so the interface is satisfied.
type magmaReachLND struct {
	peers    []lndclient.PeerInfo
	dialErrs map[string]error
	dialed   []string
}

func (f *magmaReachLND) ListPeers(context.Context) ([]lndclient.PeerInfo, error) {
	return f.peers, nil
}

func (f *magmaReachLND) ConnectPeerWithTimeout(
	_ context.Context, _ string, host string, _ bool, _ uint64,
) error {
	f.dialed = append(f.dialed, host)
	if err, ok := f.dialErrs[host]; ok {
		return err
	}
	return nil
}

func (f *magmaReachLND) CreateInvoice(context.Context, int64, string, int64, *lndclient.CreateInvoiceOptions) (lndclient.CreatedInvoice, error) {
	return lndclient.CreatedInvoice{}, errors.New("not used")
}
func (f *magmaReachLND) GetBalances(context.Context) (lndclient.BalanceSummary, error) {
	return lndclient.BalanceSummary{}, nil
}
func (f *magmaReachLND) GetNodeDetails(context.Context, string) (lndclient.NodeDetails, error) {
	return lndclient.NodeDetails{}, nil
}
func (f *magmaReachLND) OpenChannelWithOutpoints(context.Context, lndclient.OpenChannelParams) (string, error) {
	return "", errors.New("not used")
}
func (f *magmaReachLND) PreviewOpenChannel(context.Context, int64, int64, int64) (lndclient.OpenChannelPreview, error) {
	return lndclient.OpenChannelPreview{}, nil
}
func (f *magmaReachLND) ListPendingChannels(context.Context) ([]lndclient.PendingChannelInfo, error) {
	return nil, nil
}
func (f *magmaReachLND) ListChannels(context.Context) ([]lndclient.ChannelInfo, error) {
	return nil, nil
}

// A buyer we are already connected to is reached. ConnectPeer errors for a peer
// that is already there, so reading that error as unreachable would refuse every
// buyer we are already talking to - the worst possible way for this gate to fail.
func TestMagmaBuyerAlreadyConnectedCountsAsReached(t *testing.T) {
	const buyer = "033035870d69d43fae1c8019d1f31358768e0b67fed2f68ce7f2cdcf603c891488"
	lnd := &magmaReachLND{peers: []lndclient.PeerInfo{{PubKey: strings.ToUpper(buyer)}}}
	s := &MagmaService{lnd: lnd}

	if err := s.reachBuyer(context.Background(), "token", buyer); err != nil {
		t.Fatalf("an already-connected buyer must count as reached, got %v", err)
	}
	if len(lnd.dialed) != 0 {
		t.Fatalf("no dial should be needed, got %v", lnd.dialed)
	}
}

// A node can advertise both Tor and clearnet with only one of them working, so
// one failed dial settles nothing: every address gets tried.
func TestMagmaBuyerDialTriesEveryAddress(t *testing.T) {
	tor := "abc.onion:9735"
	clear := "1.2.3.4:9735"
	lnd := &magmaReachLND{dialErrs: map[string]error{tor: errors.New("dial timeout")}}
	s := &MagmaService{lnd: lnd}

	if err := s.dialBuyer(context.Background(), "pub", []string{tor, clear}, 5*time.Second); err != nil {
		t.Fatalf("clearnet worked, so the buyer is reachable: %v", err)
	}
	if len(lnd.dialed) != 2 || lnd.dialed[0] != tor || lnd.dialed[1] != clear {
		t.Fatalf("expected Tor then clearnet, got %v", lnd.dialed)
	}
}

// Tor dials fail spuriously and a node can be seconds from coming back, so the
// whole address set is retried until the budget is spent rather than concluding
// from a single pass.
func TestMagmaBuyerDialRetriesUntilBudgetSpent(t *testing.T) {
	addr := "abc.onion:9735"
	lnd := &magmaReachLND{dialErrs: map[string]error{addr: errors.New("dial timeout")}}
	s := &MagmaService{lnd: lnd}

	err := s.dialBuyer(context.Background(), "pub", []string{addr}, 150*time.Millisecond)
	if err == nil {
		t.Fatal("every dial failed, so the buyer must be reported unreachable")
	}
	if len(lnd.dialed) < 2 {
		t.Fatalf("one failed dial must not settle it, got %d attempt(s)", len(lnd.dialed))
	}
	if !strings.Contains(err.Error(), "attempt(s)") {
		t.Fatalf("the error should say how hard we tried, got %q", err)
	}
}


// The check informs, it does not decide. Order 109a4760 was bought by a node we
// could not see connected, the accept was refused 75 times with a routing error,
// and the buyer paid in full and took the channel. Reachability turned out not to
// predict fulfilment, so a failed check must never be able to refuse an order -
// only to record what it saw.
func TestMagmaBuyerReachabilityIsAdvisoryNotAGate(t *testing.T) {
	if magmaReachBudget > 30*time.Second {
		t.Fatalf("an advisory check must not spend a poll cycle, budget is %s", magmaReachBudget)
	}
	addr := "abc.onion:9735"
	lnd := &magmaReachLND{dialErrs: map[string]error{addr: errors.New("dial timeout")}}
	s := &MagmaService{lnd: lnd}

	// The unreachable result is reported, and reporting is all it may do: the
	// caller records an event and carries on to build the invoice.
	err := s.dialBuyer(context.Background(), "pub", []string{addr}, 120*time.Millisecond)
	if err == nil {
		t.Fatal("an unreachable buyer must still be reported so it can be recorded")
	}
}
