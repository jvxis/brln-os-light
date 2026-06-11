package lndclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"lightningos-light/lnrpc/routerrpc"
)

const (
	KeysendPreimageRecord        uint64 = 5482373484
	KeysendMessageRecord         uint64 = 34349334
	KeysendSenderRecord          uint64 = 34349339
	KeysendSenderSignatureRecord uint64 = 34349340
)

func KeysendIdentityPayload(senderPubkey string, recipientPubkey string, amountSat int64, paymentHash string, message string) string {
	return fmt.Sprintf(
		"brln-chat-v1\nsender=%s\nrecipient=%s\namount_sat=%d\npayment_hash=%s\nmessage=%s",
		strings.ToLower(strings.TrimSpace(senderPubkey)),
		strings.ToLower(strings.TrimSpace(recipientPubkey)),
		amountSat,
		strings.ToLower(strings.TrimSpace(paymentHash)),
		message,
	)
}

func (c *Client) SendKeysendMessage(ctx context.Context, pubkeyHex string, amountSat int64, message string) (string, error) {
	trimmed := strings.TrimSpace(pubkeyHex)
	if trimmed == "" {
		return "", errors.New("pubkey required")
	}
	if amountSat <= 0 {
		return "", errors.New("amount must be positive")
	}
	pubkey, err := hex.DecodeString(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid pubkey hex")
	}
	if len(pubkey) != 33 {
		return "", fmt.Errorf("invalid pubkey length")
	}

	senderRecord := []byte(nil)
	senderPubkey := ""
	if selfPubkey, err := c.SelfPubkey(ctx); err == nil {
		senderPubkey = strings.TrimSpace(selfPubkey)
		if senderPubkey != "" {
			if senderBytes, err := hex.DecodeString(senderPubkey); err == nil && len(senderBytes) == 33 {
				senderRecord = senderBytes
			}
		}
	}

	preimage := make([]byte, 32)
	if _, err := rand.Read(preimage); err != nil {
		return "", err
	}
	hash := sha256.Sum256(preimage)
	paymentHash := hex.EncodeToString(hash[:])

	conn, release, err := c.borrowConn(ctx, grpcRoleAdminStream)
	if err != nil {
		return "", err
	}
	defer release()

	client := routerrpc.NewRouterClient(conn)
	records := map[uint64][]byte{
		KeysendPreimageRecord: preimage,
		KeysendMessageRecord:  []byte(message),
	}
	if len(senderRecord) == 33 {
		records[KeysendSenderRecord] = senderRecord
		if signature, err := c.SignMessage(ctx, KeysendIdentityPayload(senderPubkey, trimmed, amountSat, paymentHash, message)); err == nil {
			records[KeysendSenderSignatureRecord] = []byte(signature)
		}
	}

	stream, err := client.SendPaymentV2(ctx, &routerrpc.SendPaymentRequest{
		Dest:              pubkey,
		Amt:               amountSat,
		PaymentHash:       hash[:],
		DestCustomRecords: records,
		FeeLimitMsat:      defaultRouterPaymentFeeLimitMsatForDecodedInvoice(DecodedInvoice{AmountSat: amountSat}),
		TimeoutSeconds:    paymentTimeoutSeconds(ctx, 90),
		NoInflightUpdates: true,
	})
	if err != nil {
		return "", err
	}
	if _, err := waitForRouterPayment(stream); err != nil {
		return "", err
	}

	return paymentHash, nil
}
