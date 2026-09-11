package mesh

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"
)

func TestInvoicePKCBudget(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	packets, err := Fragment(1, 2, Invoice, bytes.Repeat([]byte("invoice"), 60), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var a Assembly
	for _, p := range packets {
		sealed, err := p.Seal(key)
		if err != nil {
			t.Fatal(err)
		}
		data := blob(varint(nil, 1, PrivatePort), 2, sealed)
		if n := len(data) + 2 + 12 + 16; n > 255 {
			t.Fatalf("PKC frame exceeds PHY: %d", n)
		}
		if _, err := EncodeRadio(RadioPacket{To: 2, Payload: sealed}, 1); err != nil {
			t.Fatal(err)
		}
		opened, err := Open(sealed, key, 1, 2, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		out, err := a.Add(opened)
		if err != nil {
			t.Fatal(err)
		}
		if p.Index == p.Total-1 && !bytes.Equal(out, bytes.Repeat([]byte("invoice"), 60)) {
			t.Fatal("invoice not reassembled")
		}
	}
	// Regression: old 120-byte fragment fits Data.payload but exceeds encrypted PHY.
	if _, err := EncodeRadio(RadioPacket{To: 2, Payload: make([]byte, 228)}, 1); err == nil {
		t.Fatal("oversize legacy send accepted")
	}
}

func TestLegacyReceive(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	content := bytes.Repeat([]byte{4}, 240)
	var a Assembly
	for i := 0; i < 2; i++ {
		p := Packet{Kind: Invoice, From: 1, To: 2, Session: [16]byte{1}, Index: uint16(i), Total: 2, Size: 240, Hash: sha256.Sum256(content), Expires: time.Now().Add(time.Minute).Unix(), Payload: content[i*120 : (i+1)*120]}
		raw, err := p.Seal(key)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := Open(raw, key, 1, 2, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		result, err := a.Add(opened)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && !bytes.Equal(result, content) {
			t.Fatal("legacy mismatch")
		}
	}
}

func TestRoutingError(t *testing.T) {
	data := fixed(blob(varint(nil, 1, 5), 2, varint(nil, 3, 7)), 6, 123)
	raw := blob(nil, 2, blob(nil, 4, data))
	id, code, err := decodeRouting(raw)
	if err != nil || id != 123 || code != 7 {
		t.Fatalf("routing: %d %d %v", id, code, err)
	}
	id, _, _ = decodeRouting(blob(nil, 2, blob(nil, 4, fixed(blob(varint(nil, 1, 256), 2, varint(nil, 3, 7)), 6, 123))))
	if id != 0 {
		t.Fatal("application payload treated as routing")
	}
}
