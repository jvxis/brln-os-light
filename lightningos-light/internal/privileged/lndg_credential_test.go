package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"gopkg.in/macaroon.v2"
	"lightningos-light/internal/appmanifest"
	"lightningos-light/lnrpc"
)

func lndgRemovalFixture(t *testing.T) (*testLNDgFixture, *lnbitsRevokeTestRPC) {
	t.Helper()
	f := writeTestLNDgFixture(t)
	root := uint64(time.Now().UnixMilli())
	id := &lnrpc.MacaroonId{StorageId: []byte(strconv.FormatUint(root, 10)), Nonce: []byte("test")}
	for _, key := range []string{"address:write", "info:read", "invoices:read", "invoices:write", "message:write", "offchain:read", "offchain:write", "onchain:read", "onchain:write", "peers:read", "peers:write", "signer:generate", "signer:read"} {
		parts := strings.Split(key, ":")
		id.Ops = append(id.Ops, &lnrpc.Op{Entity: parts[0], Actions: []string{parts[1]}})
	}
	encoded, _ := proto.Marshal(id)
	mac, err := macaroon.New([]byte("unit-test-only"), append([]byte{3}, encoded...), "lnd", macaroon.V2)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := mac.MarshalBinary()
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
	rpc := &lnbitsRevokeTestRPC{root: root, present: true, authenticated: true}
	f.manager.lndgRevocationRPC = rpc
	return f, rpc
}

func TestLNDgRemovalRevokesBeforeDiscardAndPreservesData(t *testing.T) {
	f, rpc := lndgRemovalFixture(t)
	data := filepath.Join(f.dataRoot, "data", "keep.db")
	mustWriteTestFile(t, data, []byte("preserved"), 0600)
	if err := f.manager.Remove(context.Background(), appmanifest.LNDgID, true); err != nil {
		t.Fatal(err)
	}
	if rpc.deleted != 0 {
		t.Fatal("dry run revoked credential")
	}
	f.runner.hook = func(_ string, args []string) (string, error, bool) {
		for _, a := range args {
			if a == "down" && rpc.deleted != 0 {
				t.Fatal("revoked before down")
			}
		}
		return "", nil, false
	}
	if err := f.manager.Remove(context.Background(), appmanifest.LNDgID, false); err != nil {
		t.Fatal(err)
	}
	if rpc.deleted != 1 || rpc.present {
		t.Fatal("root not revoked")
	}
	if _, err := os.Stat(f.macaroonPath); !os.IsNotExist(err) {
		t.Fatal("revoked credential retained for reinstall")
	}
	if raw, err := os.ReadFile(data); err != nil || string(raw) != "preserved" {
		t.Fatal("data changed")
	}
	if _, err := os.Stat(f.adminMacaroonPath); err != nil {
		t.Fatal("admin credential changed")
	}
	if err := f.manager.Remove(context.Background(), appmanifest.LNDgID, false); err != nil {
		t.Fatal("retry after completed removal:", err)
	}
	if rpc.deleted != 1 {
		t.Fatal("repeat revocation")
	}
}

func TestLNDgRemovalDownFailureDoesNotRevoke(t *testing.T) {
	f, rpc := lndgRemovalFixture(t)
	f.runner.hook = func(_ string, args []string) (string, error, bool) {
		for _, a := range args {
			if a == "down" {
				return "", errors.New("injected failure"), true
			}
		}
		return "", nil, false
	}
	if f.manager.Remove(context.Background(), appmanifest.LNDgID, false) == nil {
		t.Fatal("expected down failure")
	}
	if rpc.deleted != 0 {
		t.Fatal("revoked after failed down")
	}
}

