package lndclient

import (
	"context"
	"errors"
	"strings"

	"lightningos-light/lnrpc"
)

func (c *Client) SignMessage(ctx context.Context, message string) (string, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "", errors.New("message required")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.SignMessage(ctx, &lnrpc.SignMessageRequest{
		Msg: []byte(msg),
	})
	if err != nil {
		return "", err
	}

	signature := strings.TrimSpace(resp.GetSignature())
	if signature == "" {
		return "", errors.New("empty signature")
	}
	return signature, nil
}

func (c *Client) VerifyMessage(ctx context.Context, message string, signature string) (string, bool, error) {
	msg := strings.TrimSpace(message)
	sig := strings.TrimSpace(signature)
	if msg == "" {
		return "", false, errors.New("message required")
	}
	if sig == "" {
		return "", false, errors.New("signature required")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.VerifyMessage(ctx, &lnrpc.VerifyMessageRequest{
		Msg:       []byte(msg),
		Signature: sig,
	})
	if err != nil {
		return "", false, err
	}

	return strings.TrimSpace(resp.GetPubkey()), resp.GetValid(), nil
}
