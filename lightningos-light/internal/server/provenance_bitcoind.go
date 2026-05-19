package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Server) provenanceBitcoinCoreAvailability(ctx context.Context) fullIndexAppAvailability {
	if readBitcoinSource() != "local" {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinSource, "Wallet Flow requires an active local Bitcoin Core source for direct bitcoind provenance.")
	}

	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cfg, _, err := readBitcoinLocalRPCConfig(checkCtx)
	if err != nil {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin RPC config could not be read. Check lnd.conf or bitcoin.conf.")
	}
	return provenanceBitcoinCoreConfigAvailability(checkCtx, cfg)
}

func provenanceBitcoinCoreConfigAvailability(ctx context.Context, cfg bitcoinRPCConfig) fullIndexAppAvailability {
	if !isLocalRPCHost(cfg.Host) {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinSource, fmt.Sprintf("Local Bitcoin RPC host is not local: %s", cfg.Host))
	}
	if strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Pass) == "" {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin RPC credentials are missing.")
	}

	info, err := fetchBitcoinInfo(ctx, cfg.Host, cfg.User, cfg.Pass)
	if err != nil {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin RPC is unavailable.")
	}
	if info.Pruned {
		return fullIndexUnavailable(fullIndexUnavailableUnpruned, "Wallet Flow requires a non-pruned local Bitcoin Core node.")
	}
	if bitcoinInfoStillSyncing(info) {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinSync, "Wallet Flow requires local Bitcoin Core to be fully synced.")
	}

	txIndexReady, txIndexKnown, err := bitcoinRPCConfigTxIndexReady(ctx, cfg)
	if err != nil || !txIndexKnown || !txIndexReady {
		return fullIndexUnavailable(fullIndexUnavailableTxIndex, "Wallet Flow requires txindex=1 and a synced txindex.")
	}
	return fullIndexAppAvailability{Available: true}
}

func bitcoinInfoStillSyncing(info bitcoinInfo) bool {
	if info.InitialBlockDownload {
		return true
	}
	if info.VerificationProgress > 0 && info.VerificationProgress < 0.9999 {
		return true
	}
	return info.Headers > 0 && info.Blocks < info.Headers
}

func bitcoinRPCConfigTxIndexReady(ctx context.Context, cfg bitcoinRPCConfig) (bool, bool, error) {
	body, err := fetchBitcoinRPCParams(ctx, cfg.Host, cfg.User, cfg.Pass, "getindexinfo", []any{"txindex"})
	if err != nil {
		return false, true, err
	}
	return parseBitcoinCoreTxIndexInfo(string(body))
}
