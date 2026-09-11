package lndclient

import (
	"context"
	"errors"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/routerrpc"
)

// MeshMainnetReady reads the live chain identity before relay publication.
func (c *Client) MeshMainnetReady(ctx context.Context) error {
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return err
	}
	defer release()
	info, err := lnrpc.NewLightningClient(conn).GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		return err
	}
	if !info.SyncedToChain || len(info.Chains) != 1 || info.Chains[0].Chain != "bitcoin" || info.Chains[0].Network != "mainnet" {
		return errors.New("synced Bitcoin mainnet wallet required")
	}
	return nil
}

// PayMeshInvoice treats zero as a strict zero-fee limit. PayInvoice's ordinary
// wallet default for zero is deliberately not used for an explicit radio review.
func (c *Client) PayMeshInvoice(ctx context.Context, invoice string, maxFeeSat int64) error {
	if maxFeeSat < 0 || maxFeeSat > 100000 {
		return errors.New("invalid mesh payment fee limit")
	}
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminStream)
	if err != nil {
		return err
	}
	defer release()
	stream, err := routerrpc.NewRouterClient(conn).SendPaymentV2(ctx, &routerrpc.SendPaymentRequest{
		PaymentRequest: invoice, FeeLimitMsat: maxFeeSat * 1000,
		TimeoutSeconds: paymentTimeoutSeconds(ctx, 90), NoInflightUpdates: true, MaxParts: 3,
	})
	if err != nil {
		return err
	}
	_, err = waitForRouterPayment(stream)
	return err
}
