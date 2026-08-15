package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	fullIndexUnavailableBitcoinSource = "requires_local_bitcoin_source"
	fullIndexUnavailableBitcoinApp    = "requires_bitcoin_store"
	fullIndexUnavailableBitcoinRPC    = "requires_bitcoin_rpc"
	fullIndexUnavailableBitcoinSync   = "requires_synced_bitcoin"
	fullIndexUnavailableUnpruned      = "requires_unpruned_bitcoin"
	fullIndexUnavailableTxIndex       = "requires_txindex"
)

type fullIndexAppAvailability struct {
	Available bool
	Reason    string
	Message   string
}

type bitcoinIndexInfo struct {
	Synced bool `json:"synced"`
}

type bitcoinIndexInfoRPCResponse struct {
	Result map[string]bitcoinIndexInfo `json:"result"`
	Error  *rpcErrorDetail             `json:"error"`
}

func (s *Server) fullIndexAppAvailability(ctx context.Context) fullIndexAppAvailability {
	if readBitcoinSource() != "local" {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinSource, "Electrs and Mempool require an active local Bitcoin Core source.")
	}

	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cfg, err := readBitcoinLocalRPCConfig(checkCtx)
	if err != nil {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin RPC config could not be read. Check bitcoin.conf or lnd.conf.")
	}
	return fullIndexBitcoinConfigAvailability(checkCtx, cfg)
}

func (s *Server) electrsAppAvailability(ctx context.Context) fullIndexAppAvailability {
	availability := s.fullIndexAppAvailability(ctx)
	return s.electrsAppAvailabilityFromBase(ctx, availability)
}

func (s *Server) electrsAppAvailabilityFromBase(ctx context.Context, availability fullIndexAppAvailability) fullIndexAppAvailability {
	if availability.Available || availability.Reason != fullIndexUnavailableBitcoinRPC || !fileExists(bitcoinCoreAppPaths().ComposePath) {
		return availability
	}
	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return s.managedBitcoinFullIndexAvailability(checkCtx)
}

func (s *Server) managedBitcoinFullIndexAvailability(ctx context.Context) fullIndexAppAvailability {
	status, err := s.bitcoinLocalStatusCached(ctx)
	if err != nil || !status.Installed || status.Source != "app" || !status.RPCOk {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin RPC is unavailable. Start Bitcoin Core and try again.")
	}
	raw, err := readBitcoinCoreConfigRaw(ctx, bitcoinCoreAppPaths())
	if err != nil {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin configuration could not be validated.")
	}
	return managedBitcoinFullIndexStatusAvailability(status, raw)
}

func managedBitcoinFullIndexStatusAvailability(status bitcoinLocalStatus, rawConfig string) fullIndexAppAvailability {
	if !status.Installed || status.Source != "app" || !status.RPCOk {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin RPC is unavailable. Start Bitcoin Core and try again.")
	}
	if status.Pruned {
		return fullIndexUnavailable(fullIndexUnavailableUnpruned, "Electrs and Mempool require a non-pruned Bitcoin Core node. Disable pruning before installing.")
	}
	info := bitcoinInfo{
		Blocks: status.Blocks, Headers: status.Headers, VerificationProgress: status.VerificationProgress,
		InitialBlockDownload: status.InitialBlockDownload,
	}
	if bitcoinInfoStillSyncing(info) {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinSync, "Electrs and Mempool require Bitcoin Core to be fully synced.")
	}
	if !parseBitcoinCoreBool(rawConfig, "txindex") {
		return fullIndexUnavailable(fullIndexUnavailableTxIndex, "Electrs and Mempool require txindex=1 before installing.")
	}
	return fullIndexAppAvailability{Available: true}
}

func fullIndexBitcoinConfigAvailability(ctx context.Context, cfg bitcoinRPCConfig) fullIndexAppAvailability {
	if !isLocalRPCHost(cfg.Host) {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinSource, fmt.Sprintf("Local Bitcoin RPC host is not local: %s", cfg.Host))
	}
	if strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Pass) == "" {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin RPC credentials are missing.")
	}
	info, err := fetchBitcoinInfo(ctx, cfg.Host, cfg.User, cfg.Pass)
	if err != nil {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Local Bitcoin RPC is unavailable. Start Bitcoin Core and try again.")
	}
	if info.Pruned {
		return fullIndexUnavailable(fullIndexUnavailableUnpruned, "Electrs and Mempool require a non-pruned Bitcoin Core node. Disable pruning before installing.")
	}
	if bitcoinInfoStillSyncing(info) {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinSync, "Electrs and Mempool require Bitcoin Core to be fully synced.")
	}
	txIndexReady, txIndexKnown, err := bitcoinRPCConfigTxIndexReady(ctx, cfg)
	if err != nil || !txIndexKnown || !txIndexReady {
		return fullIndexUnavailable(fullIndexUnavailableTxIndex, "Electrs and Mempool require txindex=1 before installing.")
	}

	return fullIndexAppAvailability{Available: true}
}

func fullIndexUnavailable(reason, message string) fullIndexAppAvailability {
	return fullIndexAppAvailability{
		Available: false,
		Reason:    reason,
		Message:   message,
	}
}

func (s *Server) requireFullIndexApps(ctx context.Context) error {
	availability := s.fullIndexAppAvailability(ctx)
	if availability.Available {
		return nil
	}
	return errors.New(availability.Message)
}

func (s *Server) decorateFullIndexAppInfo(ctx context.Context, info *appInfo) {
	if info == nil {
		return
	}
	if info.ID != electrsAppID && info.ID != mempoolAppID {
		return
	}
	availability := s.fullIndexAppAvailability(ctx)
	if info.ID == electrsAppID {
		availability = s.electrsAppAvailability(ctx)
	}
	info.Available = availability.Available
	info.UnavailableReason = availability.Reason
	info.UnavailableMessage = availability.Message
}

func parseBitcoinCoreTxIndexInfo(raw string) (bool, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, false, nil
	}
	payload := bitcoinIndexInfoRPCResponse{}
	if err := json.Unmarshal([]byte(trimmed), &payload); err == nil && (payload.Result != nil || payload.Error != nil) {
		if payload.Error != nil {
			return false, true, errors.New(payload.Error.Message)
		}
		tx, ok := payload.Result["txindex"]
		return ok && tx.Synced, ok, nil
	}

	result := map[string]bitcoinIndexInfo{}
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		return false, true, err
	}
	tx, ok := result["txindex"]
	return ok && tx.Synced, ok, nil
}

func parseBitcoinCoreBool(raw string, key string) bool {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != key {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(parts[1]))
		if value == "1" || value == "true" || value == "yes" || value == "on" {
			return true
		}
		if n, err := strconv.Atoi(value); err == nil {
			return n != 0
		}
	}
	return false
}
