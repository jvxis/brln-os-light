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
		return fullIndexUnavailable(fullIndexUnavailableBitcoinSource, "Electrs and Mempool require Bitcoin Core from the App Store as the active Bitcoin source.")
	}

	paths := bitcoinCoreAppPaths()
	if !fileExists(paths.ComposePath) {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinApp, "Install Bitcoin Core from the App Store before installing Electrs or Mempool.")
	}

	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	raw, err := readBitcoinCoreConfig(checkCtx, paths)
	if err != nil {
		return fullIndexUnavailable(fullIndexUnavailableBitcoinRPC, "Bitcoin Core config could not be read. Start Bitcoin Core and try again.")
	}
	if pruned, _ := parseBitcoinCorePrune(raw); pruned {
		return fullIndexUnavailable(fullIndexUnavailableUnpruned, "Electrs and Mempool require a non-pruned Bitcoin Core node. Disable pruning before installing.")
	}

	if chainInfo, err := fetchBitcoinLocalChainInfo(checkCtx, paths); err == nil && chainInfo.Pruned {
		return fullIndexUnavailable(fullIndexUnavailableUnpruned, "Electrs and Mempool require a non-pruned Bitcoin Core node. Disable pruning before installing.")
	}

	txIndexReady, txIndexKnown, err := bitcoinCoreTxIndexReady(checkCtx, paths, raw)
	if err != nil {
		return fullIndexUnavailable(fullIndexUnavailableTxIndex, "Electrs and Mempool require txindex=1 before installing.")
	}
	if !txIndexKnown || !txIndexReady {
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
	info.Available = availability.Available
	info.UnavailableReason = availability.Reason
	info.UnavailableMessage = availability.Message
}

func bitcoinCoreTxIndexReady(ctx context.Context, paths bitcoinCorePaths, rawConf string) (bool, bool, error) {
	out, err := execBitcoinCLI(ctx, paths, "getindexinfo", "txindex")
	if err == nil {
		ready, known, parseErr := parseBitcoinCoreTxIndexInfo(out)
		if parseErr != nil {
			return false, true, parseErr
		}
		return ready, known, nil
	}

	if !parseBitcoinCoreBool(rawConf, "txindex") {
		return false, true, nil
	}
	return false, true, fmt.Errorf("txindex is configured but current index status is unavailable: %w", err)
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
