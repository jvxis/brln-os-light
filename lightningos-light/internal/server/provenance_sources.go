package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lightningos-light/internal/electrs"
)

const (
	bitcoinCoreNoTxIndexErrorCode    = -5
	provenanceSourceCallTimeout      = 8 * time.Second
	bitcoinCoreNoTxIndexHintCooldown = 6 * time.Hour
)

// BitcoinCoreSource adapts the local bitcoind getrawtransaction call so the
// provenance ChainedSource can fall back to it when electrs is unreachable.
//
// Lifecycle:
//   - The first call lazily resolves the local RPC config; if no config is
//     available we mark this source unavailable for the rest of the process.
//   - When Bitcoin Core returns error -5 ("Use -txindex..."), we record the
//     hint (NoTxIndexHint reports it once for the UI banner) and demote
//     ourselves to ErrSourceUnavailable so the chain keeps trying.
type BitcoinCoreSource struct {
	mu             sync.Mutex
	configLoaded   bool
	configErr      error
	config         bitcoinRPCConfig
	noTxIndex      atomic.Bool
	noTxIndexAt    atomic.Int64 // unix seconds
	configResolver func(ctx context.Context) (bitcoinRPCConfig, error)
}

func NewBitcoinCoreSource() *BitcoinCoreSource {
	return &BitcoinCoreSource{
		configResolver: resolveElementsLocalBitcoinRPCConfig,
	}
}

func (b *BitcoinCoreSource) Name() string { return "bitcoind" }

// NoTxIndexHint returns true if Bitcoin Core has reported -5 / "txindex"
// to us in the last 6 hours. The UI uses this to surface a one-time hint
// banner suggesting the user enable txindex=1 for fully local provenance.
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
	if b.noTxIndex.Load() {
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
		if isBitcoinNoTxIndexError(err) {
			b.noTxIndex.Store(true)
			b.noTxIndexAt.Store(time.Now().Unix())
			return electrs.VerboseTx{}, electrs.ErrSourceUnavailable
		}
		return electrs.VerboseTx{}, electrs.ErrSourceUnavailable
	}
	return bitcoinVerboseTxToElectrs(tx), nil
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
// with just the message, so we match on substring. Code -5 alone isn't
// definitive (it's also returned for invalid txids), so we look for the
// "txindex" hint specifically.
func isBitcoinNoTxIndexError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "txindex")
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
//  2. Local bitcoind (auto when reachable; demotes itself on txindex absence)
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

// rpcErrorJSON gives us a typed view of the bitcoind RPC error envelope so
// callers outside graph_close_classifier_bitcoin.go can use the code. Kept
// internal to this file.
//
// (Currently unused by the source — message-substring is enough — but kept
// in case we add stricter checks later.)
type rpcErrorJSON struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

var _ = errors.Is // keep imports tidy if file shrinks
