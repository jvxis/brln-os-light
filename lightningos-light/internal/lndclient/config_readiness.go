package lndclient

import (
	"context"
	"lightningos-light/lnrpc"
)

// ProbeConfigState bypasses all status/info caches. Configuration apply and
// rollback must verify the newly started daemon, never a stale healthy result.
func (c *Client) ProbeConfigState(ctx context.Context) (string, error) {
	// State is available before a wallet or macaroon exists. Admin RPCs below
	// retain their authenticated connection.
	conn, err := c.dial(ctx, false)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	state, err := lnrpc.NewStateClient(conn).GetState(ctx, &lnrpc.GetStateRequest{})
	if err != nil {
		return "", err
	}
	switch state.State {
	case lnrpc.WalletState_SERVER_ACTIVE:
		admin, err := c.dial(ctx, true)
		if err != nil {
			return "", err
		}
		defer admin.Close()
		_, err = lnrpc.NewLightningClient(admin).GetInfo(ctx, &lnrpc.GetInfoRequest{})
		if err != nil {
			return "", err
		}
		return "active", nil
	case lnrpc.WalletState_LOCKED:
		return "locked", nil
	case lnrpc.WalletState_NON_EXISTING:
		return "missing", nil
	default:
		return "starting", nil
	}
}
