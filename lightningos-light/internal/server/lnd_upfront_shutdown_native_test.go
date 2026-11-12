package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
)

// Operator-only: actual native parser/restart and channels on the isolated
// regtest lab. No production broker/config permission is enabled for the lab.
func TestUpfrontShutdownNativeRegtest(t *testing.T) {
	if os.Getenv("LOS_UPFRONT_REGTEST") != "1" {
		t.Skip("explicit disposable regtest opt-in required")
	}
	marker, err := os.ReadFile("/etc/los-regtest/LAB_ONLY")
	host, _ := os.Hostname()
	if err != nil || string(marker) != "LOS_REGTEST_ONLY\n" || host != "los-regtest" || os.Geteuid() != 0 {
		t.Fatal("not isolated regtest")
	}
	const dir = "/var/lib/los-regtest/alice"
	const path = dir + "/lnd.conf"
	original, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(original), "bitcoin.regtest=true") || strings.Contains(string(original), "bitcoin.mainnet=true") {
		t.Fatal("wrong native config")
	}
	client := lndclient.New(&config.Config{LND: config.LNDConfig{GRPCHost: "127.0.0.1:10009", TLSCertPath: dir + "/tls.cert", AdminMacaroonPath: dir + "/data/chain/bitcoin/regtest/admin.macaroon"}}, log.New(io.Discard, "", 0))
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rpc := func(node string, args ...string) (map[string]any, error) {
		port := "10009"
		if node == "bob" {
			port = "11009"
		}
		base := []string{"--lnddir=/var/lib/los-regtest/" + node, "--network=regtest", "--rpcserver=127.0.0.1:" + port}
		out, err := exec.CommandContext(ctx, "/opt/los-regtest/bin/lncli", append(base, args...)...).Output()
		if err != nil {
			if failed, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("lncli %s: %s", args[0], strings.TrimSpace(string(failed.Stderr)))
			}
			return nil, err
		}
		var value map[string]any
		err = json.Unmarshal(out, &value)
		return value, err
	}
	for _, node := range []string{"alice", "bob"} {
		info, err := rpc(node, "getinfo")
		if err != nil {
			t.Fatal("RPC baseline failed")
		}
		chains, ok := info["chains"].([]any)
		if !ok || len(chains) != 1 || chains[0].(map[string]any)["network"] != "regtest" {
			t.Fatal("non-regtest RPC")
		}
	}
	if err := exec.CommandContext(ctx, "python3", "/opt/los-regtest/control.py", "status").Run(); err != nil {
		t.Fatal("Bitcoin/lab baseline failed")
	}
	ops := (&Server{lnd: client}).lndConfigOperations()
	ops.path = path
	ops.read = func() ([]byte, error) { return os.ReadFile(path) }
	ops.write = func(b []byte) error { return writeLNDConfigBytes(path, b) }
	ops.restart = func(ctx context.Context) error {
		return exec.CommandContext(ctx, "systemctl", "restart", "los-regtest-alice").Run()
	}
	ops.readyTimeout = 45 * time.Second
	t.Cleanup(func() {
		if err := ops.write(original); err != nil {
			t.Error("restore config failed")
			return
		}
		cleanup, c := context.WithTimeout(context.Background(), time.Minute)
		defer c()
		if err := ops.restart(cleanup); err != nil {
			t.Error("restore restart failed")
			return
		}
		if _, err := waitLNDConfigReadiness(cleanup, ops, string(original)); err != nil {
			t.Error("restored LND not ready")
		}
	})
	apply := func(setting upfrontShutdownSetting) {
		raw, err := ops.read()
		if err != nil {
			t.Fatal(err)
		}
		updated, err := buildUpfrontShutdownConfig(string(raw), setting, &chaincfg.RegressionNetParams)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = applyLNDConfig(ctx, ops, raw, []byte(updated), true); err != nil {
			t.Fatal("native apply failed", err)
		}
	}
	mine := func() {
		if err := exec.CommandContext(ctx, "python3", "/opt/los-regtest/control.py", "mine", "6").Run(); err != nil {
			t.Fatal("mining failed")
		}
		time.Sleep(3 * time.Second)
	}
	baseline, err := rpc("alice", "listchannels")
	if err != nil {
		t.Fatal(err)
	}
	addresses := map[string]any{}
	for _, v := range baseline["channels"].([]any) {
		ch := v.(map[string]any)
		addresses[ch["channel_point"].(string)] = ch["close_address"]
	}
	for _, scenario := range []string{"wallet", "external", "external_incoming", "off"} {
		mode := scenario
		if scenario == "external_incoming" {
			mode = "external"
		}
		setting := upfrontShutdownSetting{Mode: mode}
		if mode == "external" {
			a, e := rpc("bob", "newaddress", "p2wkh")
			if e != nil {
				t.Fatal(e)
			}
			setting.Address = a["address"].(string)
		}
		apply(setting)
		mine()
		info, e := rpc("bob", "getinfo")
		if e != nil {
			t.Fatal(e)
		}
		// Wait for the existing localhost peer connection to recover after restart.
		_, _ = rpc("alice", "connect", info["identity_pubkey"].(string)+"@127.0.0.1:9835")
		opener, peer := "alice", info["identity_pubkey"].(string)
		if scenario == "external_incoming" {
			self, e := rpc("alice", "getinfo")
			if e != nil {
				t.Fatal(e)
			}
			opener, peer = "bob", self["identity_pubkey"].(string)
		}
		opened, e := rpc(opener, "openchannel", "--node_key="+peer, "--local_amt=150000", "--push_amt=20000", "--sat_per_vbyte=2")
		if e != nil {
			t.Fatal("opening failed", e)
		}
		txid := opened["funding_txid"].(string)
		mine()
		list, e := rpc("alice", "listchannels")
		if e != nil {
			t.Fatal(e)
		}
		point, address := "", ""
		for _, v := range list["channels"].([]any) {
			ch := v.(map[string]any)
			cp := ch["channel_point"].(string)
			if old, ok := addresses[cp]; ok && old != ch["close_address"] {
				t.Fatal("old channel destination changed")
			}
			if strings.HasPrefix(cp, txid+":") {
				point = cp
				address, _ = ch["close_address"].(string)
			}
		}
		if point == "" || mode == "off" && address != "" || mode == "wallet" && address == "" || mode == "external" && address != setting.Address {
			t.Fatal("negotiated destination mismatch", mode)
		}
		parts := strings.Split(point, ":")
		closeArgs := []string{"closechannel", "--funding_txid=" + parts[0], "--output_index=" + parts[1], "--sat_per_vbyte=10"}
		if mode != "off" {
			wrong, e := rpc("bob", "newaddress", "p2wkh")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = rpc("alice", append(append([]string{}, closeArgs...), "--delivery_addr="+wrong["address"].(string))...); e == nil {
				t.Fatal("different delivery address accepted")
			} else if !strings.Contains(e.Error(), "upfront shutdown") {
				t.Fatal("wrong destination rejected for unexpected reason", e)
			}
		}
		closed, e := rpc("alice", closeArgs...)
		if e != nil {
			t.Fatal("cooperative close failed", e)
		}
		if mode != "off" {
			// Inspect the actual broadcast output, not only LND's channel metadata.
			out, e := exec.CommandContext(ctx, "/opt/los-regtest/bin/bitcoin-cli", "-datadir=/var/lib/los-regtest/bitcoin", "-regtest", "getrawtransaction", closed["closing_txid"].(string), "true").Output()
			if e != nil {
				t.Fatal("cannot inspect closing transaction", e)
			}
			var tx struct {
				Vout []struct {
					ScriptPubKey struct {
						Address string `json:"address"`
					} `json:"scriptPubKey"`
				} `json:"vout"`
			}
			if e = json.Unmarshal(out, &tx); e != nil {
				t.Fatal(e)
			}
			found := false
			for _, v := range tx.Vout {
				if v.ScriptPubKey.Address == address {
					found = true
				}
			}
			if !found {
				t.Fatal("actual close transaction lacks committed destination")
			}
		}
		mine()
		t.Log("PASS native config/open/committed address/close:", scenario)
	}
}
