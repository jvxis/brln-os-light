package lndclient

import (
	"context"

	"lightningos-light/lnrpc"
)

// ListAllTransactions returns the wallet's on-chain transactions in
// [startHeight, endHeight]. Pass endHeight = -1 for "up to current tip".
// We expose the raw proto so the provenance service can read inputs+outputs
// without losing per-vout details.
func (c *Client) ListAllTransactions(ctx context.Context, startHeight, endHeight int32) ([]*lnrpc.Transaction, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.GetTransactions(ctx, &lnrpc.GetTransactionsRequest{
		StartHeight: startHeight,
		EndHeight:   endHeight,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetTransactions(), nil
}
