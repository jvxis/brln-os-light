package mesh

import (
	"bytes"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"
)

// Pairing uses a separate, bounded, mainnet-only envelope. Discovery is NOT
// authentication. Both commitments must be fixed before either public key is
// revealed. Operators must compare the SAS over an independent trusted channel.
const (
	PairProbe     byte = 1
	PairAvailable byte = 2
	PairInvite    byte = 3
	PairAccept    byte = 4
	PairReveal    byte = 5
	PairConfirm   byte = 6
	PairReject    byte = 7
	PairHeader         = 38
)

type PairMessage struct {
	Kind     byte
	From, To uint32
	ID       [16]byte
	Expires  int64
	Data     []byte
}

func IsPairMessage(raw []byte) bool { return len(raw) >= 4 && string(raw[:4]) == "LOSP" }

func (p PairMessage) Encode() []byte {
	b := make([]byte, PairHeader)
	copy(b, "LOSP")
	b[4] = 1
	b[5] = p.Kind
	binary.BigEndian.PutUint32(b[6:10], p.From)
	binary.BigEndian.PutUint32(b[10:14], p.To)
	copy(b[14:30], p.ID[:])
	binary.BigEndian.PutUint64(b[30:38], uint64(p.Expires))
	return append(b, p.Data...)
}

func DecodePair(raw []byte, from, to uint32, now time.Time) (PairMessage, error) {
	var p PairMessage
	if len(raw) < PairHeader || len(raw) > PairHeader+64 || !IsPairMessage(raw) || raw[4] != 1 {
		return p, ErrPacket
	}
	p.Kind = raw[5]
	p.From = binary.BigEndian.Uint32(raw[6:10])
	p.To = binary.BigEndian.Uint32(raw[10:14])
	copy(p.ID[:], raw[14:30])
	p.Expires = int64(binary.BigEndian.Uint64(raw[30:38]))
	p.Data = append([]byte(nil), raw[38:]...)
	want := -1
	switch p.Kind {
	case PairProbe, PairAvailable, PairReject:
		want = 0
	case PairInvite, PairAccept, PairConfirm:
		want = 32
	case PairReveal:
		want = 64
	}
	if want < 0 || len(p.Data) != want || p.From != from || p.To != to || from == 0 || to == 0 || from == to || from == 0xffffffff || to == 0xffffffff || p.ID == ([16]byte{}) || p.Expires <= now.Unix() || p.Expires > now.Add(6*time.Minute).Unix() {
		return PairMessage{}, ErrPacket
	}
	return p, nil
}

type PairExchange struct {
	Initiator                       bool
	Local, Remote                   uint32
	ID                              [16]byte
	Expires                         int64
	private                         *ecdh.PrivateKey
	opening                         [64]byte
	commitment                      [32]byte
	remoteCommit                    []byte
	remoteOpening                   []byte
	key                             []byte
	LocalConfirmed, RemoteConfirmed bool
}

func NewPairExchange(local, remote uint32, id [16]byte, expires int64, initiator bool) (*PairExchange, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	p := &PairExchange{Initiator: initiator, Local: local, Remote: remote, ID: id, Expires: expires, private: key}
	copy(p.opening[:32], key.PublicKey().Bytes())
	if _, err = rand.Read(p.opening[32:]); err != nil {
		return nil, err
	}
	p.commitment = p.commit(local, p.opening[:])
	return p, nil
}

