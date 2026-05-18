package electrs

import (
	"context"
	"errors"
	"sync"
	"time"
)

// TxSource is a verbose-tx-by-txid fetcher. Implementations include the
// Electrum client in this package and a Bitcoin Core RPC adapter.
//
// Available reports whether the source is reachable WITHOUT requiring a
// real transaction to look up. The health endpoint uses this to decide
// whether to show the Wallet Flow tab. Implementations should treat
// network failures, missing config, and readiness failures as `false`.
type TxSource interface {
	Name() string
	Available(ctx context.Context) bool
	GetTransaction(ctx context.Context, txid string) (VerboseTx, error)
}

// ChainedSource tries members in order, remembering the last source that
// answered for stickyTTL so we don't pay probe latency on every call. A
// source that returns ErrSourceUnavailable is demoted for unavailableTTL.
//
// Concrete errors from a source (e.g. "tx not found") are returned to the
// caller without falling through — those are answers, not failures.
type ChainedSource struct {
	sources         []TxSource
	stickyTTL       time.Duration
	unavailableTTL  time.Duration
	mu              sync.Mutex
	lastGood        TxSource
	lastGoodAt      time.Time
	unavailableTill map[string]time.Time
}

// ErrSourceUnavailable signals the chain should fall through to the next
// source (vs. a real "tx not found" answer, which the caller wants).
var ErrSourceUnavailable = errors.New("electrs source unavailable")

// NewChainedSource builds a chain with the given members. Empty input is
// allowed; GetTransaction will return ErrSourceUnavailable in that case.
func NewChainedSource(sources []TxSource) *ChainedSource {
	return &ChainedSource{
		sources:         sources,
		stickyTTL:       5 * time.Minute,
		unavailableTTL:  60 * time.Second,
		unavailableTill: make(map[string]time.Time),
	}
}

func (c *ChainedSource) Name() string { return "chain" }

// Available reports true when any member of the chain is reachable. Used
// by callers that want a single yes/no — the health endpoint queries per
// source instead.
func (c *ChainedSource) Available(ctx context.Context) bool {
	for _, s := range c.sources {
		if s.Available(ctx) {
			return true
		}
	}
	return false
}

// Sources returns the configured sources in order. Useful for status/UI.
func (c *ChainedSource) Sources() []TxSource { return c.sources }

// LastGood reports which source most recently answered, for badge UI.
// Returns nil if no source has answered yet (or the sticky window expired).
func (c *ChainedSource) LastGood() TxSource {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastGood == nil || time.Since(c.lastGoodAt) > c.stickyTTL {
		return nil
	}
	return c.lastGood
}

func (c *ChainedSource) GetTransaction(ctx context.Context, txid string) (VerboseTx, error) {
	order := c.tryOrder()
	var lastErr error
	for _, src := range order {
		if c.skipUnavailable(src.Name()) {
			continue
		}
		tx, err := src.GetTransaction(ctx, txid)
		if err == nil {
			c.recordGood(src)
			return tx, nil
		}
		if errors.Is(err, ErrSourceUnavailable) {
			c.markUnavailable(src.Name())
			lastErr = err
			continue
		}
		// Concrete error (tx not found, bad txid, etc.) — that's an answer.
		return VerboseTx{}, err
	}
	if lastErr == nil {
		lastErr = ErrSourceUnavailable
	}
	return VerboseTx{}, lastErr
}

// tryOrder returns sources with the last-good one first (if still sticky).
func (c *ChainedSource) tryOrder() []TxSource {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastGood == nil || time.Since(c.lastGoodAt) > c.stickyTTL {
		return append([]TxSource(nil), c.sources...)
	}
	out := make([]TxSource, 0, len(c.sources))
	out = append(out, c.lastGood)
	for _, s := range c.sources {
		if s != c.lastGood {
			out = append(out, s)
		}
	}
	return out
}

func (c *ChainedSource) skipUnavailable(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	till, ok := c.unavailableTill[name]
	if !ok {
		return false
	}
	if time.Now().After(till) {
		delete(c.unavailableTill, name)
		return false
	}
	return true
}

func (c *ChainedSource) markUnavailable(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unavailableTill[name] = time.Now().Add(c.unavailableTTL)
}

func (c *ChainedSource) recordGood(src TxSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastGood = src
	c.lastGoodAt = time.Now()
}

// ClientSource is a small wrapper around *Client implementing TxSource.
// It translates network-level failures to ErrSourceUnavailable so the
// chain can fall through.
type ClientSource struct {
	Client *Client
	Label  string // human label for status UI (e.g. "local electrs", "public:host")
}

func (s *ClientSource) Name() string {
	if s.Label != "" {
		return s.Label
	}
	if s.Client != nil {
		return "electrs:" + s.Client.Addr()
	}
	return "electrs"
}

func (s *ClientSource) Available(ctx context.Context) bool {
	if s.Client == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, err := s.Client.Ping(cctx)
	return err == nil
}

func (s *ClientSource) GetTransaction(ctx context.Context, txid string) (VerboseTx, error) {
	if s.Client == nil {
		return VerboseTx{}, ErrSourceUnavailable
	}
	tx, err := s.Client.GetTransaction(ctx, txid)
	if err == nil {
		return tx, nil
	}
	// All electrs failures (dial, write, read, decode) bubble up as opaque
	// errors. Treat them as unavailable so the chain falls through; a real
	// "tx not found" response from electrs would come back as an empty
	// VerboseTx with no error, which we don't currently distinguish.
	return VerboseTx{}, ErrSourceUnavailable
}
