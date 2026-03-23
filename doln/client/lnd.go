package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"gopkg.in/macaroon.v2"
)

const (
	tlvKeysendPreimage = 5482373484
	tlvDNSPayload      = 65536
	tlvReturnPubkey    = 65537
	tlvRequestID       = 65538
)

type lndClient struct {
	conn      *grpc.ClientConn
	lightning lnrpc.LightningClient
	router    routerrpc.RouterClient
	macaroon  string
}

func newLNDClient(host, certPath, macPath string) (*lndClient, error) {
	creds, err := credentials.NewClientTLSFromFile(certPath, "")
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS cert: %w", err)
	}

	conn, err := grpc.Dial(host, grpc.WithTransportCredentials(creds), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1024*1024*50)))
	if err != nil {
		return nil, fmt.Errorf("failed to dial LND: %w", err)
	}

	macBytes, err := os.ReadFile(macPath)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read macaroon: %w", err)
	}
	mac := &macaroon.Macaroon{}
	if err := mac.UnmarshalBinary(macBytes); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to unmarshal macaroon: %w", err)
	}

	return &lndClient{
		conn:      conn,
		lightning: lnrpc.NewLightningClient(conn),
		router:    routerrpc.NewRouterClient(conn),
		macaroon:  hex.EncodeToString(macBytes),
	}, nil
}

func (c *lndClient) close() {
	c.conn.Close()
}

func (c *lndClient) ctx() context.Context {
	md := metadata.Pairs("macaroon", c.macaroon)
	return metadata.NewOutgoingContext(context.Background(), md)
}

func (c *lndClient) getOwnPubkey() (string, error) {
	info, err := c.lightning.GetInfo(c.ctx(), &lnrpc.GetInfoRequest{})
	if err != nil {
		return "", fmt.Errorf("GetInfo failed: %w", err)
	}
	return info.IdentityPubkey, nil
}

func (c *lndClient) subscribeInvoices(settleIndex uint64) (lnrpc.Lightning_SubscribeInvoicesClient, error) {
	return c.lightning.SubscribeInvoices(c.ctx(), &lnrpc.InvoiceSubscription{
		SettleIndex: settleIndex,
	})
}

func (c *lndClient) sendKeysend(dest []byte, amtSat int64, customRecords map[uint64][]byte) error {
	preimage := make([]byte, 32)
	if _, err := rand.Read(preimage); err != nil {
		return fmt.Errorf("failed to generate preimage: %w", err)
	}

	records := map[uint64][]byte{
		tlvKeysendPreimage: preimage,
	}
	for k, v := range customRecords {
		records[k] = v
	}

	paymentHash := sha256.Sum256(preimage)

	req := &routerrpc.SendPaymentRequest{
		Dest:              dest,
		Amt:               amtSat,
		PaymentHash:       paymentHash[:],
		DestCustomRecords: records,
		TimeoutSeconds:    60,
		FeeLimitSat:       amtSat * 10,
		MaxParts:          1,
	}

	stream, err := c.router.SendPaymentV2(c.ctx(), req)
	if err != nil {
		return fmt.Errorf("failed to send keysend: %w", err)
	}

	for {
		payment, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("payment stream error: %w", err)
		}
		switch payment.Status {
		case lnrpc.Payment_SUCCEEDED:
			return nil
		case lnrpc.Payment_FAILED:
			return fmt.Errorf("payment failed: %s", payment.FailureReason)
		}
	}
}
