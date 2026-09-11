package mesh

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// TCP targets are literal private addresses: no DNS rebinding or public endpoints.
func TCPAddress(device string) (string, error) {
	if !strings.HasPrefix(device, "tcp://") {
		return "", errors.New("invalid TCP radio target")
	}
	address := strings.TrimPrefix(device, "tcp://")
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("enter a private IP address and TCP port")
	}
	ip, err := netip.ParseAddr(host)
	p, portErr := strconv.Atoi(port)
	if err != nil || !ip.IsPrivate() || ip.Zone() != "" || portErr != nil || p < 1 || p > 65535 || strconv.Itoa(p) != port {
		return "", errors.New("TCP radio requires a private IP address and valid port")
	}
	return net.JoinHostPort(ip.String(), port), nil
}

func OpenRadio(ctx context.Context, device string) (io.ReadWriteCloser, error) {
	if !strings.HasPrefix(device, "tcp://") {
		return OpenSerial(device)
	}
	address, err := TCPAddress(device)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return d.DialContext(ctx, "tcp", address)
}

// Heartbeat nonce zero only refreshes the client API; it does not broadcast a ping.
func Heartbeat() []byte { return blob(nil, 7, nil) }
