package mesh

import (
	"encoding/json"
	"math"
	"testing"
)

func TestNodeMetricsPresenceAndBounds(t *testing.T) {
	metrics := varint(nil, 1, 0)
	metrics = fixed(metrics, 3, math.Float32bits(0))
	metrics = fixed(metrics, 4, math.Float32bits(14.5))
	node := blob(varint(nil, 1, 42), 6, metrics)
	node = fixed(node, 4, math.Float32bits(0))
	node = varint(node, 9, 0)
	n, err := DecodeRadioNode(blob(nil, 4, node))
	if err != nil || n == nil || n.SNR == nil || *n.SNR != 0 || n.HopsAway == nil || *n.HopsAway != 0 || n.Metrics.Battery == nil || *n.Metrics.Battery != 0 || n.Metrics.Channel == nil || *n.Metrics.Transmit != 14.5 || n.Metrics.Time != 0 {
		t.Fatalf("lost optional zero values: %#v %v", n, err)
	}
	n, err = DecodeRadioNode(blob(nil, 4, varint(nil, 1, 42)))
	if err != nil || n.Metrics != nil || n.SNR != nil || n.HopsAway != nil {
		t.Fatal("invented absent metrics")
	}
	invalid := fixed(fixed(nil, 3, math.Float32bits(float32(math.NaN()))), 4, math.Float32bits(101))
	m, err := decodeDeviceMetrics(invalid)
	if err != nil || m.Channel != nil || m.Transmit != nil {
		t.Fatal("accepted invalid percentages")
	}
	m, err = decodeDeviceMetrics(varint(nil, 1, 101))
	if err != nil || m.Battery == nil || *m.Battery != 101 {
		t.Fatal("lost external power indication")
	}
	if _, err = decodeDeviceMetrics([]byte{0xff}); err == nil {
		t.Fatal("accepted malformed protobuf")
	}
}

func TestDeviceTelemetryProjection(t *testing.T) {
	telemetry := blob(fixed(nil, 1, 1234), 2, varint(nil, 1, 55))
	// Irrelevant location field must never be copied to the output.
	telemetry = blob(telemetry, 3, []byte("private location"))
	packet := blob(fixed(fixed(nil, 1, 42), 7, 1300), 4, blob(varint(nil, 1, 67), 2, telemetry))
	packet = varint(packet, 14, 1)
	n, err := DecodeNodeTelemetry(blob(nil, 2, packet))
	if err != nil || n == nil || n.Metrics.Time != 1234 || *n.Metrics.Battery != 55 || !n.ViaMQTT || n.LastHeard != 1300 {
		t.Fatalf("bad telemetry: %#v %v", n, err)
	}
	encoded, _ := json.Marshal(n)
	var output map[string]any
	_ = json.Unmarshal(encoded, &output)
	if _, ok := output["payload"]; ok {
		t.Fatal("payload escaped")
	}
	// Ordinary text packets are ignored even when bytes resemble telemetry.
	packet = blob(fixed(nil, 1, 42), 4, blob(varint(nil, 1, 1), 2, telemetry))
	if n, err = DecodeNodeTelemetry(blob(nil, 2, packet)); err != nil || n != nil {
		t.Fatal("parsed unrelated port")
	}
}
