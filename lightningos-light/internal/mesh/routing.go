package mesh

import "google.golang.org/protobuf/encoding/protowire"

// Routing reports are transport diagnostics, never application acknowledgements.
// Only a request ID issued by this bridge may update its status.
func decodeRouting(raw []byte) (request uint32, code uint32, err error) {
	err = fields(raw, func(f wireField) error {
		if f.number != 2 || f.kind != protowire.BytesType {
			return nil
		}
		return fields(f.bytes, func(p wireField) error {
			if p.number != 4 || p.kind != protowire.BytesType {
				return nil
			}
			var port uint64
			var payload []byte
			var id uint32
			e := fields(p.bytes, func(d wireField) error {
				switch {
				case d.number == 1 && d.kind == protowire.VarintType:
					port = d.integer
				case d.number == 2 && d.kind == protowire.BytesType:
					payload = d.bytes
				case d.number == 6 && d.kind == protowire.Fixed32Type:
					id = uint32(d.integer)
				}
				return nil
			})
			if e != nil || port != 5 || id == 0 {
				return e
			}
			return fields(payload, func(r wireField) error {
				if r.number == 3 && r.kind == protowire.VarintType {
					request, code = id, uint32(r.integer)
				}
				return nil
			})
		})
	})
	return
}
