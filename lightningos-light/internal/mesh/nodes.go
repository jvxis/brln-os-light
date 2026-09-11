package mesh

import (
	"google.golang.org/protobuf/encoding/protowire"
	"strings"
	"unicode"
)

// Only public discovery metadata is retained. Never retain device configuration,
// channel keys, locations or firmware private keys from the configuration stream.
type RadioNode struct {
	Node      uint32 `json:"node"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	LastHeard uint32 `json:"last_heard"`
	ViaMQTT   bool   `json:"via_mqtt"`
}

func nodeText(b []byte) string {
	s := strings.ToValidUTF8(string(b), "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	r := []rune(s)
	if len(r) > 48 {
		r = r[:48]
	}
	return string(r)
}
func DecodeRadioNode(raw []byte) (*RadioNode, error) {
	var result *RadioNode
	err := fields(raw, func(f wireField) error {
		if f.number != 4 || f.kind != protowire.BytesType {
			return nil
		}
		n := &RadioNode{}
		err := fields(f.bytes, func(v wireField) error {
			switch {
			case v.number == 1 && v.kind == protowire.VarintType:
				n.Node = uint32(v.integer)
			case v.number == 5 && v.kind == protowire.Fixed32Type:
				n.LastHeard = uint32(v.integer)
			case v.number == 8 && v.kind == protowire.VarintType:
				n.ViaMQTT = v.integer != 0
			case v.number == 2 && v.kind == protowire.BytesType:
				return fields(v.bytes, func(u wireField) error {
					if u.kind == protowire.BytesType {
						if u.number == 2 {
							n.Name = nodeText(u.bytes)
						}
						if u.number == 3 {
							n.ShortName = nodeText(u.bytes)
						}
					}
					return nil
				})
			}
			return nil
		})
		if err == nil && n.Node != 0 && n.Node != 0xffffffff {
			result = n
		}
		return err
	})
	return result, err
}
