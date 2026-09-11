package mesh

import (
	"bytes"
	"testing"
	"time"
)

func exchanges(t *testing.T) (*PairExchange, *PairExchange) {
	t.Helper()
	id := [16]byte{1}
	expires := time.Now().Add(time.Minute).Unix()
	a, e := NewPairExchange(1, 2, id, expires, true)
	if e != nil {
		t.Fatal(e)
	}
	b, e := NewPairExchange(2, 1, id, expires, false)
	if e != nil {
		t.Fatal(e)
	}
	return a, b
}
func TestPairRequiresBothOperators(t *testing.T) {
	a, b := exchanges(t)
	if _, e := a.Reveal(); e == nil {
		t.Fatal("revealed before commitment")
	}
	if e := a.ReceiveCommitment(b.Commitment()); e != nil {
		t.Fatal(e)
	}
	if e := b.ReceiveCommitment(a.Commitment()); e != nil {
		t.Fatal(e)
	}
	x, _ := a.Reveal()
	y, _ := b.Reveal()
	if e := a.ReceiveReveal(y); e != nil {
		t.Fatal(e)
	}
	if e := b.ReceiveReveal(x); e != nil {
		t.Fatal(e)
	}
	if a.SAS() == "" || a.SAS() != b.SAS() {
		t.Fatal("comparison codes differ")
	}
	if a.VerifiedKey() != nil || b.VerifiedKey() != nil {
		t.Fatal("trusted before approval")
	}
	c, _ := a.Confirm()
	if e := b.ReceiveConfirmation(c); e != nil {
		t.Fatal(e)
	}
	if b.VerifiedKey() != nil || a.VerifiedKey() != nil {
		t.Fatal("one operator was sufficient")
	}
	c, _ = b.Confirm()
	if e := a.ReceiveConfirmation(c); e != nil {
		t.Fatal(e)
	}
	if len(a.VerifiedKey()) != 32 || !bytes.Equal(a.VerifiedKey(), b.VerifiedKey()) {
		t.Fatal("key mismatch")
	}
	// Replays within a live exchange are idempotent.
	if e := a.ReceiveReveal(y); e != nil {
		t.Fatal(e)
	}
	if e := a.ReceiveConfirmation(c); e != nil {
		t.Fatal(e)
	}
}
func TestPairRejectsTranscriptChanges(t *testing.T) {
	a, b := exchanges(t)
	_ = a.ReceiveCommitment(b.Commitment())
	_ = b.ReceiveCommitment(a.Commitment())
	y, _ := b.Reveal()
	y.Data[40] ^= 1
	if a.ReceiveReveal(y) == nil {
		t.Fatal("accepted changed commitment opening")
	}
	y, _ = b.Reveal()
	y.ID[0] ^= 1
	if a.ReceiveReveal(y) == nil {
		t.Fatal("accepted changed session")
	}
	y, _ = b.Reveal()
	y.From = 3
	if a.ReceiveReveal(y) == nil {
		t.Fatal("accepted changed sender")
	}
	c := b.Commitment()
	c.Data[0] ^= 1
	if a.ReceiveCommitment(c) == nil {
		t.Fatal("replaced commitment")
	}
	y, _ = b.Reveal()
	_ = a.ReceiveReveal(y)
	c, _ = a.Confirm()
	if a.ReceiveConfirmation(c) == nil {
		t.Fatal("accepted reflected local confirmation")
	}
}
func TestPairEnvelopeBounds(t *testing.T) {
	a, _ := exchanges(t)
	m := a.Commitment()
	raw := m.Encode()
	if _, e := DecodePair(raw, 1, 2, time.Now()); e != nil {
		t.Fatal(e)
	}
	for _, b := range [][]byte{raw[:12], append(raw, 0), append([]byte("NOPE"), raw[4:]...)} {
		if _, e := DecodePair(b, 1, 2, time.Now()); e == nil {
			t.Fatal("bad frame accepted")
		}
	}
	if _, e := DecodePair(raw, 2, 1, time.Now()); e == nil {
		t.Fatal("wrong routing accepted")
	}
	if _, e := DecodePair(raw, 1, 2, time.Now().Add(10*time.Minute)); e == nil {
		t.Fatal("expired frame accepted")
	}
}
func TestRadioNodeMetadataOnly(t *testing.T) {
	user := blob(blob(blob(nil, 2, []byte("Example\x00 node")), 3, []byte("EX")), 9, bytes.Repeat([]byte{99}, 32))
	node := blob(varint(nil, 1, 42), 2, user)
	node = fixed(node, 5, 12345)
	n, e := DecodeRadioNode(blob(nil, 4, node))
	if e != nil || n == nil || n.Name != "Example node" || n.Node != 42 || n.LastHeard != 12345 {
		t.Fatal("invalid metadata", e)
	}
}
func FuzzPairEnvelope(f *testing.F) {
	f.Add([]byte("LOSP"))
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = DecodePair(b, 1, 2, time.Now()) })
}
