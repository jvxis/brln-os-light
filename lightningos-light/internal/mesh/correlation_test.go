package mesh

import (
	"bytes"
	"testing"
	"time"
)

func TestCorrelatedReplyAuthenticationAndBoundaries(t *testing.T) {
	request, _ := Fragment(1, 2, PaymentRequest, []byte("request"), time.Now())
	raw, err := EncodeReply(Invoice, request[0], []byte("invoice"))
	if err != nil {
		t.Fatal(err)
	}
	kind, session, hash, content, err := DecodeReply(raw)
	if err != nil || kind != Invoice || session != request[0].Session || hash != request[0].Hash || string(content) != "invoice" {
		t.Fatal("reply identity lost")
	}
	packets, err := Fragment(2, 1, CorrelatedReply, raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{8}, 32)
	sealed, err := packets[0].Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Open(sealed, key, 2, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = Open(sealed, key, 2, 1, time.Now()); err == nil {
		t.Fatal("tampered correlation accepted")
	}
	if _, err = EncodeReply(Invoice, request[0], make([]byte, MaxContent-ReplyHeader+1)); err == nil {
		t.Fatal("oversized reply accepted")
	}
	if _, _, _, _, err = DecodeReply(raw[:ReplyHeader]); err == nil {
		t.Fatal("empty reply accepted")
	}
	// Old data kinds retain their original encoding.
	legacy, _ := Fragment(1, 2, Invoice, []byte("legacy"), time.Now())
	if legacy[0].Kind != Invoice || string(legacy[0].Payload) != "legacy" {
		t.Fatal("legacy payload changed")
	}
}
