package lndclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"
)

// utxoManagerLeaseSeed prefixes outpoints to derive deterministic lease IDs
// so we can release a lease without persisting its ID. Keep stable: changing
// it strands existing leases until they expire.
const utxoManagerLeaseSeed = "brln-os-utxo-mgr:v1:"

// SendCoinsParams carries the full set of options accepted by SendCoinsAdvanced.
// The zero value is valid; callers fill what they need.
type SendCoinsParams struct {
	Address          string
	AmountSat        int64
	SatPerVbyte      int64
	SendAll          bool
	Outpoints        []string
	Label            string
	MinConfs         int32
	SpendUnconfirmed bool
}

// SendCoinsAdvanced is the option-rich counterpart of SendCoins. When Outpoints
// is non-empty, LND restricts coin selection to those outpoints. The original
// SendCoins helper remains for the common address+amount path.
func (c *Client) SendCoinsAdvanced(ctx context.Context, params SendCoinsParams) (string, error) {
	address := strings.TrimSpace(params.Address)
	if address == "" {
		return "", errors.New("address required")
	}
	if !params.SendAll && params.AmountSat <= 0 {
		return "", errors.New("amount_sat must be positive")
	}

	outpoints, err := parseOutpointList(params.Outpoints)
	if err != nil {
		return "", err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	req := &lnrpc.SendCoinsRequest{
		Addr:             address,
		SendAll:          params.SendAll,
		MinConfs:         params.MinConfs,
		SpendUnconfirmed: params.SpendUnconfirmed,
		Outpoints:        outpoints,
		Label:            strings.TrimSpace(params.Label),
	}
	if !params.SendAll {
		req.Amount = params.AmountSat
	}
	if params.SatPerVbyte > 0 {
		req.SatPerVbyte = uint64(params.SatPerVbyte)
	}

	resp, err := client.SendCoins(ctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	return resp.Txid, nil
}

// OpenChannelParams mirrors OpenChannelWithPush's arguments and adds outpoint
// pinning. Callers without a selection should keep using OpenChannelWithPush.
type OpenChannelParams struct {
	PubkeyHex       string
	LocalFundingSat int64
	PushSat         int64
	CloseAddress    string
	Private         bool
	SatPerVbyte     int64
	Outpoints       []string
}

// OpenChannelWithOutpoints opens a channel using a fixed set of UTXOs as
// funding inputs. Useful when the caller wants the channel funded from a
// specific user-selected UTXO group.
func (c *Client) OpenChannelWithOutpoints(ctx context.Context, params OpenChannelParams) (string, error) {
	pubkeyHex := strings.TrimSpace(params.PubkeyHex)
	if pubkeyHex == "" {
		return "", errors.New("pubkey required")
	}
	if params.LocalFundingSat <= 0 {
		return "", errors.New("local funding must be positive")
	}
	if params.PushSat < 0 {
		return "", errors.New("push amount must be zero or positive")
	}
	if params.PushSat > params.LocalFundingSat {
		return "", errors.New("push amount cannot exceed local funding")
	}
	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return "", fmt.Errorf("invalid pubkey hex")
	}

	outpoints, err := parseOutpointList(params.Outpoints)
	if err != nil {
		return "", err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	req := &lnrpc.OpenChannelRequest{
		NodePubkey:         pubkey,
		LocalFundingAmount: params.LocalFundingSat,
		PushSat:            params.PushSat,
		Private:            params.Private,
		Outpoints:          outpoints,
	}
	if params.SatPerVbyte > 0 {
		req.SatPerVbyte = uint64(params.SatPerVbyte)
	}
	if strings.TrimSpace(params.CloseAddress) != "" {
		req.CloseAddress = strings.TrimSpace(params.CloseAddress)
	}

	resp, err := client.OpenChannelSync(ctx, req)
	if err != nil {
		return "", err
	}
	return channelPointString(resp), nil
}

// LeaseInfo describes a UTXO lease as reported by LND.
type LeaseInfo struct {
	Outpoint   string `json:"outpoint"`
	Txid       string `json:"txid"`
	Vout       uint32 `json:"vout"`
	Expiration uint64 `json:"expiration"`
	Value      uint64 `json:"value"`
	ID         string `json:"id"`
}

// LeaseOutput locks an outpoint for the given expiry window (seconds). The
// lease ID is deterministically derived from the outpoint so callers can
// release without storing it.
func (c *Client) LeaseOutput(ctx context.Context, outpoint string, expirySec uint64) (LeaseInfo, error) {
	point, err := parseOutPoint(outpoint)
	if err != nil {
		return LeaseInfo{}, err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return LeaseInfo{}, err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	req := &walletrpc.LeaseOutputRequest{
		Id:                deriveLeaseID(outpoint),
		Outpoint:          point,
		ExpirationSeconds: expirySec,
	}
	resp, err := client.LeaseOutput(ctx, req)
	if err != nil {
		return LeaseInfo{}, err
	}
	return LeaseInfo{
		Outpoint:   normalizedOutpointString(point),
		Txid:       strings.ToLower(strings.TrimSpace(point.GetTxidStr())),
		Vout:       point.GetOutputIndex(),
		Expiration: resp.GetExpiration(),
		ID:         hex.EncodeToString(req.Id),
	}, nil
}

// ReleaseOutput unlocks a UTXO that we previously leased with the manager.
// Returns nil if the lease no longer exists.
func (c *Client) ReleaseOutput(ctx context.Context, outpoint string) error {
	point, err := parseOutPoint(outpoint)
	if err != nil {
		return err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	_, err = client.ReleaseOutput(ctx, &walletrpc.ReleaseOutputRequest{
		Id:       deriveLeaseID(outpoint),
		Outpoint: point,
	})
	if err != nil && isLeaseNotFoundErr(err) {
		return nil
	}
	return err
}

// ListLeases returns all currently locked outpoints, keyed by "txid:vout".
func (c *Client) ListLeases(ctx context.Context) (map[string]LeaseInfo, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	resp, err := client.ListLeases(ctx, &walletrpc.ListLeasesRequest{})
	if err != nil {
		return nil, err
	}

	leases := make(map[string]LeaseInfo, len(resp.GetLockedUtxos()))
	for _, lease := range resp.GetLockedUtxos() {
		if lease == nil {
			continue
		}
		point := lease.GetOutpoint()
		if point == nil {
			continue
		}
		outpoint := normalizedOutpointString(point)
		if outpoint == "" {
			continue
		}
		leases[outpoint] = LeaseInfo{
			Outpoint:   outpoint,
			Txid:       strings.ToLower(strings.TrimSpace(point.GetTxidStr())),
			Vout:       point.GetOutputIndex(),
			Expiration: lease.GetExpiration(),
			Value:      lease.GetValue(),
			ID:         hex.EncodeToString(lease.GetId()),
		}
	}
	return leases, nil
}

func deriveLeaseID(outpoint string) []byte {
	sum := sha256.Sum256([]byte(utxoManagerLeaseSeed + strings.ToLower(strings.TrimSpace(outpoint))))
	return sum[:]
}

func parseOutpointList(items []string) ([]*lnrpc.OutPoint, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]*lnrpc.OutPoint, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, raw := range items {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		point, err := parseOutPoint(trimmed)
		if err != nil {
			return nil, err
		}
		out = append(out, point)
	}
	return out, nil
}

func normalizedOutpointString(point *lnrpc.OutPoint) string {
	if point == nil {
		return ""
	}
	txid := strings.ToLower(strings.TrimSpace(point.GetTxidStr()))
	if txid == "" {
		txid = txidFromBytes(point.GetTxidBytes())
	}
	if txid == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", txid, point.GetOutputIndex())
}

func isLeaseNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no such lease") || strings.Contains(msg, "unknown")
}
