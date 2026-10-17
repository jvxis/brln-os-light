package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// zmtpSignature is the 10-byte signature every ZMTP 3 peer sends first: 0xFF, eight padding
// bytes, 0x7F. A Bitcoin Core ZMQ publisher sends it as soon as a connection opens.
var zmtpSignature = []byte{0xFF, 0, 0, 0, 0, 0, 0, 0, 1, 0x7F}

// errBitcoinZMQUnreachable marks a remote Bitcoin whose RPC answers while its ZMQ does not.
const errBitcoinZMQUnreachable = "bitcoin_zmq_unreachable"

// probeZMQ checks that a ZMQ endpoint answers like ZMQ from this node.
//
// The RPC answering is not enough for LND: it learns about new blocks and transactions over
// ZMQ, and without it the chain never syncs, so LND never looks for peers nor downloads the
// graph. On 17/10/2026 a BR⚡LN member's node made tens of thousands of RPC calls to the club's
// Bitcoin while it never opened its ZMQ ports (28332, 28333) -- something on his network blocked
// them -- and the setup page had said everything was fine. A bare TCP connect (testTCP) would
// pass on a firewall or proxy that accepts and then drops, so this reads the peer's ZMTP
// signature, which also catches an address pointing at the wrong port.
func probeZMQ(ctx context.Context, endpoint string, timeout time.Duration) error {
	host, port, err := splitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		return fmt.Errorf("invalid ZMQ address %q", endpoint)
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("could not connect to %s:%s: %w", host, port, err)
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	// Each side sends its signature without waiting for the other's.
	if _, err := conn.Write(zmtpSignature); err != nil {
		return fmt.Errorf("%s:%s closed the connection: %w", host, port, err)
	}
	greeting := make([]byte, len(zmtpSignature))
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return fmt.Errorf("%s:%s accepted the connection but sent no ZMQ greeting: %w", host, port, err)
	}
	if greeting[0] != 0xFF || greeting[9] != 0x7F {
		return errors.New(host + ":" + port + " answered, but not as ZMQ")
	}
	return nil
}

// probeBitcoinZMQ checks the block and transaction endpoints LND will be given, and names the
// first that fails.
func probeBitcoinZMQ(ctx context.Context, block, tx string) error {
	for _, endpoint := range []string{block, tx} {
		if strings.TrimSpace(endpoint) == "" {
			continue
		}
		if err := probeZMQ(ctx, endpoint, 4*time.Second); err != nil {
			return err
		}
	}
	return nil
}
