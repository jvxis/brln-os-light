package mesh

import (
	"bufio"
	"encoding/binary"
	"io"
	"math"

	"google.golang.org/protobuf/encoding/protowire"
)

const MaxSerialFrame = 512

// Minimal independent Protobuf wire adapter. Schema reference and provenance:
// docs/LOS_MESH.md. Unknown fields are skipped; no device configuration/admin
// commands or raw serial passthrough are exposed to clients.
type RadioPacket struct {
	From    uint32  `json:"from"`
	To      uint32  `json:"to"`
	Payload []byte  `json:"payload"`
	SNR     float32 `json:"snr"`
	RSSI    int32   `json:"rssi"`
}

func ReadFrame(r *bufio.Reader) ([]byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != 0x94 {
			continue
		}
		b, err = r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != 0xc3 {
			if b == 0x94 {
				_ = r.UnreadByte()
			}
			continue
		}
		var size [2]byte
		if _, err = io.ReadFull(r, size[:]); err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint16(size[:]))
		if n == 0 || n > MaxSerialFrame {
			continue
		}
		frame := make([]byte, n)
		_, err = io.ReadFull(r, frame)
		return frame, err
	}
}

func Frame(payload []byte) ([]byte, error) {
	if len(payload) == 0 || len(payload) > MaxSerialFrame {
		return nil, ErrPacket
	}
	b := []byte{0x94, 0xc3, byte(len(payload) >> 8), byte(len(payload))}
	return append(b, payload...), nil
}

func varint(b []byte, field protowire.Number, v uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(b, field, protowire.VarintType), v)
}
func fixed(b []byte, field protowire.Number, v uint32) []byte {
	return protowire.AppendFixed32(protowire.AppendTag(b, field, protowire.Fixed32Type), v)
}
func blob(b []byte, field protowire.Number, v []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(b, field, protowire.BytesType), v)
}
func ConfigRequest(id uint32) []byte { return varint(nil, 3, uint64(id)) }
func EncodeRadio(p RadioPacket, id uint32) ([]byte, error) {
	if p.To == 0 || p.To == 0xffffffff || len(p.Payload) == 0 || len(p.Payload) > MaxPacket {
		return nil, ErrPacket
	}
	data := blob(varint(nil, 1, PrivatePort), 2, p.Payload)
	packet := fixed(nil, 2, p.To)
	packet = blob(packet, 4, data)
	packet = fixed(packet, 6, id)
	packet = varint(packet, 9, 3)
	packet = varint(packet, 10, 1)
	return blob(nil, 1, packet), nil
}

type wireField struct {
	number  protowire.Number
	kind    protowire.Type
	integer uint64
	bytes   []byte
}

func fields(b []byte, visit func(wireField) error) error {
	if len(b) > MaxSerialFrame {
		return ErrPacket
	}
	for len(b) > 0 {
		n, t, k := protowire.ConsumeTag(b)
		if k < 0 {
			return ErrPacket
		}
		b = b[k:]
		f := wireField{number: n, kind: t}
		switch t {
		case protowire.VarintType:
			f.integer, k = protowire.ConsumeVarint(b)
		case protowire.Fixed32Type:
			var v uint32
			v, k = protowire.ConsumeFixed32(b)
			f.integer = uint64(v)
		case protowire.BytesType:
			f.bytes, k = protowire.ConsumeBytes(b)
		default:
			k = protowire.ConsumeFieldValue(n, t, b)
		}
		if k < 0 {
			return ErrPacket
		}
		if err := visit(f); err != nil {
			return err
		}
		b = b[k:]
	}
	return nil
}

func DecodeRadio(b []byte) (*RadioPacket, uint32, error) {
	var result *RadioPacket
	var node uint32
	err := fields(b, func(f wireField) error {
		if f.number == 3 && f.kind == protowire.BytesType {
			return fields(f.bytes, func(m wireField) error {
				if m.number == 1 && m.kind == protowire.VarintType {
					node = uint32(m.integer)
				}
				return nil
			})
		}
		if f.number != 2 || f.kind != protowire.BytesType {
			return nil
		}
		p := RadioPacket{}
		var port uint64
		err := fields(f.bytes, func(m wireField) error {
			switch {
			case m.number == 1 && m.kind == protowire.Fixed32Type:
				p.From = uint32(m.integer)
			case m.number == 2 && m.kind == protowire.Fixed32Type:
				p.To = uint32(m.integer)
			case m.number == 8 && m.kind == protowire.Fixed32Type:
				p.SNR = math.Float32frombits(uint32(m.integer))
				if math.IsNaN(float64(p.SNR)) || math.IsInf(float64(p.SNR), 0) {
					p.SNR = 0
				}
			case m.number == 12 && m.kind == protowire.VarintType:
				p.RSSI = int32(m.integer)
			case m.number == 4 && m.kind == protowire.BytesType:
				return fields(m.bytes, func(d wireField) error {
					if d.number == 1 && d.kind == protowire.VarintType {
						port = d.integer
					}
					if d.number == 2 && d.kind == protowire.BytesType {
						p.Payload = append([]byte{}, d.bytes...)
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		if port == PrivatePort && len(p.Payload) <= MaxPacket && len(p.Payload) >= HeaderSize+16 {
			result = &p
		}
		return nil
	})
	return result, node, err
}
