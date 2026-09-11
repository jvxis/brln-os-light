package mesh

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestTCPAddressPolicy(t *testing.T) {
	for _, target := range []string{"tcp://192.168.1.50:4403", "tcp://10.1.2.3:4403", "tcp://[fd00::1234]:4403"} {
		if _, err := TCPAddress(target); err != nil {
			t.Fatal(target, err)
		}
	}
	for _, target := range []string{"tcp://127.0.0.1:4403", "tcp://8.8.8.8:4403", "tcp://169.254.169.254:80", "tcp://radio.local:4403", "tcp://192.168.1.1:0", "tcp://192.168.1.1:65536", "tcp://192.168.1.1:4403/path", "tcp://[::1]:4403", "tcp://[ff02::1]:4403", "tcp://192.168.1.1:4403\nExecStart=x"} {
		if _, err := TCPAddress(target); err == nil {
			t.Fatal("unsafe target accepted", target)
		}
	}
	if got := Heartbeat(); len(got) != 2 || got[0] != 0x3a || got[1] != 0 {
		t.Fatal("heartbeat must be empty nonce-zero ToRadio field 7", got)
	}
}

func TestTCPBridgeReconnectAndMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	b := NewBridge("tcp://192.168.1.50:4403")
	var attempts atomic.Int32
	ready := make(chan struct{})
	sent := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Run(ctx, func() (io.ReadWriteCloser, error) {
			client, radio := net.Pipe()
			attempt := attempts.Add(1)
			go func() {
				defer radio.Close()
				_ = radio.SetDeadline(time.Now().Add(11 * time.Second))
				raw, err := ReadFrame(bufio.NewReader(radio))
				if err != nil || len(raw) == 0 || raw[0] != 0x18 {
					return
				}
				if attempt == 1 {
					return
				} // EOF must reopen the transport and repeat identification.
				frame, _ := Frame(blob(nil, 3, varint(nil, 1, 123)))
				if _, err = radio.Write(frame); err != nil {
					return
				}
				user := blob(blob(nil, 2, []byte("Radio owner")), 3, []byte("OWN"))
				frame, _ = Frame(blob(nil, 4, blob(varint(nil, 1, 123), 2, user)))
				if _, err = radio.Write(frame); err != nil {
					return
				}
				close(ready)
				reader := bufio.NewReader(radio)
				for i := 0; i < 2; i++ {
					data, e := ReadFrame(reader)
					if e != nil || len(data) == 0 || data[0] != 0x0a {
						return
					}
				}
				close(sent)
				<-ctx.Done()
			}()
			return client, nil
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("reconnection did not complete")
	}
	for until := time.Now().Add(time.Second); ; {
		b.mu.Lock()
		name, short, state := b.status.Name, b.status.ShortName, b.status.State
		b.mu.Unlock()
		if name == "Radio owner" && short == "OWN" && state == "running" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("missing metadata", name, short, state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.tx <- RadioPacket{To: 456, Payload: []byte{1}}
	b.tx <- RadioPacket{To: 456, Payload: []byte{2}}
	select {
	case <-sent:
	case <-ctx.Done():
		t.Fatal("persistent session did not deliver both actions")
	}
	if attempts.Load() != 2 {
		t.Fatal("actions opened new connections")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader leaked after cancellation")
	}
}
