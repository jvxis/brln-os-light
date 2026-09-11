package mesh

import (
	"bytes"
	"errors"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

type Output struct {
	Address string `json:"address"`
	Sats    int64  `json:"sats"`
}
type TxPreview struct {
	TXID       string   `json:"txid"`
	Bytes      int      `json:"bytes"`
	Chunks     int      `json:"chunks"`
	Outputs    []Output `json:"outputs"`
	OutputSats int64    `json:"output_sats"`
}

// Raw Bitcoin transactions do not encode a network. This structural check is
// complemented by the live LND mainnet check and full validation on publication.
func ValidateTransaction(raw []byte) (TxPreview, error) {
	p := TxPreview{Outputs: []Output{}}
	invalid := errors.New("invalid, unsigned or oversized transaction")
	if len(raw) == 0 || len(raw) > MaxContent {
		return p, invalid
	}
	r := bytes.NewReader(raw)
	var tx wire.MsgTx
	if tx.Deserialize(r) != nil || r.Len() != 0 || len(tx.TxIn) == 0 || len(tx.TxOut) == 0 {
		return p, invalid
	}
	seen := map[wire.OutPoint]bool{}
	for _, in := range tx.TxIn {
		if seen[in.PreviousOutPoint] || in.PreviousOutPoint.Hash == ([32]byte{}) || (len(in.SignatureScript) == 0 && len(in.Witness) == 0) {
			return p, invalid
		}
		seen[in.PreviousOutPoint] = true
	}
	for _, out := range tx.TxOut {
		if out.Value < 0 || out.Value > 21_000_000*100_000_000 || p.OutputSats > 21_000_000*100_000_000-out.Value {
			return p, invalid
		}
		p.OutputSats += out.Value
		_, addresses, _, _ := txscript.ExtractPkScriptAddrs(out.PkScript, &chaincfg.MainNetParams)
		address := "non-address script"
		if len(addresses) == 1 {
			address = addresses[0].EncodeAddress()
		}
		p.Outputs = append(p.Outputs, Output{address, out.Value})
	}
	p.TXID = tx.TxHash().String()
	p.Bytes = len(raw)
	p.Chunks = (len(raw) + ChunkSize - 1) / ChunkSize
	return p, nil
}
