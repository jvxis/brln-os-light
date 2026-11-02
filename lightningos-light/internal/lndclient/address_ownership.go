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

// IsControlledDefaultWalletAddress is deliberately stronger than IsWalletAddress:
// imported/watch-only visibility is not proof that a transfer stays under the
// node's private-key control. Unknown/unavailable evidence is never exempt.
func (c *Client) IsControlledDefaultWalletAddress(ctx context.Context, address string) (bool, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	client := walletrpc.NewWalletKitClient(conn)
	accounts, err := client.ListAccounts(ctx, &walletrpc.ListAccountsRequest{Name: "default"})
	if err != nil {
		return false, err
	}
	addresses, err := client.ListAddresses(ctx, &walletrpc.ListAddressesRequest{AccountName: "default"})
	if err != nil {
		return false, err
	}
	return controlledDefaultWalletAddress(address, accounts.GetAccounts(), addresses.GetAccountWithAddresses()), nil
}

func controlledDefaultWalletAddress(address string, accounts []*walletrpc.Account, groups []*walletrpc.AccountWithAddresses) bool {
	target := canonicalWalletAddress(address)
	if target == "" {
		return false
	}
	for _, group := range groups {
		if group == nil || group.GetName() != "default" || group.GetDerivationPath() == "" {
			continue
		}
		controlled := false
		for _, account := range accounts {
			if account != nil && account.GetName() == "default" && !account.GetWatchOnly() && account.GetAddressType() == group.GetAddressType() && account.GetDerivationPath() == group.GetDerivationPath() {
				controlled = true
				break
			}
		}
		if !controlled {
			continue
		}
		for _, item := range group.GetAddresses() {
			if item != nil && item.GetDerivationPath() != "" && (len(item.GetPublicKey()) == 32 || len(item.GetPublicKey()) == 33) && canonicalWalletAddress(item.GetAddress()) == target {
				return true
			}
		}
	}
	return false
}

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
