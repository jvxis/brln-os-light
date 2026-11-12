package server

import (
	"context"
	"net/http"
	"strings"

	"lightningos-light/internal/lndclient"
)

const (
	onchainAddressOwned    = "owned"    // derived from the node's own seed, spendable by it
	onchainAddressKnown    = "known"    // visible to the wallet (imported / watch-only) but not under its key
	onchainAddressExternal = "external" // not part of this wallet
)

type onchainAddressOwnership struct {
	Address string `json:"address"`
	Network string `json:"network"`
	Status  string `json:"status"`
}

// classifyOnchainAddressOwnership answers "is this address mine?" with three
// outcomes. "known" matters for close addresses and sweeps: the wallet can
// see such an address, but the funds are not under the node's private key.
func classifyOnchainAddressOwnership(ctx context.Context, lnd interface {
	IsWalletAddress(context.Context, string) (bool, error)
	IsControlledDefaultWalletAddress(context.Context, string) (bool, error)
}, address string) (string, error) {
	visible, err := lnd.IsWalletAddress(ctx, address)
	if err != nil {
		return "", err
	}
	if !visible {
		return onchainAddressExternal, nil
	}
	controlled, err := lnd.IsControlledDefaultWalletAddress(ctx, address)
	if err != nil {
		return "", err
	}
	if controlled {
		return onchainAddressOwned, nil
	}
	return onchainAddressKnown, nil
}

func (s *Server) handleOnchainAddressOwnership(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	if address == "" {
		writeError(w, http.StatusBadRequest, "address required")
		return
	}
	if len(address) > 128 {
		writeError(w, http.StatusBadRequest, "address too long")
		return
	}
	network, ok := lndclient.WalletAddressNetwork(address)
	if !ok {
		writeError(w, http.StatusBadRequest, "not a valid bitcoin address")
		return
	}
	if s.lnd == nil {
		writeError(w, http.StatusServiceUnavailable, "LND unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()
	status, err := classifyOnchainAddressOwnership(ctx, s.lnd, address)
	if err != nil {
		writeError(w, http.StatusBadGateway, lndStatusMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, onchainAddressOwnership{Address: address, Network: network, Status: status})
}
