package lndclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"
)

type opreturnFixtureMacaroon string

func (m opreturnFixtureMacaroon) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"macaroon": string(m)}, nil
}
func (opreturnFixtureMacaroon) RequireTransportSecurity() bool { return true }

// The fixture must be disposable, funded, and use the fixed local regtest
// ports/container below. The production entry point remains mainnet-only;
// only this package-level integration test invokes the lower-level funder.
func TestOPReturnRegtestEndToEnd(t *testing.T) {
	if os.Getenv("LIGHTNINGOS_OPRETURN_REGTEST") != "1" {
		t.Skip("disposable regtest fixture not requested")
	}
	dir := "/tmp/los-opreturn-137-lab/lnd"
	tls, err := credentials.NewClientTLSFromFile(filepath.Join(dir, "tls.cert"), "")
	if err != nil {
		t.Fatal(err)
	}
	mac, err := os.ReadFile(filepath.Join(dir, "data/chain/bitcoin/regtest/admin.macaroon"))
	if err != nil {
		t.Fatal("fixture macaroon unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "127.0.0.1:19009", grpc.WithTransportCredentials(tls), grpc.WithPerRPCCredentials(opreturnFixtureMacaroon(hex.EncodeToString(mac))))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	info, err := lnrpc.NewLightningClient(conn).GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Chains) != 1 || info.Chains[0].Network != "regtest" || !info.SyncedToChain {
		t.Fatal("refusing to use a non-regtest or unsynchronized fixture")
	}
	kit := walletrpc.NewWalletKitClient(conn)
	before, err := kit.ListLeases(ctx, &walletrpc.ListLeasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	f, err := fundOPReturn(ctx, kit, "LightningOS ação 😀", 1)
	if err != nil {
		t.Fatal("preview funding", err)
	}
	quote := f.Quote
	if err = f.Release(); err != nil {
		t.Fatal(err)
	}
	after, err := kit.ListLeases(ctx, &walletrpc.ListLeasesRequest{})
	if err != nil || len(after.LockedUtxos) != len(before.LockedUtxos) {
		t.Fatal("preview leaked leases")
	}
	f, err = fundOPReturn(ctx, kit, quote.Text, quote.SatPerVbyte)
	if err != nil {
		t.Fatal("publish funding", err)
	}
	defer f.Release()
	if f.Quote.FeeSat > quote.FeeSat {
		t.Fatal("fee exceeded approval")
	}
	txid, raw, err := f.Finalize(ctx)
	if err != nil {
		t.Fatal("finalize", err)
	}
	txbytes, _ := hex.DecodeString(raw)
	var tx wire.MsgTx
	if err = tx.Deserialize(bytes.NewReader(txbytes)); err != nil {
		t.Fatal(err)
	}
	script, _ := OPReturnScript(quote.Text)
	nulldata := 0
	for _, out := range tx.TxOut {
		if bytes.Equal(out.PkScript, script) {
			nulldata++
			if out.Value != 0 {
				t.Fatal("nonzero nulldata")
			}
		}
	}
	if nulldata != 1 || tx.TxHash().String() != txid {
		t.Fatal("transaction payload or TXID mismatch")
	}
	resp, err := kit.PublishTransaction(ctx, &walletrpc.Transaction{TxHex: txbytes, Label: "lightningos:opreturn:regtest"})
	if err != nil || resp.PublishError != "" {
		t.Fatal("publish rejected", err, resp.GetPublishError())
	}
	// bitcoin-cli uses the fixture's config/cookie internally, never production
	// RPC credentials. The container name is fixed to avoid accidental targeting.
	cli := func(args ...string) []byte {
		base := []string{"exec", "los-opreturn-137-bitcoin", "bitcoin-cli", "-datadir=/regtest"}
		command := "docker"
		if os.Getenv("LIGHTNINGOS_OPRETURN_REGTEST_BACKEND") == "native" {
			command = "/tmp/los-opreturn-137-lab/bitcoin-cli"
			base = []string{"-datadir=/tmp/los-opreturn-137-lab/bitcoin"}
		}
		out, err := exec.CommandContext(ctx, command, append(base, args...)...).Output()
		if err != nil {
			t.Fatal("fixture bitcoin-cli failed", err)
		}
		return out
	}
	var chain struct {
		Chain string `json:"chain"`
	}
	_ = json.Unmarshal(cli("getblockchaininfo"), &chain)
	if chain.Chain != "regtest" {
		t.Fatal("refusing to mine outside regtest")
	}
	addr := strings.TrimSpace(string(cli("getnewaddress")))
	cli("generatetoaddress", "1", addr)
	for i := 0; i < 30; i++ {
		transaction, err := kit.GetTransaction(ctx, &walletrpc.GetTransactionRequest{Txid: txid})
		if err == nil && transaction.NumConfirmations > 0 {
			if transaction.TotalFees != quote.FeeSat || transaction.BlockHeight <= 0 {
				t.Fatal("incorrect confirmed fee or block height")
			}
			t.Logf("regtest publication confirmed: bytes=%d fee=%d inputs=%d vbytes=%d confirmations=%d", quote.ByteCount, quote.FeeSat, quote.SelectedInputCount, quote.EstimatedVbytes, transaction.NumConfirmations)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("publication confirmation was not observed")
}
