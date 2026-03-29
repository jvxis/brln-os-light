package lndclient

import (
	"context"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"

	"lightningos-light/lnrpc/walletrpc"
)

const walletAddressCacheTTL = 1 * time.Minute

func (c *Client) IsWalletAddress(ctx context.Context, address string) (bool, error) {
	target := canonicalWalletAddress(address)
	if target == "" {
		return false, nil
	}

	addresses, err := c.walletAddressSet(ctx)
	if err != nil {
		return false, err
	}
	_, ok := addresses[target]
	return ok, nil
}

func (c *Client) walletAddressSet(ctx context.Context) (map[string]struct{}, error) {
	now := time.Now()

	c.walletAddressesMu.Lock()
	cached := c.walletAddresses
	expiresAt := c.walletAddressesAt
	c.walletAddressesMu.Unlock()

	if cached != nil && now.Before(expiresAt) {
		return cached, nil
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	resp, err := client.ListAddresses(ctx, &walletrpc.ListAddressesRequest{
		ShowCustomAccounts: true,
	})
	if err != nil {
		return nil, err
	}

	addresses := make(map[string]struct{})
	for _, account := range resp.GetAccountWithAddresses() {
		if account == nil {
			continue
		}
		for _, item := range account.GetAddresses() {
			if item == nil {
				continue
			}
			address := canonicalWalletAddress(item.GetAddress())
			if address == "" {
				continue
			}
			addresses[address] = struct{}{}
		}
	}

	c.walletAddressesMu.Lock()
	c.walletAddresses = addresses
	c.walletAddressesAt = now.Add(walletAddressCacheTTL)
	c.walletAddressesMu.Unlock()

	return addresses, nil
}

func canonicalWalletAddress(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	networks := []*chaincfg.Params{
		&chaincfg.MainNetParams,
		&chaincfg.TestNet3Params,
		&chaincfg.RegressionNetParams,
		&chaincfg.SigNetParams,
	}
	for _, network := range networks {
		decoded, err := btcutil.DecodeAddress(trimmed, network)
		if err != nil || decoded == nil || !decoded.IsForNet(network) {
			continue
		}
		return decoded.EncodeAddress()
	}
	return trimmed
}
