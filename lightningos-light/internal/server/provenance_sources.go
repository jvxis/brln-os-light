package server

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lightningos-light/internal/electrs"
)

const (
	provenanceSourceCallTimeout      = 8 * time.Second
	bitcoinCoreReadinessTTL          = 30 * time.Second
	bitcoinCoreNoTxIndexHintCooldown = 6 * time.Hour
)

// BitcoinCoreSource adapts the local bitcoind getrawtransaction call so the
// provenance ChainedSource can fall back to it when electrs is unreachable.
//
// Readiness is gated on the project's existing fullIndexAppAvailability
// signal — the same check that decides whether to offer Electrs/Mempool
// apps in the store (local Bitcoin Core + non-pruned + txindex synced).
// We cache the result for 30 s so we're not paying the cost on every
// provenance lookup.
//
// When the readiness check reports "requires_txindex" we surface
// NoTxIndexHint() so the UI can show its one-time banner.
type BitcoinCoreSource struct {
	readiness         func(ctx context.Context) (ok bool, reason string)
	mu                sync.Mutex
	configLoaded      bool
	configErr         error
	config            bitcoinRPCConfig
	configResolver    func(ctx context.Context) (bitcoinRPCConfig, error)
	readinessExpiresAt time.Time
	readinessOK       bool
	readinessReason   string
	noTxIndex         atomic.Bool
	noTxIndexAt       atomic.Int64 // unix seconds
}

// NewBitcoinCoreSource builds a source backed by the given readiness check.
// In production wire it to s.fullIndexAppAvailability via a closure so we
// reuse the project's existing readiness signal.
func NewBitcoinCoreSource(readiness func(ctx context.Context) (bool, string)) *BitcoinCoreSource {
	return &BitcoinCoreSource{
		readiness:      readiness,
		configResolver: resolveElementsLocalBitcoinRPCConfig,
	}
}

func (b *BitcoinCoreSource) Name() string { return "bitcoind" }

// NoTxIndexHint returns true if the readiness check has recently reported
// requires_txindex. The UI uses this to surface a one-time hint banner
// suggesting the user enable txindex=1 for fully local provenance. The
// flag resets after 6 h so the banner doesn't reappear forever.
func (b *BitcoinCoreSource) NoTxIndexHint() bool {
	if !b.noTxIndex.Load() {
		return false
	}
	last := b.noTxIndexAt.Load()
	if last == 0 {
		return true
	}
	return time.Now().Unix()-last < int64(bitcoinCoreNoTxIndexHintCooldown.Seconds())
}

func (b *BitcoinCoreSource) GetTransaction(ctx context.Context, txid string) (electrs.VerboseTx, error) {
	if !b.checkReadiness(ctx) {
		return electrs.VerboseTx{}, electrs.ErrSourceUnavailable
	}
	cfg, err := b.loadConfig(ctx)
	if err != nil {
		return electrs.VerboseTx{}, electrs.ErrSourceUnavailable
	}

	callCtx, cancel := context.WithTimeout(ctx, provenanceSourceCallTimeout)
	defer cancel()
	tx, err := fetchBitcoinVerboseTransactionRPC(callCtx, cfg.Host, cfg.User, cfg.Pass, txid)
	if err != nil {
		// Belt-and-suspenders: readiness should have caught a missing
		// txindex, but if Core still answers with the hint for a
		// specific txid, propagate the signal.
		if isBitcoinNoTxIndexError(err) {
			b.markNoTxIndex()
		}
		return electrs.VerboseTx{}, electrs.ErrSourceUnavailable
	}
	return bitcoinVerboseTxToElectrs(tx), nil
}

// checkReadiness consults the cached readiness verdict, refreshing it after
// bitcoinCoreReadinessTTL. Returns false if the source is unavailable; sets
// the NoTxIndexHint when the readiness reason is requires_txindex.
func (b *BitcoinCoreSource) checkReadiness(ctx context.Context) bool {
	b.mu.Lock()
	if time.Now().Before(b.readinessExpiresAt) {
		ok := b.readinessOK
		b.mu.Unlock()
		return ok
	}
	check := b.readiness
	b.mu.Unlock()

	if check == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ok, reason := check(cctx)

	b.mu.Lock()
	b.readinessOK = ok
	b.readinessReason = reason
	b.readinessExpiresAt = time.Now().Add(bitcoinCoreReadinessTTL)
	b.mu.Unlock()

	if !ok && reason == fullIndexUnavailableTxIndex {
		b.markNoTxIndex()
	}
	return ok
}