func TestLNDgRevocationInterruptionRetry(t *testing.T) {
	f, rpc := lndgRemovalFixture(t)
	p, err := f.manager.prepareLNDgCredentialRemoval(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = p.revoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Simulate crash before snapshot and credential cleanup.
	if err = f.manager.Remove(context.Background(), appmanifest.LNDgID, false); err != nil {
		t.Fatal(err)
	}
	if rpc.deleted != 1 {
		t.Fatal("revoked twice")
	}
}

func TestLNDgRemovalRejectsUnknownCredentialBeforeRuntimeChange(t *testing.T) {
	for _, kind := range []string{"malformed", "enrollment", "authentication"} {
		t.Run(kind, func(t *testing.T) {
			f, rpc := lndgRemovalFixture(t)
			switch kind {
			case "malformed":
				mustWriteTestFile(t, f.macaroonPath, []byte("unknown"), 0600)
			case "enrollment":
				mustWriteTestFile(t, filepath.Join(f.privilegedRoot, "lndg", "lnd", "lndg.macaroon"), []byte("different"), 0600)
			case "authentication":
				rpc.authenticated = false
			}
			before := len(f.runner.commands)
			if f.manager.Remove(context.Background(), appmanifest.LNDgID, false) == nil {
				t.Fatal("unsafe removal accepted")
			}
			if rpc.deleted != 0 || len(f.runner.commands) != before {
				t.Fatal("mutation before credential validation")
			}
		})
	}
}

type lndgFailOnceRevoker struct {
	*lnbitsRevokeTestRPC
	fail bool
}

func (r *lndgFailOnceRevoker) DeleteMacaroonID(ctx context.Context, root uint64) error {
	if r.fail {
		r.fail = false
		return errors.New("injected revocation failure")
	}
	return r.lnbitsRevokeTestRPC.DeleteMacaroonID(ctx, root)
}

func TestLNDgRemovalRevocationFailureKeepsRetryState(t *testing.T) {
	f, rpc := lndgRemovalFixture(t)
	f.manager.lndgRevocationRPC = &lndgFailOnceRevoker{lnbitsRevokeTestRPC: rpc, fail: true}
	if f.manager.Remove(context.Background(), appmanifest.LNDgID, false) == nil {
		t.Fatal("revocation failure hidden")
	}
	if !rpc.present {
		t.Fatal("unexpected revocation")
	}
	if _, err := os.Stat(f.macaroonPath); err != nil {
		t.Fatal("credential discarded before revocation")
	}
	if _, err := os.Stat(filepath.Join(f.privilegedRoot, "lndg")); err != nil {
		t.Fatal("enrollment discarded before revocation")
	}
	if err := f.manager.Remove(context.Background(), appmanifest.LNDgID, false); err != nil {
		t.Fatal(err)
	}
	if rpc.present || rpc.deleted != 1 {
		t.Fatal("retry did not revoke once")
	}
}

func TestLNDgRemovalDoesNotTrustRevokedReceiptForReusedRoot(t *testing.T) {
	f, rpc := lndgRemovalFixture(t)
	if err := f.manager.Remove(context.Background(), appmanifest.LNDgID, false); err != nil {
		t.Fatal(err)
	}
	rpc.present = true
	if f.manager.Remove(context.Background(), appmanifest.LNDgID, false) == nil {
		t.Fatal("accepted reused root")
	}
	if rpc.deleted != 1 {
		t.Fatal("deleted reused root")
	}
}

func TestLNDgRemovalRejectsUnsafeRootAndCaveats(t *testing.T) {
	for _, kind := range []string{"default-root", "legacy-root", "future-root", "caveat"} {
		t.Run(kind, func(t *testing.T) {
			f, rpc := lndgRemovalFixture(t)
			raw, err := os.ReadFile(f.macaroonPath)
			if err != nil {
				t.Fatal(err)
			}
			var mac macaroon.Macaroon
			if err := mac.UnmarshalBinary(raw); err != nil {
				t.Fatal(err)
			}
			if kind == "caveat" {
				if err := mac.AddFirstPartyCaveat([]byte("time-before 2099-01-01T00:00:00Z")); err != nil {
					t.Fatal(err)
				}
			} else {
				var id lnrpc.MacaroonId
				if err := proto.Unmarshal(mac.Id()[1:], &id); err != nil {
					t.Fatal(err)
				}
				root := uint64(0)
				switch kind {
				case "legacy-root":
					root = uint64(time.Now().Unix())
				case "future-root":
					root = uint64(time.Now().Add(48 * time.Hour).UnixMilli())
				}
				id.StorageId = []byte(strconv.FormatUint(root, 10))
				encoded, err := proto.Marshal(&id)
				if err != nil {
					t.Fatal(err)
				}
				replacement, err := macaroon.New([]byte("unit-test-only"), append([]byte{3}, encoded...), "lnd", macaroon.V2)
				if err != nil {
					t.Fatal(err)
				}
				mac = *replacement
				rpc.root = root
			}
			raw, err = mac.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			// Matching enrollment must not make an unsafe credential revocable.
			mustWriteTestFile(t, f.macaroonPath, raw, 0600)
			mustWriteTestFile(t, filepath.Join(f.privilegedRoot, "lndg", "lnd", "lndg.macaroon"), raw, 0600)
			before := len(f.runner.commands)
			if f.manager.Remove(context.Background(), appmanifest.LNDgID, false) == nil {
				t.Fatal("unsafe credential accepted")
			}
			if rpc.deleted != 0 || len(f.runner.commands) != before {
				t.Fatal("runtime changed before validation")
			}
		})
	}
}
