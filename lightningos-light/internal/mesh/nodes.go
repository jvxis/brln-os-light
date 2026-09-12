package mesh

import (
	"google.golang.org/protobuf/encoding/protowire"
	"math"
	"strings"
	"unicode"
)

// Only public discovery metadata is retained. Never retain device configuration,
// channel keys, locations or firmware private keys from the configuration stream.
type RadioNode struct {
	SNR       *float32       `json:"snr,omitempty"`
	HopsAway  *uint32        `json:"hops_away,omitempty"`
	Metrics   *DeviceMetrics `json:"metrics,omitempty"`
	Node      uint32         `json:"node"`
	Name      string         `json:"name"`
	ShortName string         `json:"short_name"`
	LastHeard uint32         `json:"last_heard"`
	ViaMQTT   bool           `json:"via_mqtt"`
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
			case v.number == 4 && v.kind == protowire.Fixed32Type:
				n.SNR = finiteFloat(v.integer)
			case v.number == 9 && v.kind == protowire.VarintType && v.integer <= 7:
				hops := uint32(v.integer)
				n.HopsAway = &hops
			case v.number == 6 && v.kind == protowire.BytesType:
				metrics, err := decodeDeviceMetrics(v.bytes)
				if err != nil {
					return err
				}
				n.Metrics = metrics
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

// Schema: meshtastic/protobufs 3b3df2a5e54a6f4599ab37ac819da597607a4a27.
// NodeInfo has no device-metric sample time. A cached snapshot deliberately
// retains time=0; reading configuration is not a fresh telemetry measurement.
type DeviceMetrics struct {
	Battery  *uint32  `json:"battery,omitempty"`
	Channel  *float32 `json:"channel_utilization,omitempty"`
	Transmit *float32 `json:"air_util_tx,omitempty"`
	Time     uint32   `json:"time"`
}

func finiteFloat(bits uint64) *float32 {
	v := math.Float32frombits(uint32(bits))
	if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
		return nil
	}
	return &v
}
func decodeDeviceMetrics(raw []byte) (*DeviceMetrics, error) {
	m := &DeviceMetrics{}
	err := fields(raw, func(f wireField) error {
		if f.number == 1 && f.kind == protowire.VarintType && f.integer <= math.MaxUint32 {
			v := uint32(f.integer)
			m.Battery = &v
		}
		if (f.number == 3 || f.number == 4) && f.kind == protowire.Fixed32Type {
			v := finiteFloat(f.integer)
			if v != nil && *v >= 0 && *v <= 100 {
				if f.number == 3 {
					m.Channel = v
				} else {
					m.Transmit = v
				}
			}
		}
		return nil
	})
	return m, err
}

// Only device telemetry is projected. Text, locations and other payloads never
// enter the node cache or the API. No radio commands are sent to obtain metrics.
func DecodeNodeTelemetry(raw []byte) (*RadioNode, error) {
	var result *RadioNode
	err := fields(raw, func(f wireField) error {
		if f.number != 2 || f.kind != protowire.BytesType {
			return nil
		}
		n := &RadioNode{}
		var port uint64
		var payload []byte
		err := fields(f.bytes, func(v wireField) error {
			switch {
			case v.number == 1 && v.kind == protowire.Fixed32Type:
				n.Node = uint32(v.integer)
			case v.number == 7 && v.kind == protowire.Fixed32Type:
				n.LastHeard = uint32(v.integer)
			case v.number == 8 && v.kind == protowire.Fixed32Type:
				n.SNR = finiteFloat(v.integer)
			case v.number == 14 && v.kind == protowire.VarintType:
				n.ViaMQTT = v.integer != 0
			case v.number == 4 && v.kind == protowire.BytesType:
				return fields(v.bytes, func(d wireField) error {
					if d.number == 1 && d.kind == protowire.VarintType {
						port = d.integer
					}
					if d.number == 2 && d.kind == protowire.BytesType {
						payload = d.bytes
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		if port != 67 || n.Node == 0 || n.Node == 0xffffffff {
			return nil
		}
		var sample uint32
		err = fields(payload, func(t wireField) error {
			if t.number == 1 && t.kind == protowire.Fixed32Type {
				sample = uint32(t.integer)
			}
			if t.number == 2 && t.kind == protowire.BytesType {
				var err error
				n.Metrics, err = decodeDeviceMetrics(t.bytes)
				return err
			}
			return nil
		})
		if err == nil && n.Metrics != nil {
			n.Metrics.Time = sample
			result = n
		}
		return err
	})
	return result, err
}
