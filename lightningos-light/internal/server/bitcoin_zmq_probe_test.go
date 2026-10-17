package server

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// serve accepts one connection and answers it with reply (nil: stays silent until closed).
func serve(t *testing.T, reply []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if reply != nil {
			_, _ = conn.Write(reply)
		}
		buf := make([]byte, 64)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Read(buf)
	}()
	return "tcp://" + ln.Addr().String()
}

func TestProbeZMQAcceptsAZMQPublisher(t *testing.T) {
	addr := serve(t, zmtpSignature)
	if err := probeZMQ(context.Background(), addr, time.Second); err != nil {
		t.Fatalf("a ZMQ greeting was refused: %v", err)
	}
}

func TestProbeZMQRefusesAnotherService(t *testing.T) {
	addr := serve(t, []byte("HTTP/1.1 400 Bad Request\r\n"))
	err := probeZMQ(context.Background(), addr, time.Second)
	if err == nil || !strings.Contains(err.Error(), "not as ZMQ") {
		t.Fatalf("an HTTP answer passed as ZMQ: %v", err)
	}
}

// A firewall or proxy that accepts the connection and passes nothing would fool a bare TCP check.
func TestProbeZMQRefusesASilentPort(t *testing.T) {
	addr := serve(t, nil)
	start := time.Now()
	err := probeZMQ(context.Background(), addr, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no ZMQ greeting") {
		t.Fatalf("a silent port passed: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the probe did not respect its timeout")
	}
}

func TestProbeZMQRefusesAClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "tcp://" + ln.Addr().String()
	ln.Close()
	if err := probeZMQ(context.Background(), addr, 300*time.Millisecond); err == nil {
		t.Fatal("a closed port passed")
	}
}

func TestProbeBitcoinZMQNamesTheFailingEndpoint(t *testing.T) {
	ok := serve(t, zmtpSignature)
	bad := serve(t, []byte("nope......"))
	err := probeBitcoinZMQ(context.Background(), ok, bad)
	if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(bad, "tcp://")) {
		t.Fatalf("the failing endpoint is not named: %v", err)
	}
	if err := probeBitcoinZMQ(context.Background(), "", ""); err != nil {
		t.Fatalf("no endpoint configured should not fail: %v", err)
	}
}
