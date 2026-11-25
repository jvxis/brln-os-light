package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

type treasuryBridgeRequest struct {
	Action      string `json:"action"`
	APIKey      string `json:"api_key"`
	Direction   string `json:"direction,omitempty"`
	Recipient   string `json:"recipient,omitempty"`
	Refund      string `json:"refund,omitempty"`
	Amount      int64  `json:"amount,omitempty"`
	BridgeID    string `json:"bridge_id,omitempty"`
	StatusToken string `json:"status_token,omitempty"`
}

type treasuryProviderFee struct {
	Amount   int64  `json:"amount"`
	Asset    string `json:"asset"`
	Decimals uint32 `json:"decimals"`
}

type treasuryBridgeReply struct {
	BridgeID    string               `json:"bridge_id"`
	StatusToken string               `json:"status_token"`
	Invoice     string               `json:"invoice"`
	Deposit     string               `json:"deposit"`
	AmountIn    int64                `json:"amount_in"`
	AmountOut   int64                `json:"amount_out"`
	ExpiresAt   int64                `json:"expires_at"`
	Status      string               `json:"status"`
	OrderID     string               `json:"order_id"`
	DepositRef  string               `json:"deposit_ref"`
	DeliveryRef string               `json:"delivery_ref"`
	RefundRef   string               `json:"refund_ref"`
	ProviderFee *treasuryProviderFee `json:"provider_fee"`
}

func treasuryBridgeCall(ctx context.Context, request treasuryBridgeRequest) (treasuryBridgeReply, error) {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", treasuryBridgeSocket)
	if err != nil {
		return treasuryBridgeReply{}, errors.New("isolated Breez bridge unreachable")
	}
	defer conn.Close()
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()
	deadline := time.Now().Add(30 * time.Second)
	if target, ok := ctx.Deadline(); ok && target.Before(deadline) {
		deadline = target
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return treasuryBridgeReply{}, errors.New("could not set Breez bridge deadline")
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return treasuryBridgeReply{}, errors.New("could not send Breez bridge request")
	}
	unix, ok := conn.(*net.UnixConn)
	if !ok || unix.CloseWrite() != nil {
		return treasuryBridgeReply{}, errors.New("could not finish Breez bridge request")
	}
	d := json.NewDecoder(io.LimitReader(conn, 32<<10))
	d.DisallowUnknownFields()
	var reply treasuryBridgeReply
	if d.Decode(&reply) != nil || d.Decode(new(any)) != io.EOF {
		return treasuryBridgeReply{}, errors.New("Breez rejected the request or is unavailable")
	}
	return reply, nil
}
