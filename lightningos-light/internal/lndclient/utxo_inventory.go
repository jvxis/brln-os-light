package lndclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// ListOnchainUtxoInventory is for display and metadata maintenance ONLY.
// ListOnchainUtxos and all spending/preview/automation callers deliberately keep
// using native spendable outputs, which exclude leases.
// Return a complete unfiltered snapshot so response filters cannot prune live
// metadata. Native failures never become an apparently complete empty inventory.
func (c *Client) ListOnchainUtxoInventory(ctx context.Context) ([]OnchainUtxo, map[string]LeaseInfo, error) {
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	lightning := lnrpc.NewLightningClient(conn)
	kit := walletrpc.NewWalletKitClient(conn)
	unspent, err := lightning.ListUnspent(ctx, &lnrpc.ListUnspentRequest{MinConfs: 0, MaxConfs: math.MaxInt32})
	if err != nil {
		return nil, nil, err
	}
	response, err := kit.ListLeases(ctx, &walletrpc.ListLeasesRequest{})
	if err != nil {
		return nil, nil, err
	}
	leases := activeInventoryLeases(leasesFromRPC(response), uint64(time.Now().Unix()))
	items := onchainUtxosFromRPC(unspent.GetUtxos())
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.Outpoint] = true
	}
	var missing []string
	for point := range leases {
		if !seen[point] {
			missing = append(missing, point)
		}
	}
	if len(missing) == 0 {
		return items, leases, nil
	}
	sort.Strings(missing)

	// History provides confirmations, wallet ownership, addresses and spends.
	// Fetch it once for the missing leases, never once per output or for a normal
	// unleased read. Include unconfirmed spends so a stale lease cannot revive a
	// spent output. The caller's RPC timeout bounds this read as well.
	history, err := lightning.GetTransactions(ctx, &lnrpc.GetTransactionsRequest{StartHeight: 0, EndHeight: -1})
	if err != nil {
		return nil, nil, err
	}
	transactions := make(map[string]*lnrpc.Transaction)
	spent := make(map[string]bool)
	for _, tx := range history.GetTransactions() {
		if tx == nil || tx.GetNumConfirmations() < 0 {
			continue
		}
		transactions[strings.ToLower(tx.GetTxHash())] = tx
		for _, input := range tx.GetPreviousOutpoints() {
			if input != nil {
				spent[strings.ToLower(strings.TrimSpace(input.GetOutpoint()))] = true
			}
		}
		if tx.GetRawTxHex() != "" {
			raw, decodeErr := hex.DecodeString(tx.GetRawTxHex())
			if decodeErr != nil {
				return nil, nil, errors.New("invalid native wallet transaction")
			}
			var message wire.MsgTx
			reader := bytes.NewReader(raw)
			if message.Deserialize(reader) != nil || reader.Len() != 0 {
				return nil, nil, errors.New("invalid native wallet transaction")
			}
			for _, input := range message.TxIn {
				spent[input.PreviousOutPoint.String()] = true
			}
		}
	}
	// Recheck after history collection; an expired/released native lease must
	// not be resurrected merely because its transaction was found in history.
	response, err = kit.ListLeases(ctx, &walletrpc.ListLeasesRequest{})
	if err != nil {
		return nil, nil, err
	}
	current := activeInventoryLeases(leasesFromRPC(response), uint64(time.Now().Unix()))
	for point := range leases {
		if latest, ok := current[point]; ok {
			leases[point] = latest
		} else {
			delete(leases, point)
		}
	}
	for _, point := range missing {
		lease, exists := leases[point]
		if !exists || spent[point] {
			continue
		}
		tx := transactions[lease.Txid]
		if tx == nil {
			return nil, nil, errors.New("leased wallet output history unavailable")
		}
		var found *lnrpc.OutputDetail
		for _, output := range tx.GetOutputDetails() {
			if output != nil && output.GetOutputIndex() == int64(lease.Vout) {
				found = output
				break
			}
		}
		if found == nil || !found.GetIsOurAddress() || found.GetAmount() <= 0 || uint64(found.GetAmount()) != lease.Value {
			return nil, nil, errors.New("leased output does not match wallet ownership/value")
		}
		script, decodeErr := hex.DecodeString(found.GetPkScript())
		if decodeErr != nil || len(script) == 0 || !bytes.Equal(script, lease.PkScript) || found.GetAddress() == "" {
			return nil, nil, errors.New("leased output script/address unavailable")
		}
		kind := txscript.GetScriptClass(script).String()
		switch {
		case txscript.IsPayToWitnessPubKeyHash(script):
			kind = "p2wkh"
		case txscript.IsPayToScriptHash(script):
			kind = "np2wkh"
		case txscript.IsPayToTaproot(script):
			kind = "p2tr"
		}
		items = append(items, OnchainUtxo{Outpoint: point, Txid: lease.Txid, Vout: lease.Vout, AmountSat: found.GetAmount(), Confirmations: int64(tx.GetNumConfirmations()), PkScript: found.GetPkScript(), Address: found.GetAddress(), AddressType: kind})
	}
	return items, leases, nil
}

func activeInventoryLeases(leases map[string]LeaseInfo, now uint64) map[string]LeaseInfo {
	for point, lease := range leases {
		if lease.Expiration <= now {
			delete(leases, point)
		}
	}
	return leases
}
