package privileged

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

// Opt-in real RPC validation; this does not pretend to exercise Docker or the UI.
func TestLNbitsNativeRegtestMigration(t *testing.T) {
	if os.Getenv("LOS_LNBITS_REGTEST") != "1" {
		t.Skip("disposable regtest opt-in required")
	}
	host, _ := os.Hostname()
	marker, _ := os.ReadFile("/etc/los-regtest/LAB_ONLY")
	if host != "los-regtest" || string(marker) != "LOS_REGTEST_ONLY\n" {
		t.Fatal("not the isolated regtest lab")
	}
	const base = "/var/lib/los-regtest/alice"
	admin := newLNDManagerCredentialClient("127.0.0.1:10009", base+"/tls.cert", base+"/data/chain/bitcoin/regtest/admin.macaroon")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	state, err := admin.FinancialIntentNodeState(ctx)
	if err != nil || len(state.Chains) != 1 || state.Chains[0].Network != "regtest" {
		t.Fatal("regtest guard failed")
	}
	protected := map[string][]byte{}
	for _, p := range []string{base + "/tls.cert", base + "/tls.key", base + "/lnd.conf", base + "/data/chain/bitcoin/regtest/admin.macaroon"} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		protected[p] = raw
	}
	initial, err := admin.ListMacaroonIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root, err := lndclient.GenerateMacaroonRootKeyID(initial, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := admin.DeleteMacaroonID(c, root); err != nil {
			t.Error("test root cleanup failed:", err)
		}
	})
	permissions := []lndclient.MacaroonPermission{}
	for _, k := range legacyLNbitsTestKeys {
		p := strings.Split(k, ":")
		permissions = append(permissions, lndclient.MacaroonPermission{Entity: p[0], Action: p[1]})
	}
	bake := func(perms []lndclient.MacaroonPermission) []byte {
		t.Helper()
		r, e := admin.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{Permissions: perms, RootKeyID: root})
		if e != nil {
			t.Fatal(e)
		}
		raw, e := hex.DecodeString(r.MacaroonHex)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	old := bake(permissions)
	f := writeTestLNbitsFixture(t)
	mustWriteTestFile(t, f.macaroonPath, old, 0600)
	files, err := f.manager.validatedLNbitsFiles()
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := f.manager.createLNbitsSnapshot(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	f.manager.lnbitsRevocationRPC = admin
	if err := admin.AuthenticateMacaroon(ctx, old, permissions); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.RevokeLNbitsLegacyCredential(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateMacaroon(ctx, old, permissions); err != nil {
		t.Fatal("dry run revoked root")
	}
	if err := f.manager.RevokeLNbitsLegacyCredential(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateMacaroon(ctx, old, permissions); err == nil {
		t.Fatal("old copy still authenticates")
	}
	// Server migration rebakes the restricted policy with this root ID. The
	// completed receipt must not revoke its new secret on repeated requests.
	fresh := bake(permissions[:5])
	if err := f.manager.RevokeLNbitsLegacyCredential(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateMacaroon(ctx, fresh, permissions[:5]); err != nil {
		t.Fatal("replacement revoked on retry:", err)
	}
	if err := admin.AuthenticateMacaroon(ctx, old, permissions); err == nil {
		t.Fatal("old copy valid after rebake")
	}
	for p, before := range protected {
		after, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(before, after) {
			t.Fatal("native file changed:", filepath.Base(p))
		}
	}
	final, err := admin.ListMacaroonIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range initial {
		found := false
		for _, v := range final {
			found = found || v == id
		}
		if !found {
			t.Fatal("unrelated root removed")
		}
	}
	t.Log("Real regtest migration/rebake/retry passed; old copy rejected, new credential valid; native files and other roots preserved")
}