func (p *PairExchange) message(kind byte, data []byte) PairMessage {
	return PairMessage{kind, p.Local, p.Remote, p.ID, p.Expires, append([]byte(nil), data...)}
}
func (p *PairExchange) commit(from uint32, opening []byte) [32]byte {
	other := p.Remote
	if from == p.Remote {
		other = p.Local
	}
	b := PairMessage{PairReveal, from, other, p.ID, p.Expires, opening}.Encode()
	return sha256.Sum256(append([]byte("LOS-Mesh-pair-mainnet-v1-commit"), b...))
}
func (p *PairExchange) Commitment() PairMessage {
	kind := PairAccept
	if p.Initiator {
		kind = PairInvite
	}
	return p.message(kind, p.commitment[:])
}
func (p *PairExchange) ReceiveCommitment(m PairMessage) error {
	kind := PairInvite
	if p.Initiator {
		kind = PairAccept
	}
	if !p.matches(m) || m.Kind != kind || len(m.Data) != 32 {
		return ErrPacket
	}
	if p.remoteCommit != nil && !bytes.Equal(p.remoteCommit, m.Data) {
		return ErrPacket
	}
	p.remoteCommit = append([]byte(nil), m.Data...)
	return nil
}
func (p *PairExchange) matches(m PairMessage) bool {
	return m.From == p.Remote && m.To == p.Local && m.ID == p.ID && m.Expires == p.Expires
}
func (p *PairExchange) Reveal() (PairMessage, error) {
	if len(p.remoteCommit) != 32 {
		return PairMessage{}, ErrPacket
	}
	return p.message(PairReveal, p.opening[:]), nil
}
func (p *PairExchange) ReceiveReveal(m PairMessage) error {
	if !p.matches(m) || m.Kind != PairReveal || len(m.Data) != 64 || len(p.remoteCommit) != 32 {
		return ErrPacket
	}
	h := p.commit(p.Remote, m.Data)
	if !hmac.Equal(h[:], p.remoteCommit) {
		return ErrPacket
	}
	if p.remoteOpening != nil {
		if !bytes.Equal(p.remoteOpening, m.Data) {
			return ErrPacket
		}
		return nil
	}
	pub, err := ecdh.X25519().NewPublicKey(m.Data[:32])
	if err != nil {
		return ErrPacket
	}
	shared, err := p.private.ECDH(pub)
	if err != nil {
		return ErrPacket
	}
	p.remoteOpening = append([]byte(nil), m.Data...)
	first, second := p.opening[:], p.remoteOpening
	if !p.Initiator {
		first, second = second, first
	}
	initiator, responder := p.Local, p.Remote
	if !p.Initiator {
		initiator, responder = responder, initiator
	}
	transcript := PairMessage{PairReveal, initiator, responder, p.ID, p.Expires, append(append([]byte(nil), first...), second...)}.Encode()
	// HKDF-Extract with the transcript hash as salt, followed by one expand block.
	salt := sha256.Sum256(append([]byte("LOS-Mesh-pair-mainnet-v1"), transcript...))
	extract := hmac.New(sha256.New, salt[:])
	extract.Write(shared)
	expand := hmac.New(sha256.New, extract.Sum(nil))
	expand.Write([]byte("application-key\x01"))
	p.key = expand.Sum(nil)
	return nil
}
func (p *PairExchange) SAS() string {
	if len(p.key) == 0 {
		return ""
	}
	h := hmac.New(sha256.New, p.key)
	h.Write([]byte("comparison-code"))
	n := binary.BigEndian.Uint32(h.Sum(nil)[:4]) % 100000000
	return fmt.Sprintf("%04d %04d", n/10000, n%10000)
}
func (p *PairExchange) confirmation(from uint32) []byte {
	h := hmac.New(sha256.New, p.key)
	h.Write([]byte("operator-confirmed"))
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], from)
	h.Write(b[:])
	return h.Sum(nil)
}
func (p *PairExchange) Confirm() (PairMessage, error) {
	if len(p.key) != 32 {
		return PairMessage{}, ErrPacket
	}
	p.LocalConfirmed = true
	return p.message(PairConfirm, p.confirmation(p.Local)), nil
}
func (p *PairExchange) ReceiveConfirmation(m PairMessage) error {
	if !p.matches(m) || m.Kind != PairConfirm || len(p.key) != 32 || !hmac.Equal(m.Data, p.confirmation(p.Remote)) {
		return ErrPacket
	}
	p.RemoteConfirmed = true
	return nil
}
func (p *PairExchange) VerifiedKey() []byte {
	if !p.LocalConfirmed || !p.RemoteConfirmed {
		return nil
	}
	return append([]byte(nil), p.key...)
}
