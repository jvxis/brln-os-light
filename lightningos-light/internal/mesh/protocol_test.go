package mesh

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthenticatedReassembly(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	content := bytes.Repeat([]byte{7}, MaxContent)
	packets, err := Fragment(123, 456, Transaction, content, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var assembly Assembly
	for i := len(packets) - 1; i >= 0; i-- {
		sealed, err := packets[i].Seal(key)
		if err != nil {
			t.Fatal(err)
		}
		if len(sealed) > 233 {
			t.Fatal("exceeds Meshtastic payload budget")
		}
		p, err := Open(sealed, key, 123, 456, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		result, err := assembly.Add(p)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && result != nil {
			t.Fatal("premature completion")
		}
		if i == 0 && !bytes.Equal(content, result) {
			t.Fatal("content mismatch")
		}
		if _, err = assembly.Add(p); err != nil {
			t.Fatal("identical duplicate rejected", err)
		}
	}
}
func TestRejectTamperWrongPeerKeyNetworkExpiryAndCoordinates(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	packets, _ := Fragment(10, 20, Transaction, bytes.Repeat([]byte{2}, 241), time.Now())
	raw, _ := packets[0].Seal(key)
	for i := range raw {
		copyRaw := append([]byte{}, raw...)
		copyRaw[i] ^= 1
		if _, err := Open(copyRaw, key, 10, 20, time.Now()); err == nil {
			t.Fatalf("accepted tampering at byte %d", i)
		}
	}
	for _, test := range []struct {
		from, to uint32
		key      []byte
		now      time.Time
	}{{11, 20, key, time.Now()}, {10, 21, key, time.Now()}, {10, 20, bytes.Repeat([]byte{2}, 32), time.Now()}, {10, 20, key, time.Now().Add(21 * time.Minute)}} {
		if _, err := Open(raw, test.key, test.from, test.to, test.now); err == nil {
			t.Fatal("accepted wrong context")
		}
	}
	p := packets[0]
	p.Total = 65535
	if _, err := p.Seal(key); err == nil {
		t.Fatal("unbounded coordinates")
	}
	p = packets[0]
	p.Index = p.Total
	if _, err := p.Seal(key); err == nil {
		t.Fatal("out-of-range index")
	}
	var a Assembly
	_, _ = a.Add(packets[0])
	p = packets[1]
	p.Hash[0] ^= 1
	if _, err := a.Add(p); err == nil {
		t.Fatal("mixed content hash")
	}
	p = packets[1]
	p.Kind = Invoice
	if _, err := a.Add(p); err == nil {
		t.Fatal("mixed message kinds")
	}
	p = packets[0]
	p.Payload = append([]byte{}, p.Payload...)
	p.Payload[0] ^= 1
	if _, err := a.Add(p); err == nil {
		t.Fatal("conflicting duplicate")
	}
}
func TestIncompleteAndBadFullHash(t *testing.T) {
	packets, _ := Fragment(1, 2, Invoice, bytes.Repeat([]byte{2}, 121), time.Now())
	var a Assembly
	if result, err := a.Add(packets[0]); err != nil || result != nil {
		t.Fatal("missing fragment must not complete")
	}
	packets[1].Payload[0] ^= 1
	if _, err := a.Add(packets[1]); err == nil {
		t.Fatal("whole-content corruption accepted")
	}
}
func TestSerialFramesResynchronize(t *testing.T) {
	raw := ConfigRequest(12345)
	frame, _ := Frame(raw)
	stream := append([]byte("boot debug\n"), 0x94, 0x94, 0xc3, 0xff, 0xff)
	stream = append(stream, frame...)
	decoded, err := ReadFrame(bufio.NewReader(bytes.NewReader(stream)))
	if err != nil || !bytes.Equal(raw, decoded) {
		t.Fatal("frame resync failed", err)
	}
	if _, err := ReadFrame(bufio.NewReader(bytes.NewReader(frame[:len(frame)-1]))); err == nil {
		t.Fatal("truncated frame accepted")
	}
	if _, err := Frame(make([]byte, 513)); err == nil {
		t.Fatal("oversized serial frame accepted")
	}
}
func TestDecodeOnlyPrivateApplication(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	packets, _ := Fragment(1, 2, Invoice, []byte("lnbc-test"), time.Now())
	payload, _ := packets[0].Seal(key)
	for _, port := range []uint64{1, PrivatePort} {
		data := blob(varint(nil, 1, port), 2, payload)
		packet := fixed(fixed(nil, 1, 1), 2, 2)
		packet = blob(packet, 4, data)
		p, node, err := DecodeRadio(blob(blob(nil, 2, packet), 3, varint(nil, 1, 2)))
		if err != nil || node != 2 {
			t.Fatal(err)
		}
		if (p != nil) != (port == PrivatePort) {
			t.Fatal("incorrect port filtering")
		}
	}
}
func TestBridgeWithSimulatedSerialAndDisconnect(t *testing.T) {
	bridge := NewBridge("test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local, radio := net.Pipe()
	defer radio.Close()
	done := make(chan struct{})
	go func() { defer close(done); bridge.session(ctx, local) }()
	r := bufio.NewReader(radio)
	if _, err := ReadFrame(r); err != nil {
		t.Fatal(err)
	}
	frame, _ := Frame(blob(nil, 3, varint(nil, 1, 123)))
	if _, err := radio.Write(frame); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		bridge.mu.Lock()
		node := bridge.status.Node
		bridge.mu.Unlock()
		if node == 123 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("configuration not processed")
		}
		time.Sleep(time.Millisecond)
	}
	status := httptest.NewRecorder()
	bridge.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/status", nil))
	if status.Code != 200 {
		t.Fatal(status.Code)
	}
	for _, body := range []string{`{"to":4294967295,"payload":"AA=="}`, `{"to":123,"payload":"AA==","command":"reboot"}`, `{} {}`} {
		response := httptest.NewRecorder()
		bridge.Handler().ServeHTTP(response, httptest.NewRequest("POST", "/packets", bytes.NewBufferString(body)))
		if response.Code != 400 {
			t.Fatalf("accepted invalid command: %d", response.Code)
		}
	}
	_ = radio.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not terminate serial session")
	}
}
func FuzzAuthenticatedParser(f *testing.F) {
	key := bytes.Repeat([]byte{1}, 32)
	packets, _ := Fragment(1, 2, Transaction, []byte("test"), time.Now())
	raw, _ := packets[0].Seal(key)
	f.Add(raw)
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := Open(b, key, 1, 2, time.Now())
		if err == nil {
			if len(p.Payload) > ChunkSize || p.Total > MaxChunks {
				t.Fatal("bounds violated")
			}
		}
	})
}
func FuzzRadioParser(f *testing.F) {
	f.Add([]byte{0x1a, 2, 8, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _, _ = DecodeRadio(b)
		if len(b) <= 512 {
			frame := []byte{0x94, 0xc3, 0, 0}
			binary.BigEndian.PutUint16(frame[2:], uint16(len(b)))
			frame = append(frame, b...)
			_, err := ReadFrame(bufio.NewReader(bytes.NewReader(frame)))
			if len(b) > 0 && err != nil && err != io.EOF {
				t.Fatal(err)
			}
		}
	})
}
