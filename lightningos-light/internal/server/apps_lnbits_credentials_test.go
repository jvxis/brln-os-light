package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"gopkg.in/macaroon.v2"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

type lnbitsTestRPC struct {
	keys                 map[uint64][]byte
	deletes, bakes       int
	failBake, failDelete bool
}

func lnbitsTestMacaroon(t *testing.T, root uint64, permissions []lndclient.MacaroonPermission, secret []byte) []byte {
	t.Helper()
	id := &lnrpc.MacaroonId{StorageId: []byte(strconv.FormatUint(root, 10)), Nonce: []byte("test-nonce")}
	for _, p := range permissions {
		id.Ops = append(id.Ops, &lnrpc.Op{Entity: p.Entity, Actions: []string{p.Action}})
	}
	encoded, err := proto.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := macaroon.New(secret, append([]byte{3}, encoded...), "lnd", macaroon.V2)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mac.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *lnbitsTestRPC) ListMacaroonIDs(context.Context) ([]uint64, error) {
	var ids []uint64
	for id := range f.keys {
		ids = append(ids, id)
	}
	return ids, nil
}

func (f *lnbitsTestRPC) DeleteMacaroonID(_ context.Context, root uint64) error {
	if f.failDelete {
		return errors.New("delete failed")
	}
	f.deletes++
	delete(f.keys, root)
	return nil
}

func (f *lnbitsTestRPC) AuthenticateMacaroon(_ context.Context, raw []byte, requested []lndclient.MacaroonPermission) error {
	root, permissions, err := lndclient.InspectMacaroon(raw)
	if err != nil {
		return err
	}
	key, found := f.keys[root]
	if !found {
		return errors.New("root revoked")
	}
	var mac macaroon.Macaroon
	if mac.UnmarshalBinary(raw) != nil || mac.Verify(key, nil, nil) != nil {
		return errors.New("signature invalid")
	}
	for _, want := range requested {
		found := false
		for _, have := range permissions {
			if want == have {
				found = true
			}
		}
		if !found {
			return errors.New("permission denied")
		}
	}
	return nil
}

func (f *lnbitsTestRPC) BakeCustomMacaroon(_ context.Context, req lndclient.BakeCustomMacaroonRequest) (lndclient.BakeCustomMacaroonResult, error) {
	f.bakes++
	if f.failBake {
		return lndclient.BakeCustomMacaroonResult{}, errors.New("bake failed")
	}
	key := f.keys[req.RootKeyID]
	if key == nil {
		key = []byte("replacement-secret-" + strconv.Itoa(f.bakes))
		f.keys[req.RootKeyID] = key
	}
	id := &lnrpc.MacaroonId{StorageId: []byte(strconv.FormatUint(req.RootKeyID, 10)), Nonce: []byte("new-nonce")}
	for _, p := range req.Permissions {
		id.Ops = append(id.Ops, &lnrpc.Op{Entity: p.Entity, Actions: []string{p.Action}})
	}
	encoded, _ := proto.Marshal(id)
	mac, err := macaroon.New(key, append([]byte{3}, encoded...), "lnd", macaroon.V2)
	if err != nil {
		return lndclient.BakeCustomMacaroonResult{}, err
	}
	raw, err := mac.MarshalBinary()
	return lndclient.BakeCustomMacaroonResult{MacaroonHex: hex.EncodeToString(raw)}, err
}

