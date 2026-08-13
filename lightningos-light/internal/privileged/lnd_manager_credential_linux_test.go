//go:build linux

package privileged

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type memoryLNDManagerCredentialConfig struct {
	path string
}

func (config *memoryLNDManagerCredentialConfig) LNDMacaroonPath(context.Context) (string, error) {
	return config.path, nil
}

func (config *memoryLNDManagerCredentialConfig) SetLNDMacaroonPath(_ context.Context, path string, dryRun bool) (bool, error) {
	changed := config.path != path
	if !dryRun {
		config.path = path
	}
	return changed, nil
}

type fakeLNDManagerCredentialRPC struct {
	credential []byte
	rootKeyID  uint64
	bakeErr    error
	verifyErr  error
	deleteErr  error
	bakes      int
	verifies   int
	deleted    []uint64
}

func (rpc *fakeLNDManagerCredentialRPC) Bake(context.Context) ([]byte, uint64, error) {
	rpc.bakes++
	return append([]byte(nil), rpc.credential...), rpc.rootKeyID, rpc.bakeErr
}

func (rpc *fakeLNDManagerCredentialRPC) Verify(_ context.Context, path string) error {
	rpc.verifies++
	if rpc.verifyErr != nil {
		return rpc.verifyErr
	}
	credential, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(credential, rpc.credential) {
		return errors.New("unexpected manager credential")
	}
	return nil
}

func (rpc *fakeLNDManagerCredentialRPC) DeleteRootKey(_ context.Context, rootKeyID uint64) error {
	rpc.deleted = append(rpc.deleted, rootKeyID)
	return rpc.deleteErr
}

func TestNativeLNDManagerCredentialTransactionAndRollback(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to verify the production ownership boundary")
	}
	const (
		managerUID = 41001
		managerGID = 41001
		lndUID     = 41002
		lndGID     = 41002
	)
	root := t.TempDir()
	adminDir := filepath.Join(root, "lnd")
	if err := os.Mkdir(adminDir, 0o750); err != nil {
		t.Fatal(err)
	}
	adminPath := filepath.Join(adminDir, "admin.macaroon")
	adminCredential := []byte("native-admin-credential")
	if err := os.WriteFile(adminPath, adminCredential, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(adminPath, lndUID, lndGID); err != nil {
		t.Fatal(err)
	}

	credentialRoot := filepath.Join(root, "credentials")
	credentialDir := filepath.Join(credentialRoot, "lnd")
	credentialPath := filepath.Join(credentialDir, "manager.macaroon")
	statePath := filepath.Join(credentialDir, "manager-state.json")
	config := &memoryLNDManagerCredentialConfig{path: adminPath}
	rpc := &fakeLNDManagerCredentialRPC{credential: []byte("restricted-manager-credential"), rootKeyID: 9001}
	manager := &NativeLNDManagerCredentialManager{
		credentialRoot: credentialRoot,
		credentialDir:  credentialDir,
		credentialPath: credentialPath,
		statePath:      statePath,
		adminPath:      adminPath,
		tlsPath:        filepath.Join(root, "tls.cert"),
		grpcHost:       "127.0.0.1:10009",
		managerUser:    "manager-test",
		lndUser:        "lnd-test",
		lookupIdentity: func(name string) (int, int, error) {
			switch name {
			case "manager-test":
				return managerUID, managerGID, nil
			case "lnd-test":
				return lndUID, lndGID, nil
			default:
				return 0, 0, errors.New("unexpected identity")
			}
		},
		config: config,
		rpc:    rpc,
	}

	state, err := manager.Ensure(context.Background(), false)
	if err != nil || state.Status != "ready" || !state.Changed || !state.AdminProtected || config.path != credentialPath {
		t.Fatalf("unexpected ensure result: state=%#v config=%q err=%v", state, config.path, err)
	}
	assertLNDManagerCredentialMetadata(t, credentialPath, 0, managerGID, 0o640)
	assertLNDManagerCredentialMetadata(t, statePath, 0, 0, 0o600)
	assertLNDManagerCredentialMetadata(t, adminPath, lndUID, lndGID, 0o600)
	if rpc.bakes != 1 || rpc.verifies != 1 || len(rpc.deleted) != 0 {
		t.Fatalf("unexpected RPC calls after ensure: %#v", rpc)
	}

	state, err = manager.Ensure(context.Background(), false)
	if err != nil || state.Status != "ready" || state.Changed || rpc.bakes != 1 || rpc.verifies != 2 {
		t.Fatalf("idempotent ensure failed: state=%#v rpc=%#v err=%v", state, rpc, err)
	}

	state, err = manager.Rollback(context.Background(), false)
	if err != nil || state.Status != "rolled_back" || !state.Changed || config.path != adminPath {
		t.Fatalf("unexpected rollback result: state=%#v config=%q err=%v", state, config.path, err)
	}
	assertLNDManagerCredentialMetadata(t, adminPath, lndUID, lndGID, 0o640)
	for _, path := range []string{credentialPath, statePath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rollback retained %s: %v", path, err)
		}
	}
	if len(rpc.deleted) != 1 || rpc.deleted[0] != rpc.rootKeyID {
		t.Fatalf("rollback did not revoke the dedicated root key: %#v", rpc.deleted)
	}

	state, err = manager.Rollback(context.Background(), false)
	if err != nil || state.Status != "absent" || state.Changed {
		t.Fatalf("idempotent rollback failed: state=%#v err=%v", state, err)
	}
}

