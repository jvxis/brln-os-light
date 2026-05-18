// Package electrs is a minimal Electrum-protocol client for talking to a
// local electrs instance. We use it to enrich transactions LND doesn't own
// (e.g. external senders/recipients on the provenance graph) by fetching
// their raw form and decoding outputs.
package electrs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultAddr    = "127.0.0.1:50001"
	dialTimeout    = 4 * time.Second
	requestTimeout = 8 * time.Second
)

// Client is goroutine-safe; it dials lazily and reuses a single TCP connection
// guarded by a write mutex. Read responses are correlated by request ID.
type Client struct {
	addr   string
	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
	nextID atomic.Int64
}

// New returns a client configured from the ELECTRUM_RPC_ADDR env var, falling
// back to 127.0.0.1:50001. Pass an explicit override for tests.
func New(overrideAddr string) *Client {
	addr := strings.TrimSpace(overrideAddr)
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("ELECTRUM_RPC_ADDR"))
	}
	if addr == "" {
		addr = defaultAddr
	}
	return &Client{addr: addr}
}

func (c *Client) Addr() string { return c.addr }

func (c *Client) dial(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("electrs dial %s: %w", c.addr, err)
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	return nil
}

func (c *Client) closeLocked() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.reader = nil
	}
}

type rpcResponse struct {
	ID    int64           `json:"id"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Result json.RawMessage `json:"result"`
}

// Call sends a JSON-RPC request and decodes the result into out. The client
// reuses one TCP connection so callers are serialized.
func (c *Client) Call(ctx context.Context, method string, params []any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.dial(ctx); err != nil {
		return err
	}

	id := c.nextID.Add(1)
	req := map[string]any{
		"id":     id,
		"method": method,
		"params": params,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	deadline := time.Now().Add(requestTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = c.conn.SetDeadline(deadline)

	if _, err := c.conn.Write(payload); err != nil {
		c.closeLocked()
		return fmt.Errorf("electrs write: %w", err)
	}

	for {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			c.closeLocked()
			return fmt.Errorf("electrs read: %w", err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Unexpected line (electrs sometimes emits notifications); skip.
			continue
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return fmt.Errorf("electrs error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return errors.New("electrs empty result")
		}
		return json.Unmarshal(resp.Result, out)
	}
}

// Ping sends server.version. Returns the [server, protocol] strings.
func (c *Client) Ping(ctx context.Context) ([2]string, error) {
	var out [2]string
	if err := c.Call(ctx, "server.version", []any{"brln-os", "1.4"}, &out); err != nil {
		return out, err
	}
	return out, nil
}

// VerboseVout mirrors the relevant fields of an electrs verbose-tx vout.
type VerboseVout struct {
	Value        float64 `json:"value"`
	N            uint32  `json:"n"`
	ScriptPubKey struct {
		Address   string   `json:"address"`
		Addresses []string `json:"addresses"`
		Type      string   `json:"type"`
		Hex       string   `json:"hex"`
	} `json:"scriptPubKey"`
}

// VerboseVin mirrors the relevant fields of an electrs verbose-tx vin.
type VerboseVin struct {
	Txid string `json:"txid"`
	Vout uint32 `json:"vout"`
}

// VerboseTx is the subset of a Bitcoin Core / electrs verbose tx we care about.
type VerboseTx struct {
	Txid          string        `json:"txid"`
	Hash          string        `json:"hash"`
	BlockHash     string        `json:"blockhash,omitempty"`
	Confirmations uint32        `json:"confirmations,omitempty"`
	Time          int64         `json:"time,omitempty"`
	Vin           []VerboseVin  `json:"vin"`
	Vout          []VerboseVout `json:"vout"`
}

// GetTransaction returns the verbose decoded transaction. Some older electrs
// builds don't support verbose; callers should handle the error.
func (c *Client) GetTransaction(ctx context.Context, txid string) (VerboseTx, error) {
	var out VerboseTx
	err := c.Call(ctx, "blockchain.transaction.get", []any{txid, true}, &out)
	return out, err
}