func (b *BitcoinCoreSource) markNoTxIndex() {
	b.noTxIndex.Store(true)
	b.noTxIndexAt.Store(time.Now().Unix())
}

func (b *BitcoinCoreSource) loadConfig(ctx context.Context) (bitcoinRPCConfig, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.configLoaded {
		return b.config, b.configErr
	}
	cfg, err := b.configResolver(ctx)
	b.config = cfg
	b.configErr = err
	b.configLoaded = true
	return cfg, err
}

// isBitcoinNoTxIndexError checks for Bitcoin Core's "use -txindex" hint.
// fetchBitcoinVerboseTransactionRPC wraps the JSON-RPC error into fmt.Errorf
// with just the message, so we match on substring.
func isBitcoinNoTxIndexError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "txindex")
}

func bitcoinVerboseTxToElectrs(tx bitcoinVerboseTransaction) electrs.VerboseTx {
	out := electrs.VerboseTx{
		Txid: tx.TxID,
		Hash: tx.Hash,
	}
	for _, vin := range tx.Vin {
		if vin.Coinbase != "" {
			continue
		}
		out.Vin = append(out.Vin, electrs.VerboseVin{Txid: vin.TxID, Vout: vin.Vout})
	}
	for _, vout := range tx.Vout {
		v := electrs.VerboseVout{Value: vout.Value, N: vout.N}
		v.ScriptPubKey.Address = vout.ScriptPubKey.Address
		v.ScriptPubKey.Addresses = vout.ScriptPubKey.Addresses
		v.ScriptPubKey.Type = vout.ScriptPubKey.Type
		out.Vout = append(out.Vout, v)
	}
	return out
}

// publicElectrumDefaults is the list of public Electrum servers shipped on
// by default. Both are community mainnet servers; users can override with
// PROVENANCE_PUBLIC_ELECTRUM (comma-separated) or disable with the value
// "disabled" (case-insensitive).
//
// SECURITY note: hitting these servers exposes the txids you ask about,
// which deanonymizes your wallet's ancestor graph to the operator. Disable
// (or replace with your own) if that's not acceptable.
var publicElectrumDefaults = []string{
	"electrum.pagcoin.org:50002:s",
	"electrum.br-ln.com:50001:t",
}

func parsePublicElectrumList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "disabled") || strings.EqualFold(raw, "off") || raw == "-" {
		return nil
	}
	if raw == "" {
		return append([]string(nil), publicElectrumDefaults...)
	}
	out := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// buildProvenanceSourceChain assembles the fallback chain in priority order:
//
//  1. Local electrs (ELECTRUM_RPC_ADDR, default 127.0.0.1:50001)
//  2. Local bitcoind (gated on fullIndexAppAvailability — local Bitcoin
//     Core + non-pruned + txindex synced)
//  3. Public Electrum servers (mainnet only — opt out via
//     PROVENANCE_PUBLIC_ELECTRUM=disabled)
//
// publicAllowed gates step 3; pass false on non-mainnet networks.
func buildProvenanceSourceChain(publicAllowed bool, bitcoindFallback *BitcoinCoreSource) (*electrs.ChainedSource, []string) {
	sources := []electrs.TxSource{}
	notes := []string{}

	local := &electrs.ClientSource{
		Client: electrs.New(""),
		Label:  "local electrs",
	}
	sources = append(sources, local)
	notes = append(notes, "local electrs @ "+local.Client.Addr())

	if bitcoindFallback != nil {
		sources = append(sources, bitcoindFallback)
		notes = append(notes, "local bitcoind (txindex)")
	}

	if publicAllowed {
		for _, addr := range parsePublicElectrumList(os.Getenv("PROVENANCE_PUBLIC_ELECTRUM")) {
			cli := electrs.New(addr)
			sources = append(sources, &electrs.ClientSource{Client: cli, Label: "public:" + cli.Addr()})
			notes = append(notes, "public electrum: "+cli.Addr())
		}
	}

	return electrs.NewChainedSource(sources), notes
}