func TestNativeLNDManagerCredentialUnavailableIsPendingWithoutFilesystemMutation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to verify the production ownership boundary")
	}
	root := t.TempDir()
	adminPath := filepath.Join(root, "admin.macaroon")
	if err := os.WriteFile(adminPath, []byte("admin"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(adminPath, 42002, 42002); err != nil {
		t.Fatal(err)
	}
	credentialRoot := filepath.Join(root, "credentials")
	config := &memoryLNDManagerCredentialConfig{path: adminPath}
	rpc := &fakeLNDManagerCredentialRPC{rootKeyID: 9002, bakeErr: errors.New("LND unavailable")}
	manager := &NativeLNDManagerCredentialManager{
		credentialRoot: credentialRoot,
		credentialDir:  filepath.Join(credentialRoot, "lnd"),
		credentialPath: filepath.Join(credentialRoot, "lnd", "manager.macaroon"),
		statePath:      filepath.Join(credentialRoot, "lnd", "manager-state.json"),
		adminPath:      adminPath,
		tlsPath:        filepath.Join(root, "tls.cert"),
		grpcHost:       "127.0.0.1:10009",
		managerUser:    "manager-test",
		lndUser:        "lnd-test",
		lookupIdentity: func(name string) (int, int, error) {
			if name == "manager-test" {
				return 42001, 42001, nil
			}
			return 42002, 42002, nil
		},
		config: config,
		rpc:    rpc,
	}

	state, err := manager.Ensure(context.Background(), false)
	if err != nil || state.Status != "pending" || state.Changed || config.path != adminPath {
		t.Fatalf("unexpected pending result: state=%#v config=%q err=%v", state, config.path, err)
	}
	if _, err := os.Lstat(credentialRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending migration changed the credential filesystem: %v", err)
	}
	if len(rpc.deleted) != 1 || rpc.deleted[0] != rpc.rootKeyID {
		t.Fatalf("pending migration did not clean up the allocated root key: %#v", rpc.deleted)
	}
}

func assertLNDManagerCredentialMetadata(t *testing.T, path string, uid, gid uint32, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("unsupported stat metadata for %s", path)
	}
	if stat.Uid != uid || stat.Gid != gid || info.Mode().Perm() != mode || !info.Mode().IsRegular() {
		t.Fatalf("unsafe metadata for %s: uid=%d gid=%d mode=%v regular=%v", path, stat.Uid, stat.Gid, info.Mode().Perm(), info.Mode().IsRegular())
	}
}
