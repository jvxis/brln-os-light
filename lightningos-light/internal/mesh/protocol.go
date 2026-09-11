// Package mesh implements the LOS Mesh application protocol. No Meshtastic
// generated sources or wallet credentials are part of this package.
package mesh

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"
)

const (
	Version              = 1
	PrivatePort          = 256
	ChunkSize            = 100
	LegacyChunkSize      = 120
	MaxChunks            = 128
	MaxContent           = ChunkSize * MaxChunks
	HeaderSize           = 92
	MaxPacket            = HeaderSize + LegacyChunkSize + 16
	Hello           byte = 1
	HelloAck        byte = 2
	Transaction     byte = 3
	ChunkAck        byte = 4
	Result          byte = 5
	Cancel          byte = 6
)

const Invoice byte = 7
const PaymentRequest byte = 8

func dataKind(kind byte) bool {
	return kind == Transaction || kind == Invoice || kind == PaymentRequest
}

var ErrPacket = errors.New("invalid mesh packet")

type Packet struct {
	WireVersion  byte // Zero retains v1 control/legacy packet encoding.
	Kind         byte
	From, To     uint32
	Session      [16]byte
	Index, Total uint16
	Size         uint32
	Hash         [32]byte
	Expires      int64
	Payload      []byte
}

// AES-256-GCM authenticates both header and content, including mainnet domain,
// peers, expiry, and fragment coordinates. Every transmission uses a fresh nonce.
func (p Packet) Seal(key []byte) ([]byte, error) {
	if err := p.validate(time.Now()); err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	b := make([]byte, HeaderSize)
	copy(b, "LOSM")
	b[4] = p.version()
	b[5] = p.Kind
	b[6] = 0 // Bitcoin mainnet
	binary.BigEndian.PutUint32(b[8:12], p.From)
	binary.BigEndian.PutUint32(b[12:16], p.To)
	copy(b[16:32], p.Session[:])
	binary.BigEndian.PutUint16(b[32:34], p.Index)
	binary.BigEndian.PutUint16(b[34:36], p.Total)
	binary.BigEndian.PutUint32(b[36:40], p.Size)
	copy(b[40:72], p.Hash[:])
	binary.BigEndian.PutUint64(b[72:80], uint64(p.Expires))
	// Fresh 96-bit nonce on every seal, including retransmission.
	if _, err = rand.Read(b[80:92]); err != nil {
		return nil, err
	}
	nonce := b[80:92]
	return aead.Seal(b, nonce, p.Payload, b), nil
}

func Open(b, key []byte, from, to uint32, now time.Time) (Packet, error) {
	p := Packet{}
	if len(b) < HeaderSize+16 || len(b) > MaxPacket || !bytes.Equal(b[:4], []byte("LOSM")) || (b[4] != 1 && b[4] != 2) || b[6] != 0 || b[7] != 0 {
		return p, ErrPacket
	}
	aead, err := newAEAD(key)
	if err != nil {
		return p, ErrPacket
	}
	nonce := b[80:92]
	p.Payload, err = aead.Open(nil, nonce, b[HeaderSize:], b[:HeaderSize])
	if err != nil {
		return Packet{}, ErrPacket
	}
	p.WireVersion = b[4]
	p.Kind = b[5]
	p.From = binary.BigEndian.Uint32(b[8:12])
	p.To = binary.BigEndian.Uint32(b[12:16])
	copy(p.Session[:], b[16:32])
	p.Index = binary.BigEndian.Uint16(b[32:34])
	p.Total = binary.BigEndian.Uint16(b[34:36])
	p.Size = binary.BigEndian.Uint32(b[36:40])
	copy(p.Hash[:], b[40:72])
	p.Expires = int64(binary.BigEndian.Uint64(b[72:80]))
	if p.From != from || p.To != to {
		return Packet{}, ErrPacket
	}
	return p, p.validate(now)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrPacket
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func (p Packet) version() byte {
	if p.WireVersion == 0 {
		return 1
	}
	return p.WireVersion
}

func (p Packet) validate(now time.Time) error {
	chunkSize := LegacyChunkSize
	if p.version() == 2 {
		chunkSize = ChunkSize
	} else if p.version() != 1 {
		return ErrPacket
	}
	if p.From == 0 || p.To == 0 || p.To == 0xffffffff || p.From == p.To || p.Session == ([16]byte{}) || p.Kind < Hello || p.Kind > PaymentRequest || len(p.Payload) > chunkSize || p.Expires <= now.Unix() || p.Expires > now.Add(30*time.Minute).Unix() {
		return ErrPacket
	}
	if dataKind(p.Kind) {
		if p.Size == 0 || p.Size > LegacyChunkSize*MaxChunks || p.Total > MaxChunks || p.Total != uint16((int(p.Size)+chunkSize-1)/chunkSize) || p.Index >= p.Total {
			return ErrPacket
		}
		want := chunkSize
		if p.Index == p.Total-1 {
			want = int(p.Size) - int(p.Index)*chunkSize
		}
		if len(p.Payload) != want {
			return ErrPacket
		}
	} else if p.Total != 0 || p.Size != 0 {
		return ErrPacket
	}
	return nil
}

func Fragment(from, to uint32, kind byte, content []byte, now time.Time) ([]Packet, error) {
	if len(content) == 0 || len(content) > MaxContent {
		return nil, ErrPacket
	}
	var session [16]byte
	if _, err := rand.Read(session[:]); err != nil {
		return nil, err
	}
	total := (len(content) + ChunkSize - 1) / ChunkSize
	packets := make([]Packet, 0, total)
	for i := 0; i < total; i++ {
		end := (i + 1) * ChunkSize
		if end > len(content) {
			end = len(content)
		}
		packets = append(packets, Packet{WireVersion: 2, Kind: kind, From: from, To: to, Session: session, Index: uint16(i), Total: uint16(total), Size: uint32(len(content)), Hash: sha256.Sum256(content), Expires: now.Add(20 * time.Minute).Unix(), Payload: append([]byte{}, content[i*ChunkSize:end]...)})
	}
	return packets, nil
}

type Assembly struct {
	First Packet
	Parts map[uint16][]byte
}

// Add accepts authenticated fragments only. Caller bounds active assemblies and
// consults its durable session ledger before allocating one.
func (a *Assembly) Add(p Packet) ([]byte, error) {
	if !dataKind(p.Kind) || p.validate(time.Now()) != nil {
		return nil, ErrPacket
	}
	if a.Parts == nil {
		a.First = p
		a.First.Payload = nil
		a.Parts = make(map[uint16][]byte)
	}
	f := a.First
	if p.version() != f.version() || p.Kind != f.Kind || p.From != f.From || p.To != f.To || p.Session != f.Session || p.Total != f.Total || p.Size != f.Size || p.Hash != f.Hash || p.Expires != f.Expires {
		return nil, ErrPacket
	}
	if old, ok := a.Parts[p.Index]; ok && !bytes.Equal(old, p.Payload) {
		return nil, ErrPacket
	}
	a.Parts[p.Index] = append([]byte{}, p.Payload...)
	if len(a.Parts) != int(p.Total) {
		return nil, nil
	}
	content := make([]byte, 0, p.Size)
	for i := uint16(0); i < p.Total; i++ {
		content = append(content, a.Parts[i]...)
	}
	if uint32(len(content)) != p.Size || sha256.Sum256(content) != p.Hash {
		return nil, ErrPacket
	}
	return content, nil
}
