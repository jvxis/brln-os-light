package lndclient

import (
	"context"
	"encoding/hex"
	"errors"

	"lightningos-light/lnrpc"
)

// LookupTreasuryInvoice returns the amount actually settled on the exact local
// invoice, in millisatoshis. An open invoice has no settlement amount.
func (c *Client) LookupTreasuryInvoice(ctx context.Context, paymentHash string) (int64, error) {
	hash, err := hex.DecodeString(paymentHash)
	if err != nil || len(hash) != 32 {
		return 0, errors.New("invalid invoice hash")
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return 0, err
	}
	defer release()
	invoice, err := lnrpc.NewLightningClient(conn).LookupInvoice(ctx, &lnrpc.PaymentHash{RHash: hash})
	if err != nil || invoice.GetState() != lnrpc.Invoice_SETTLED {
		return 0, err
	}
	return invoice.GetAmtPaidMsat(), nil
}
