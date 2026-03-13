package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

type bitcoinBlockHashRPCResponse struct {
	Result string          `json:"result"`
	Error  *rpcErrorDetail `json:"error"`
}

func resolveBitcoinRPCConfigForClosedChannels(ctx context.Context) (bitcoinRPCConfig, error) {
	if cfg, _, err := readBitcoinLocalRPCConfig(ctx); err == nil {
		if strings.TrimSpace(cfg.Host) != "" && strings.TrimSpace(cfg.User) != "" && strings.TrimSpace(cfg.Pass) != "" {
			return cfg, nil
		}
	}
	if cfg, ok := readBitcoinTaggedRPCConfigFromLNDConf("remote"); ok {
		return cfg, nil
	}
	if cfg, ok := readBitcoindRPCConfigFromLNDConf(); ok {
		return cfg, nil
	}
	return bitcoinRPCConfig{}, errors.New("bitcoin rpc unavailable")
}

func fetchBitcoinBlockHashRPC(ctx context.Context, host, user, pass string, height uint32) (string, error) {
	body, err := fetchBitcoinRPCParams(ctx, host, user, pass, "getblockhash", []any{height})
	if err != nil {
		return "", err
	}
	var payload bitcoinBlockHashRPCResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if payload.Error != nil {
		return "", errors.New(payload.Error.Message)
	}
	return strings.TrimSpace(payload.Result), nil
}

func enrichClosedChannelTimes(ctx context.Context, items []lndclient.ClosedChannelInfo) {
	if len(items) == 0 {
		return
	}

	cfg, err := resolveBitcoinRPCConfigForClosedChannels(ctx)
	if err != nil {
		return
	}

	blockTimeCache := make(map[uint32]string)
	for i := range items {
		if strings.TrimSpace(items[i].ClosedAt) != "" || items[i].CloseHeight == 0 {
			continue
		}
		if cached, ok := blockTimeCache[items[i].CloseHeight]; ok {
			items[i].ClosedAt = cached
			continue
		}

		hash, hashErr := fetchBitcoinBlockHashRPC(ctx, cfg.Host, cfg.User, cfg.Pass, items[i].CloseHeight)
		if hashErr != nil || strings.TrimSpace(hash) == "" {
			continue
		}
		header, headerErr := fetchBitcoinBlockHeaderRPC(ctx, cfg.Host, cfg.User, cfg.Pass, hash)
		if headerErr != nil || header.Time <= 0 {
			continue
		}

		closedAt := time.Unix(header.Time, 0).UTC().Format(time.RFC3339)
		blockTimeCache[items[i].CloseHeight] = closedAt
		items[i].ClosedAt = closedAt
	}
}
