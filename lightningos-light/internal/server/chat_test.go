package server

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSentKeysendNotificationIncludesMessage(t *testing.T) {
	now := time.Unix(1710000000, 0).UTC()
	evt := buildSentKeysendNotification(ChatMessage{
		Timestamp:  now,
		PeerPubkey: "  02abc  ",
		Message:    "  hello   telegram \n keysend  ",
	}, "hash123", func(pubkey string) string {
		if strings.TrimSpace(pubkey) != "02abc" {
			t.Fatalf("unexpected pubkey lookup: %q", pubkey)
		}
		return "resolved alias"
	})

	if evt.Type != "keysend" || evt.Action != "sent" || evt.Direction != "out" {
		t.Fatalf("unexpected event classification: %+v", evt)
	}
	if evt.Memo != "hello   telegram \n keysend" {
		t.Fatalf("expected message in memo, got %q", evt.Memo)
	}
	if evt.PeerAlias != "resolved alias" {
		t.Fatalf("expected resolved alias, got %q", evt.PeerAlias)
	}
	if evt.PaymentHash != "hash123" {
		t.Fatalf("expected payment hash, got %q", evt.PaymentHash)
	}
}

