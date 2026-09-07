package mesh

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

const SocketPath = "/run/lightningos-mesh/bridge.sock"

type RadioStatus struct {
	Protocol    int       `json:"protocol"`
	SNR         float32   `json:"snr"`
	RSSI        int32     `json:"rssi"`
	State       string    `json:"state"`
	Node        uint32    `json:"node"`
	Device      string    `json:"device"`
	LastReceive time.Time `json:"last_receive"`
	Dropped     uint64    `json:"dropped"`
}

type Bridge struct {
	mu     sync.Mutex
	status RadioStatus
	rx     []RadioPacket
	tx     chan RadioPacket
}

func NewBridge(device string) *Bridge {
	return &Bridge{status: RadioStatus{Protocol: Version, State: "hardware_disconnected", Device: device}, tx: make(chan RadioPacket, 16)}
}

func (b *Bridge) Run(ctx context.Context, open func() (io.ReadWriteCloser, error)) {
	for ctx.Err() == nil {
		port, err := open()
		if err == nil {
			b.session(ctx, port)
		}
		b.mu.Lock()
		b.status.State = "hardware_disconnected"
		b.status.Node = 0
		b.rx = nil
		b.mu.Unlock()
		for len(b.tx) > 0 {
			<-b.tx
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (b *Bridge) session(ctx context.Context, port io.ReadWriteCloser) {
	defer port.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-ctx.Done(); _ = port.Close() }()
	// Wake the serial client interface, as in the Meshtastic serial client.
	// This prelude does not change any radio settings.
	if _, err := port.Write(bytes.Repeat([]byte{0xc3}, 32)); err != nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(100 * time.Millisecond):
	}
	var id [4]byte
	if _, err := rand.Read(id[:]); err != nil {
		return
	}
	frame, _ := Frame(ConfigRequest(binary.BigEndian.Uint32(id[:])))
	if _, err := port.Write(frame); err != nil {
		return
	}
	b.mu.Lock()
	b.status.State = "connecting"
	b.mu.Unlock()
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		r := bufio.NewReader(port)
		for {
			raw, err := ReadFrame(r)
			if err != nil {
				cancel()
				return
			}
			packet, node, err := DecodeRadio(raw)
			if err != nil {
				continue
			}
			b.mu.Lock()
			if node != 0 {
				b.status.Node = node
				b.status.State = "running"
			}
			if packet != nil {
				b.status.LastReceive = time.Now().UTC()
				b.status.SNR = packet.SNR
				b.status.RSSI = packet.RSSI
				if len(b.rx) < 64 {
					b.rx = append(b.rx, *packet)
				} else {
					b.status.Dropped++
				}
			}
			b.mu.Unlock()
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			b.mu.Lock()
			ready := b.status.Node != 0
			b.mu.Unlock()
			if !ready {
				return
			}
		case <-ticker.C:
			select {
			case p := <-b.tx:
				if _, err := rand.Read(id[:]); err != nil {
					return
				}
				raw, err := EncodeRadio(p, binary.BigEndian.Uint32(id[:]))
				if err != nil {
					continue
				}
				frame, _ := Frame(raw)
				if _, err = port.Write(frame); err != nil {
					return
				}
			default:
			}
		}
	}
}

func (b *Bridge) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		s := b.status
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
	mux.HandleFunc("GET /packets", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		packets := b.rx
		b.rx = nil
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(packets)
	})
	mux.HandleFunc("POST /packets", func(w http.ResponseWriter, r *http.Request) {
		var p RadioPacket
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		dec.DisallowUnknownFields()
		if dec.Decode(&p) != nil {
			http.Error(w, "invalid packet", 400)
			return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			http.Error(w, "invalid packet", 400)
			return
		}
		if _, err := EncodeRadio(p, 1); err != nil {
			http.Error(w, "invalid packet", 400)
			return
		}
		b.mu.Lock()
		ready := b.status.State == "running"
		b.mu.Unlock()
		if !ready {
			http.Error(w, "radio unavailable", 503)
			return
		}
		select {
		case b.tx <- p:
			w.WriteHeader(202)
		default:
			http.Error(w, "radio queue full", 429)
		}
	})
	return mux
}

// The socket accepts only the fixed Manager UID. World-connectable mode does
// not grant access: Linux SO_PEERCRED is checked before HTTP parsing.
type peerListener struct {
	net.Listener
	uid uint32
}

func (l peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := PeerUID(conn)
		if err == nil && (uid == l.uid || uid == 0) {
			return conn, nil
		}
		_ = conn.Close()
	}
}
func Serve(ctx context.Context, b *Bridge, managerUID uint32) error {
	if info, err := os.Lstat(SocketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("invalid socket path")
		}
		if err = os.Remove(SocketPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	l, err := net.Listen("unix", SocketPath)
	if err != nil {
		return err
	}
	defer l.Close()
	if err = os.Chmod(SocketPath, 0666); err != nil {
		return err
	}
	s := &http.Server{Handler: b.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 2048}
	go func() { <-ctx.Done(); _ = s.Close() }()
	return s.Serve(peerListener{l, managerUID})
}