func lnbitsMigrationFixture(t *testing.T) (lnbitsPaths, *lnbitsTestRPC, []byte) {
	t.Helper()
	dir := t.TempDir()
	paths := lnbitsPaths{Root: filepath.Join(dir, "apps", "lnbits"), LndDir: filepath.Join(dir, "data", "lnd")}
	paths.MacaroonPath = filepath.Join(paths.LndDir, "lnbits.macaroon")
	for _, p := range []string{paths.Root, paths.LndDir} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	root := uint64(time.Now().Add(-time.Hour).UnixMilli())
	rpc := &lnbitsTestRPC{keys: map[uint64][]byte{root: []byte("legacy-secret"), 0: []byte("native-secret"), 42: []byte("other-app-secret")}}
	raw := lnbitsTestMacaroon(t, root, lnbitsLegacyPermissions(), rpc.keys[root])
	if err := os.WriteFile(paths.MacaroonPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return paths, rpc, raw
}

func TestLNbitsCredentialMigrationRevokesOldCopyAndPreservesWallet(t *testing.T) {
	for _, status := range []string{"running", "stopped"} {
		t.Run(status, func(t *testing.T) {
			paths, rpc, old := lnbitsMigrationFixture(t)
			var calls []string
			runtime := lnbitsCredentialRuntime{inspect: func(context.Context) (string, error) { return status, nil }, lifecycle: func(_ context.Context, a string) error { calls = append(calls, a); return nil }}
			ctx := context.Background()
			if err := ensureLNbitsWalletCredential(ctx, paths, rpc, runtime); err != nil {
				t.Fatal(err)
			}
			current, err := os.ReadFile(paths.MacaroonPath)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(current, old) || rpc.deletes != 1 {
				t.Fatal("legacy credential was not rotated")
			}
			if rpc.AuthenticateMacaroon(ctx, old, lnbitsLegacyPermissions()) == nil {
				t.Fatal("leaked copy still valid")
			}
			if err := rpc.AuthenticateMacaroon(ctx, current, lnbitsMacaroonPermissions()); err != nil {
				t.Fatal(err)
			}
			for _, blocked := range []lndclient.MacaroonPermission{{Entity: "onchain", Action: "write"}, {Entity: "peers", Action: "write"}, {Entity: "macaroon", Action: "write"}} {
				if rpc.AuthenticateMacaroon(ctx, current, []lndclient.MacaroonPermission{blocked}) == nil {
					t.Fatalf("unexpected authority: %v", blocked)
				}
			}
			want := []string{"stop"}
			if status == "running" {
				want = append(want, "start")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("lifecycle=%v", calls)
			}
			if string(rpc.keys[0]) != "native-secret" || string(rpc.keys[42]) != "other-app-secret" {
				t.Fatal("unrelated root modified")
			}
			if _, err := os.Stat(lnbitsMigrationPath(paths)); !os.IsNotExist(err) {
				t.Fatal("journal not finalized")
			}
			if err := ensureLNbitsWalletCredential(ctx, paths, rpc, runtime); err != nil {
				t.Fatal(err)
			}
			if rpc.deletes != 1 || rpc.bakes != 1 || !reflect.DeepEqual(calls, want) {
				t.Fatal("second run was not idempotent")
			}
		})
	}
}

func TestLNbitsCredentialMigrationRetriesWithoutRestoringLegacyAuthority(t *testing.T) {
	for _, stage := range []string{"stop", "delete", "bake", "start"} {
		t.Run(stage, func(t *testing.T) {
			paths, rpc, old := lnbitsMigrationFixture(t)
			fail := true
			rpc.failDelete = stage == "delete"
			rpc.failBake = stage == "bake"
			runtime := lnbitsCredentialRuntime{inspect: func(context.Context) (string, error) { return "running", nil }, lifecycle: func(_ context.Context, a string) error {
				if fail && a == stage {
					return errors.New("injected failure")
				}
				return nil
			}}
			if err := ensureLNbitsWalletCredential(context.Background(), paths, rpc, runtime); err == nil {
				t.Fatal("expected failure")
			}
			if _, err := os.Stat(lnbitsMigrationPath(paths)); err != nil {
				t.Fatal("missing recovery journal")
			}
			fail = false
			rpc.failDelete = false
			rpc.failBake = false
			if err := ensureLNbitsWalletCredential(context.Background(), paths, rpc, runtime); err != nil {
				t.Fatal(err)
			}
			if rpc.AuthenticateMacaroon(context.Background(), old, lnbitsLegacyPermissions()) == nil {
				t.Fatal("legacy credential restored")
			}
			if rpc.deletes != 1 {
				t.Fatalf("unexpected revocations: %d", rpc.deletes)
			}
		})
	}
}

func TestLNbitsCredentialMigrationRejectsUntrustedState(t *testing.T) {
	for _, kind := range []string{"admin", "custom", "shared-id", "malformed", "signature", "caveat", "unknown-runtime"} {
		t.Run(kind, func(t *testing.T) {
			paths, rpc, raw := lnbitsMigrationFixture(t)
			root, _, _ := lndclient.InspectMacaroon(raw)
			switch kind {
			case "admin":
				raw = lnbitsTestMacaroon(t, 0, lnbitsLegacyPermissions(), rpc.keys[0])
			case "shared-id":
				raw = lnbitsTestMacaroon(t, 42, lnbitsLegacyPermissions(), rpc.keys[42])
			case "custom":
				raw = lnbitsTestMacaroon(t, root, []lndclient.MacaroonPermission{{Entity: "info", Action: "read"}}, rpc.keys[root])
			case "malformed":
				raw = []byte("malformed")
			case "signature":
				raw = lnbitsTestMacaroon(t, root, lnbitsLegacyPermissions(), []byte("forged"))
			case "caveat":
				var m macaroon.Macaroon
				_ = m.UnmarshalBinary(raw)
				_ = m.AddFirstPartyCaveat([]byte("time-before 2030-01-01T00:00:00Z"))
				raw, _ = m.MarshalBinary()
			}
			if err := os.WriteFile(paths.MacaroonPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			runtime := lnbitsCredentialRuntime{inspect: func(context.Context) (string, error) { return "unknown", nil }, lifecycle: func(context.Context, string) error { t.Fatal("unexpected lifecycle mutation"); return nil }}
			if err := ensureLNbitsWalletCredential(context.Background(), paths, rpc, runtime); err == nil {
				t.Fatal("expected rejection")
			}
			current, _ := os.ReadFile(paths.MacaroonPath)
			if !bytes.Equal(current, raw) || rpc.deletes != 0 || rpc.bakes != 0 {
				t.Fatal("unsafe state changed")
			}
		})
	}
}

func TestLNbitsFreshCredential(t *testing.T) {
	paths, rpc, _ := lnbitsMigrationFixture(t)
	if err := os.Remove(paths.MacaroonPath); err != nil {
		t.Fatal(err)
	}
	if err := ensureLNbitsWalletCredential(context.Background(), paths, rpc, lnbitsCredentialRuntime{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(paths.MacaroonPath)
	_, permissions, err := lndclient.InspectMacaroon(raw)
	if err != nil || !lnbitsPermissionsEqual(permissions, lnbitsMacaroonPermissions()) || rpc.deletes != 0 {
		t.Fatal("incorrect fresh credential")
	}
}

func TestLNbitsExplicitStopCancelsMigrationResume(t *testing.T) {
	paths, rpc, _ := lnbitsMigrationFixture(t)
	rpc.failBake = true
	var calls []string
	r := lnbitsCredentialRuntime{inspect: func(context.Context) (string, error) { return "running", nil }, lifecycle: func(_ context.Context, a string) error { calls = append(calls, a); return nil }}
	if ensureLNbitsWalletCredential(context.Background(), paths, rpc, r) == nil {
		t.Fatal("expected interrupted migration")
	}
	if err := pauseLNbitsCredentialMigration(paths); err != nil {
		t.Fatal(err)
	}
	rpc.failBake = false
	if err := ensureLNbitsWalletCredential(context.Background(), paths, rpc, r); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if call == "start" {
			t.Fatal("explicitly stopped app restarted")
		}
	}
}

func TestLNbitsMigrationMissingCredentialFailsClosed(t *testing.T) {
	paths, rpc, _ := lnbitsMigrationFixture(t)
	rpc.failBake = true
	r := lnbitsCredentialRuntime{inspect: func(context.Context) (string, error) { return "running", nil }, lifecycle: func(context.Context, string) error { return nil }}
	if ensureLNbitsWalletCredential(context.Background(), paths, rpc, r) == nil {
		t.Fatal("expected interruption")
	}
	if err := os.Remove(paths.MacaroonPath); err != nil {
		t.Fatal(err)
	}
	rpc.failBake = false
	bakes := rpc.bakes
	if ensureLNbitsWalletCredential(context.Background(), paths, rpc, r) == nil {
		t.Fatal("missing credential should require review")
	}
	if rpc.bakes != bakes {
		t.Fatal("lost migration state was silently replaced")
	}
}

func TestLNbitsCredentialPermissionOrderIndependent(t *testing.T) {
	a := lnbitsMacaroonPermissions()
	b := append([]lndclient.MacaroonPermission(nil), a...)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	if !lnbitsPermissionsEqual(a, b) {
		t.Fatal("equivalent permission order rejected")
	}
}
