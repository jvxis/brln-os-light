package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

func TestChatFileReadStatePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	messagesPath := filepath.Join(dir, "messages.jsonl")
	cursorPath := filepath.Join(dir, "cursor.txt")
	readStatePath := filepath.Join(dir, "read-state.json")
	peerPubkey := "020000000000000000000000000000000000000000000000000000000000000001"
	receivedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)

	store := newChatFileStore(messagesPath, cursorPath, readStatePath)
	if err := store.append(ChatMessage{
		Timestamp:  receivedAt,
		PeerPubkey: peerPubkey,
		Direction:  "in",
		Message:    "hello",
		Status:     "received",
	}); err != nil {
		t.Fatal(err)
	}

	items, err := store.inbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("initial inbox items=%d err=%v", len(items), err)
	}
	if !items[0].LastReadAt.IsZero() {
		t.Fatalf("new conversation unexpectedly read at %s", items[0].LastReadAt)
	}

	readAt, err := store.markRead(peerPubkey)
	if err != nil {
		t.Fatal(err)
	}
	if !readAt.Equal(receivedAt) {
		t.Fatalf("read timestamp=%s want=%s", readAt, receivedAt)
	}

	reloaded := newChatFileStore(messagesPath, cursorPath, readStatePath)
	items, err = reloaded.inbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("reloaded inbox items=%d err=%v", len(items), err)
	}
	if !items[0].LastReadAt.Equal(receivedAt) {
		t.Fatalf("persisted read timestamp=%s want=%s", items[0].LastReadAt, receivedAt)
	}
}

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

func TestResolveChatAmountSat(t *testing.T) {
	if got, err := resolveChatAmountSat(nil); err != nil || got != chatDefaultAmountSat {
		t.Fatalf("expected default amount, got amount=%d err=%v", got, err)
	}
	custom := int64(2500)
	if got, err := resolveChatAmountSat(&custom); err != nil || got != custom {
		t.Fatalf("expected custom amount, got amount=%d err=%v", got, err)
	}
	zero := int64(0)
	if _, err := resolveChatAmountSat(&zero); err == nil {
		t.Fatal("expected zero amount to fail")
	}
}

func TestChatSendErrorCodeIncorrectPaymentDetails(t *testing.T) {
	if got := chatSendErrorCode("incorrect Payment details"); got != "chat_keysend_incorrect_payment_details" {
		t.Fatalf("unexpected code: %s", got)
	}
}

func TestExtractKeysendMessageIncludesSenderSignature(t *testing.T) {
	invoice := &lnrpc.Invoice{
		Htlcs: []*lnrpc.InvoiceHTLC{
			{
				ChanId: 123,
				CustomRecords: map[uint64][]byte{
					lndclient.KeysendMessageRecord:         []byte("hello"),
					lndclient.KeysendSenderRecord:          mustDecodeHexPubkey(t, "020000000000000000000000000000000000000000000000000000000000000001"),
					lndclient.KeysendSenderSignatureRecord: []byte("signature"),
				},
			},
		},
	}
	message, chanID, sender, signature := extractKeysendMessage(invoice)
	if message != "hello" || chanID != 123 {
		t.Fatalf("unexpected message metadata: message=%q chanID=%d", message, chanID)
	}
	if sender != "020000000000000000000000000000000000000000000000000000000000000001" {
		t.Fatalf("unexpected sender: %s", sender)
	}
	if signature != "signature" {
		t.Fatalf("unexpected signature: %s", signature)
	}
}

func mustDecodeHexPubkey(t *testing.T, value string) []byte {
	t.Helper()
	decoded := make([]byte, 33)
	for i := range decoded {
		decoded[i] = 0
	}
	if len(value) != 66 {
		t.Fatalf("test pubkey must be 66 hex chars")
	}
	for i := 0; i < len(decoded); i++ {
		hi := fromHex(t, value[i*2])
		lo := fromHex(t, value[i*2+1])
		decoded[i] = hi<<4 | lo
	}
	return decoded
}

func fromHex(t *testing.T, value byte) byte {
	t.Helper()
	switch {
	case value >= '0' && value <= '9':
		return value - '0'
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10
	default:
		t.Fatalf("invalid hex byte: %q", value)
	}
	return 0
}
