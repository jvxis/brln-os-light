package lndclient

import (
	"context"
	"encoding/hex"
	"os"
	"reflect"
	"testing"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/lnrpc"
)

// Explicitly opt-in, fixed disposable lab only. Never accepts arbitrary hosts,
// credentials or networks. Uses regtest credentials in place, never exports them.
func TestMacaroonAuthenticationRegtest(t *testing.T) {
	if os.Getenv("LOS_REGTEST_MACAROON_TEST") != "1" {
		t.Skip("dedicated native regtest lab required")
	}
	host, _ := os.Hostname()
	marker, err := os.ReadFile("/etc/los-regtest/LAB_ONLY")
	if err != nil || host != "los-regtest" || string(marker) != "LOS_REGTEST_ONLY\n" {
		t.Fatal("refusing non-lab host")
	}
	shared := false
	c := New(&config.Config{LND: config.LNDConfig{GRPCHost: "127.0.0.1:10009", TLSCertPath: "/var/lib/los-regtest/alice/tls.cert", AdminMacaroonPath: "/var/lib/los-regtest/alice/data/chain/bitcoin/regtest/admin.macaroon", SharedGRPC: &shared}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := c.dial(ctx, true)
	if err != nil {
		t.Fatal("lab LND unavailable")
	}
	defer conn.Close()
	info, err := lnrpc.NewLightningClient(conn).GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil || len(info.GetChains()) != 1 || info.Chains[0].Network != "regtest" {
		t.Fatal("refusing non-regtest LND")
	}
	before, err := c.ListMacaroonIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root, err := GenerateMacaroonRootKeyID(before, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := c.DeleteMacaroonID(cleanup, root); err != nil {
			t.Error("test root cleanup failed")
		}
		after, err := c.ListMacaroonIDs(cleanup)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Error("unrelated root IDs changed")
		}
	})
	permissions := []MacaroonPermission{{"info", "read"}, {"invoices", "read"}, {"invoices", "write"}, {"offchain", "read"}, {"offchain", "write"}}
	bake := func() []byte {
		result, err := c.BakeCustomMacaroon(ctx, BakeCustomMacaroonRequest{RootKeyID: root, Permissions: permissions})
		if err != nil {
			t.Fatal("native bake failed")
		}
		raw, err := hex.DecodeString(result.MacaroonHex)
		if err != nil {
			t.Fatal("native decode failed")
		}
		id, ops, err := InspectMacaroon(raw)
		if err != nil || id != root || len(ops) != 5 {
			t.Fatal("native identifier parsing failed")
		}
		if c.AuthenticateMacaroon(ctx, raw, permissions) != nil {
			t.Fatal("native CheckMacaroonPermissions failed")
		}
		return raw
	}
	old := bake()
	if c.AuthenticateMacaroon(ctx, old, []MacaroonPermission{{"onchain", "write"}}) == nil {
		t.Fatal("unexpected onchain authority")
	}
	if err := c.DeleteMacaroonID(ctx, root); err != nil {
		t.Fatal(err)
	}
	_ = bake()
	if c.AuthenticateMacaroon(ctx, old, permissions) == nil {
		t.Fatal("old credential remains valid")
	}
}
