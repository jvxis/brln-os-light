package privileged

import (
	"context"
	"encoding/json"
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
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

type lnbitsRevokeTestRPC struct {
	root                   uint64
	present, authenticated bool
	deleted                int
}

func (r *lnbitsRevokeTestRPC) ListMacaroonIDs(context.Context) ([]uint64, error) {
	if r.present {
		return []uint64{0, 42, r.root}, nil
	}
	return []uint64{0, 42}, nil
}
func (r *lnbitsRevokeTestRPC) AuthenticateMacaroon(context.Context, []byte, []lndclient.MacaroonPermission) error {
	if !r.authenticated {
		return errors.New("invalid signature")
	}
	return nil
}
func (r *lnbitsRevokeTestRPC) DeleteMacaroonID(_ context.Context, id uint64) error {
	if id != r.root || id == 0 {
		return errors.New("wrong root")
	}
	r.deleted++
	r.present = false
	return nil
}

func lnbitsRevokeFixture(t *testing.T, root uint64, keys []string) (*ComposeAppManager, *lnbitsRevokeTestRPC) {
	t.Helper()
	dir := t.TempDir()
	m := &ComposeAppManager{AppsDataRoot: filepath.Join(dir, "data"), PrivilegedAppsRoot: filepath.Join(dir, "private")}
	credentialDir := filepath.Join(m.AppsDataRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir)
	if err := os.MkdirAll(credentialDir, 0700); err != nil {
		t.Fatal(err)
	}
	metadata := &lnrpc.MacaroonId{StorageId: []byte(strconv.FormatUint(root, 10)), Nonce: []byte("fixture")}
	for _, key := range keys {
		p := strings.SplitN(key, ":", 2)
		metadata.Ops = append(metadata.Ops, &lnrpc.Op{Entity: p[0], Actions: []string{p[1]}})
	}
	encoded, _ := proto.Marshal(metadata)
	mac, err := macaroon.New([]byte("fixture-only"), append([]byte{3}, encoded...), "lnd", macaroon.V2)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mac.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(credentialDir, appmanifest.LNbitsMacaroonFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	snapshotDir := filepath.Join(m.PrivilegedAppsRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir)
	if err := os.MkdirAll(snapshotDir, 0700); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(snapshotDir, appmanifest.LNbitsMacaroonFile)
	if err := os.WriteFile(snapshotPath, raw, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(snapshotPath, 0640); err != nil {
		t.Fatal(err)
	}
	if err := setPrivilegedPathGroup(snapshotPath, appmanifest.LNbitsContainerGID); err != nil {
		t.Fatal(err)
	}
	rpc := &lnbitsRevokeTestRPC{root: root, present: true, authenticated: true}
	m.lnbitsRevocationRPC = rpc
	return m, rpc
}

var legacyLNbitsTestKeys = []string{"info:read", "invoices:read", "invoices:write", "offchain:read", "offchain:write", "onchain:read", "onchain:write", "peers:read", "peers:write"}

func TestLNbitsBrokerRevocationAcceptsActualSnapshot(t *testing.T) {
	m, rpc := lnbitsRevokeFixture(t, uint64(time.Now().Add(-time.Hour).UnixMilli()), legacyLNbitsTestKeys)
	raw, err := os.ReadFile(filepath.Join(m.AppsDataRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir, appmanifest.LNbitsMacaroonFile))
	if err != nil {
		t.Fatal(err)
	}
	f := writeTestLNbitsFixture(t)
	mustWriteTestFile(t, f.macaroonPath, raw, 0600)
	files, err := f.manager.validatedLNbitsFiles()
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := f.manager.createLNbitsSnapshot(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	f.manager.lnbitsRevocationRPC = rpc
	if err := f.manager.RevokeLNbitsLegacyCredential(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if rpc.deleted != 1 {
		t.Fatal("actual enrolled credential was not revoked")
	}
}

type lnbitsRecordingApps struct {
	*recordingApps
	revoked      int
	revokeDryRun bool
}

func (a *lnbitsRecordingApps) RevokeLNbitsLegacyCredential(_ context.Context, dryRun bool) error {
	a.revoked++
	a.revokeDryRun = dryRun
	return a.err
}

func TestLNbitsRevocationBrokerUsesMutationLock(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		a := &lnbitsRecordingApps{recordingApps: &recordingApps{}}
		lock := &recordingLocker{}
		broker := testBroker(&recordingRunner{}, &recordingAudit{}, lock)
		broker.Apps = a
		response := broker.Handle(context.Background(), emptyParamsRequest(t, OperationLNbitsLegacyCredentialRevoke, dryRun))
		if !response.OK || a.revoked != 1 || a.revokeDryRun != dryRun {
			t.Fatalf("bad response: %#v", response)
		}
		wantLocks := 1
		if dryRun {
			wantLocks = 0
		}
		if lock.locks != wantLocks || lock.unlocks != wantLocks {
			t.Fatal("revocation lock bypassed")
		}
	}
}

func TestLNbitsBrokerRevocationRequiresMatchingEnrollment(t *testing.T) {
	m, rpc := lnbitsRevokeFixture(t, uint64(time.Now().Add(-time.Hour).UnixMilli()), legacyLNbitsTestKeys)
	path := filepath.Join(m.PrivilegedAppsRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir, appmanifest.LNbitsMacaroonFile)
	if err := os.WriteFile(path, []byte("different-enrolled-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if m.RevokeLNbitsLegacyCredential(context.Background(), false) == nil || rpc.deleted != 0 {
		t.Fatal("unenrolled credential revoked")
	}
}

func TestLNbitsBrokerRevocationDryRunAndIdempotence(t *testing.T) {
	m, rpc := lnbitsRevokeFixture(t, uint64(time.Now().Add(-time.Hour).UnixMilli()), legacyLNbitsTestKeys)
	if err := m.RevokeLNbitsLegacyCredential(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if rpc.deleted != 0 {
		t.Fatal("dry run revoked root")
	}
	if _, err := os.Stat(filepath.Join(m.PrivilegedAppsRoot, "lnbits-credential-revocation.json")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote receipt")
	}
	if err := m.RevokeLNbitsLegacyCredential(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if rpc.deleted != 1 {
		t.Fatal("legacy root not revoked")
	}
	// The ID can now contain a new restricted secret after a server-side bake.
	// A retry must not delete that replacement root.
	rpc.present = true
	rpc.authenticated = false
	if err := m.RevokeLNbitsLegacyCredential(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if rpc.deleted != 1 {
		t.Fatal("replacement root revoked during retry")
	}
}

func TestLNbitsBrokerRevocationRejectsOtherCredentials(t *testing.T) {
	root := uint64(time.Now().Add(-time.Hour).UnixMilli())
	for _, tc := range []struct {
		name string
		root uint64
		keys []string
		auth bool
	}{
		{"default", 0, legacyLNbitsTestKeys, true}, {"shared", 42, legacyLNbitsTestKeys, true},
		{"wallet-only", root, legacyLNbitsTestKeys[:5], true}, {"forged", root, legacyLNbitsTestKeys, false},
		{"admin-authority", root, append(append([]string(nil), legacyLNbitsTestKeys...), "macaroon:write"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, rpc := lnbitsRevokeFixture(t, tc.root, tc.keys)
			rpc.authenticated = tc.auth
			if m.RevokeLNbitsLegacyCredential(context.Background(), false) == nil {
				t.Fatal("unsafe revocation accepted")
			}
			if rpc.deleted != 0 {
				t.Fatal("unrelated root revoked")
			}
		})
	}
}

func TestLNbitsBrokerRevocationProtocolHasNoCallerSelectedTarget(t *testing.T) {
	for _, params := range []string{`{"root_key_id":0}`, `{"path":"/tmp/other"}`, `{"app_id":"lndg"}`, `{"macaroon":"secret"}`} {
		req := Request{Version: ProtocolVersion, RequestID: "lnbits-test", Operation: OperationLNbitsLegacyCredentialRevoke, Params: json.RawMessage(params)}
		if ValidateRequest(req) == nil {
			t.Fatalf("accepted arbitrary target %s", params)
		}
	}
	req := Request{Version: ProtocolVersion, RequestID: "lnbits-test", Operation: OperationLNbitsLegacyCredentialRevoke, Params: json.RawMessage(`{}`)}
	if err := ValidateRequest(req); err != nil {
		t.Fatal(err)
	}
	if !knownOperation(req.Operation) || !mutatingOperation(req.Operation) {
		t.Fatal("revocation must use broker mutation locking/audit")
	}
	for _, p := range lndManagerPermissions {
		if p.Entity == "macaroon" && p.Action == "write" {
			t.Fatal("manager revocation authority widened")
		}
	}
}
