package privileged

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
)

// Native LND authentication/revocation, with recorded (not executed) Docker
// commands. This is deliberately NOT an App Store end-to-end installation test.
func TestLNDgNativeRegtestCredentialLifecycle(t *testing.T) {
	if os.Getenv("LOS_LNDG_REGTEST") != "1" {
		t.Skip("explicit disposable regtest opt-in required")
	}
	hostname, _ := os.Hostname()
	marker, _ := os.ReadFile("/etc/los-regtest/LAB_ONLY")
	if hostname != "los-regtest" || string(marker) != "LOS_REGTEST_ONLY\n" {
		t.Fatal("not the dedicated regtest laboratory")
	}
	const base = "/var/lib/los-regtest/alice"
	const tls = base + "/tls.cert"
	const adminPath = base + "/data/chain/bitcoin/regtest/admin.macaroon"
	admin := newLNDManagerCredentialClient("127.0.0.1:10009", tls, adminPath)
	defer admin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	info, err := admin.FinancialIntentNodeState(ctx)
	if err != nil {
		t.Fatal("native GetInfo failed:", err)
	}
	if len(info.Chains) != 1 || info.Chains[0].Chain != "bitcoin" || info.Chains[0].Network != "regtest" {
		t.Fatal("refusing non-regtest LND")
	}
	initial, err := admin.ListMacaroonIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	protected := map[string][]byte{}
	for _, path := range []string{tls, base + "/tls.key", base + "/lnd.conf", adminPath} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		protected[path] = raw
	}
	f, _ := lndgRemovalFixture(t)
	f.manager.lndgRevocationRPC = admin
	original, err := os.ReadFile(f.macaroonPath)
	if err != nil {
		t.Fatal(err)
	}
	_, permissions, err := lndclient.InspectMacaroon(original)
	if err != nil {
		t.Fatal(err)
	}
	bake := func(perms []lndclient.MacaroonPermission) ([]byte, uint64) {
		t.Helper()
		ids, err := admin.ListMacaroonIDs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		root, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		result, err := admin.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{Permissions: perms, RootKeyID: root})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			ids, err := admin.ListMacaroonIDs(cleanup)
			if err != nil {
				t.Error("cleanup inventory failed")
				return
			}
			for _, id := range ids {
				if id == root {
					if err := admin.DeleteMacaroonID(cleanup, root); err != nil {
						t.Error("test root cleanup failed")
					}
				}
			}
		})
		raw, err := hex.DecodeString(result.MacaroonHex)
		if err != nil {
			t.Fatal(err)
		}
		return raw, root
	}
	enroll := func(raw []byte) {
		t.Helper()
		mustWriteTestFile(t, f.macaroonPath, raw, 0600)
		files, err := f.manager.validatedLNDgFiles()
		if err != nil {
			t.Fatal(err)
		}
		_, cleanup, err := f.manager.createLNDgSnapshot(files)
		if err != nil {
			t.Fatal(err)
		}
		cleanup()
	}
	old, oldRoot := bake(permissions)
	enroll(old)
	copyPath := filepath.Join(t.TempDir(), "copied.macaroon")
	mustWriteTestFile(t, copyPath, old, 0600)
	copiedClient := newLNDManagerCredentialClient("127.0.0.1:10009", tls, copyPath)
	if _, err := copiedClient.FinancialIntentNodeState(ctx); err != nil {
		t.Fatal("copied credential failed before revocation:", err)
	}
	otherPerms := []lndclient.MacaroonPermission{{Entity: "info", Action: "read"}}
	other, otherRoot := bake(otherPerms)
	if err := f.manager.Remove(ctx, appmanifest.LNDgID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := copiedClient.FinancialIntentNodeState(ctx); err != nil {
		t.Fatal("dry-run revoked credential")
	}
	if err := f.manager.Remove(ctx, appmanifest.LNDgID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := copiedClient.FinancialIntentNodeState(ctx); err == nil {
		t.Fatal("copied credential still authenticates after removal")
	}
	if err := admin.AuthenticateMacaroon(ctx, other, otherPerms); err != nil {
		t.Fatal("other app root affected:", err)
	}
	fresh, freshRoot := bake(permissions)
	if freshRoot == oldRoot || freshRoot == otherRoot {
		t.Fatal("root reused")
	}
	enroll(fresh)
	if err := admin.AuthenticateMacaroon(ctx, fresh, permissions); err != nil {
		t.Fatal("fresh credential rejected:", err)
	}
	if err := f.manager.Remove(ctx, appmanifest.LNDgID, false); err != nil {
		t.Fatal("second removal failed:", err)
	}
	for _, scenario := range []string{"down-failure", "revoke-failure", "interrupted-cleanup", "missing-enrollment"} {
		t.Run(scenario, func(t *testing.T) {
			raw, _ := bake(permissions)
			enroll(raw)
			switch scenario {
			case "down-failure":
				f.runner.hook = func(_ string, args []string) (string, error, bool) {
					for _, arg := range args {
						if arg == "down" {
							return "", errors.New("injected Docker down failure"), true
						}
					}
					return "", nil, false
				}
				if f.manager.Remove(ctx, appmanifest.LNDgID, false) == nil {
					t.Fatal("down failure ignored")
				}
				f.runner.hook = nil
				if err := admin.AuthenticateMacaroon(ctx, raw, permissions); err != nil {
					t.Fatal("revoked before down")
				}
			case "revoke-failure":
				f.manager.lndgRevocationRPC = &nativeLNDgFailedDelete{lnbitsRevocationRPC: admin}
				if f.manager.Remove(ctx, appmanifest.LNDgID, false) == nil {
					t.Fatal("revocation failure ignored")
				}
				f.manager.lndgRevocationRPC = admin
				if err := admin.AuthenticateMacaroon(ctx, raw, permissions); err != nil {
					t.Fatal("credential lost before successful revocation")
				}
			case "interrupted-cleanup":
				p, err := f.manager.prepareLNDgCredentialRemoval(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := p.revoke(ctx); err != nil {
					t.Fatal(err)
				}
				if err := admin.AuthenticateMacaroon(ctx, raw, permissions); err == nil {
					t.Fatal("old credential still accepted")
				}
			case "missing-enrollment":
				path := filepath.Join(f.privilegedRoot, "lndg", "lnd", "lndg.macaroon")
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				before := len(f.runner.commands)
				if f.manager.Remove(ctx, appmanifest.LNDgID, false) == nil {
					t.Fatal("missing enrollment accepted")
				}
				if len(f.runner.commands) != before {
					t.Fatal("Docker mutated before validation")
				}
				if err := admin.AuthenticateMacaroon(ctx, raw, permissions); err != nil {
					t.Fatal("unenrolled credential revoked")
				}
				if err := os.Rename(path+".saved", path); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.manager.Remove(ctx, appmanifest.LNDgID, false); err != nil {
				t.Fatal("retry failed:", err)
			}
			if err := admin.AuthenticateMacaroon(ctx, raw, permissions); err == nil {
				t.Fatal("copied credential accepted after retry")
			}
		})
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
			t.Fatal("pre-existing root removed")
		}
	}
	for path, before := range protected {
		after, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("native protected file changed:", filepath.Base(path))
		}
	}
	t.Log("Real regtest LND: old copied credential rejected; new root accepted; second removal succeeded; unrelated roots, TLS, config and admin unchanged. Docker calls were recorded, not executed.")
}

type nativeLNDgFailedDelete struct{ lnbitsRevocationRPC }

func (*nativeLNDgFailedDelete) DeleteMacaroonID(context.Context, uint64) error {
	return errors.New("injected unavailable revocation RPC")
}
