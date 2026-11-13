package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"
)

// Keep the Manager's existing restricted macaroon: deletion is exclusively a
// no-argument broker operation which derives/validates its target from disk.
type lnbitsBrokerCredentialRPC struct{ *lndclient.Client }

func (rpc lnbitsBrokerCredentialRPC) DeleteMacaroonID(ctx context.Context, _ uint64) error {
	return system.RevokeLNbitsLegacyCredentialWithBroker(ctx, false)
}
func (rpc lnbitsBrokerCredentialRPC) ValidateRevocation(ctx context.Context) error {
	return system.RevokeLNbitsLegacyCredentialWithBroker(ctx, true)
}

type lnbitsCredentialRPC interface {
	ListMacaroonIDs(context.Context) ([]uint64, error)
	BakeCustomMacaroon(context.Context, lndclient.BakeCustomMacaroonRequest) (lndclient.BakeCustomMacaroonResult, error)
	AuthenticateMacaroon(context.Context, []byte, []lndclient.MacaroonPermission) error
	DeleteMacaroonID(context.Context, uint64) error
}

type lnbitsCredentialRuntime struct {
	inspect   func(context.Context) (string, error)
	lifecycle func(context.Context, string) error
}

// Stored outside both the container's writable data and broker-attested app
// declaration. It contains no credential, and survives interrupted migration.
type lnbitsCredentialMigration struct {
	RootKeyID uint64 `json:"root_key_id"`
	OldSHA256 string `json:"old_sha256"`
	Resume    bool   `json:"resume"`
}

func lnbitsMigrationPath(paths lnbitsPaths) string {
	return filepath.Join(filepath.Dir(paths.Root), ".lnbits-wallet-migration.json")
}

func lnbitsLegacyPermissions() []lndclient.MacaroonPermission {
	return append(lnbitsMacaroonPermissions(),
		lndclient.MacaroonPermission{Entity: "onchain", Action: "read"},
		lndclient.MacaroonPermission{Entity: "onchain", Action: "write"},
		lndclient.MacaroonPermission{Entity: "peers", Action: "read"},
		lndclient.MacaroonPermission{Entity: "peers", Action: "write"})
}

func lnbitsPermissionsEqual(a, b []lndclient.MacaroonPermission) bool {
	left, right := lndclient.MacaroonPermissionStrings(a), lndclient.MacaroonPermissionStrings(b)
	sort.Strings(left)
	sort.Strings(right)
	return reflect.DeepEqual(left, right)
}

// An explicit user stop/uninstall overrides a pending automatic resume.
func pauseLNbitsCredentialMigration(paths lnbitsPaths) error {
	raw, err := readLNbitsPrivateFile(lnbitsMigrationPath(paths))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal lnbitsCredentialMigration
	if json.Unmarshal(raw, &journal) != nil {
		return errors.New("invalid LNbits credential migration record")
	}
	journal.Resume = false
	raw, err = json.Marshal(journal)
	if err != nil {
		return err
	}
	return writeLNbitsPrivateFile(lnbitsMigrationPath(paths), raw)
}

func lnbitsCredentialHash(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func readLNbitsPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, errors.New("LNbits credential state must be a bounded regular file")
	}
	return os.ReadFile(path)
}

func writeLNbitsPrivateFile(path string, raw []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("unsafe LNbits credential target")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	tempDir := filepath.Dir(path)
	if filepath.Base(path) == "lnbits.macaroon" {
		// Never leave interrupted temporary files in the broker's strict LND
		// declaration directory. This parent is not mounted into the container.
		tempDir = filepath.Dir(tempDir)
	}
	f, err := os.CreateTemp(tempDir, ".lnbits-credential-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		defer dir.Close()
		return dir.Sync()
	}
	return nil
}

func bakeLNbitsWalletCredential(ctx context.Context, rpc lnbitsCredentialRPC, root uint64) ([]byte, error) {
	result, err := rpc.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{RootKeyID: root, Permissions: lnbitsMacaroonPermissions()})
	if err != nil {
		return nil, errors.New("LNbits wallet credential bake failed")
	}
	raw, err := hex.DecodeString(result.MacaroonHex)
	if err != nil {
		return nil, errors.New("invalid LNbits credential response")
	}
	actualRoot, permissions, err := lndclient.InspectMacaroon(raw)
	if err != nil || root == 0 || actualRoot != root || !lnbitsPermissionsEqual(permissions, lnbitsMacaroonPermissions()) {
		return nil, errors.New("LNbits wallet credential permission verification failed")
	}
	if err := rpc.AuthenticateMacaroon(ctx, raw, lnbitsMacaroonPermissions()); err != nil {
		return nil, err
	}
	return raw, nil
}

// Call under the app operation lock. Only the exact former catalog credential
// is migrated. Custom/shared/default credentials need operator review; never
// broaden an attenuated credential or revoke the native default root key.
func ensureLNbitsWalletCredential(ctx context.Context, paths lnbitsPaths, rpc lnbitsCredentialRPC, runtime lnbitsCredentialRuntime) error {
	journalPath := lnbitsMigrationPath(paths)
	journalRaw, journalErr := readLNbitsPrivateFile(journalPath)
	if journalErr != nil && !os.IsNotExist(journalErr) {
		return journalErr
	}
	var journal *lnbitsCredentialMigration
	if journalErr == nil {
		journal = &lnbitsCredentialMigration{}
		if json.Unmarshal(journalRaw, journal) != nil || journal.RootKeyID == 0 || len(journal.OldSHA256) != 64 {
			return errors.New("invalid LNbits credential migration record; operator review required")
		}
	}
	raw, err := readLNbitsPrivateFile(paths.MacaroonPath)
	if os.IsNotExist(err) && journal == nil {
		ids, err := rpc.ListMacaroonIDs(ctx)
		if err != nil {
			return err
		}
		root, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
		if err != nil {
			return err
		}
		raw, err = bakeLNbitsWalletCredential(ctx, rpc, root)
		if err != nil {
			return err
		}
		return writeLNbitsPrivateFile(paths.MacaroonPath, raw)
	}
	if err != nil {
		return err
	}
	root, permissions, err := lndclient.InspectMacaroon(raw)
	if err != nil || root == 0 {
		return errors.New("LNbits credential requires operator review; default/admin or unsupported credential refused")
	}
	walletOnly := lnbitsPermissionsEqual(permissions, lnbitsMacaroonPermissions())
	if journal == nil && walletOnly {
		if err := rpc.AuthenticateMacaroon(ctx, raw, lnbitsMacaroonPermissions()); err != nil {
			return err
		}
		return os.Chmod(paths.MacaroonPath, 0600)
	}
	if !walletOnly && !lnbitsPermissionsEqual(permissions, lnbitsLegacyPermissions()) {
		return errors.New("custom LNbits credential requires operator review; no permissions changed")
	}
	if journal == nil {
		// Previous LOS bakes allocated millisecond IDs, never the common shared
		// IDs 0/1. This is a compatibility guard, not proof of exclusive use:
		// operators must not share an App Store credential with another app.
		if root < 1_000_000_000_000 || root > uint64(time.Now().Add(24*time.Hour).UnixMilli()) {
			return errors.New("LNbits root key ownership is unknown; operator review required")
		}
		if err := rpc.AuthenticateMacaroon(ctx, raw, lnbitsLegacyPermissions()); err != nil {
			return err
		}
		if preflight, ok := rpc.(interface{ ValidateRevocation(context.Context) error }); ok {
			if err := preflight.ValidateRevocation(ctx); err != nil {
				return err
			}
		}
		status, err := runtime.inspect(ctx)
		if err != nil {
			return err
		}
		if status != "running" && status != "stopped" && status != "not_installed" {
			return errors.New("LNbits runtime state is unknown; credential migration deferred")
		}
		journal = &lnbitsCredentialMigration{RootKeyID: root, OldSHA256: lnbitsCredentialHash(raw), Resume: status == "running"}
		encoded, _ := json.Marshal(journal)
		if err := writeLNbitsPrivateFile(journalPath, encoded); err != nil {
			return err
		}
	}
	if journal.RootKeyID != root || (!walletOnly && journal.OldSHA256 != lnbitsCredentialHash(raw)) {
		return errors.New("LNbits credential changed during migration; operator review required")
	}
	if !walletOnly {
		// Stop before revocation. Failure never falls back to the old authority.
		if err := runtime.lifecycle(ctx, "stop"); err != nil {
			return err
		}
		ids, err := rpc.ListMacaroonIDs(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id == root {
				if err := rpc.DeleteMacaroonID(ctx, root); err != nil {
					return err
				}
				break
			}
		}
		// Reuse the dedicated numeric ID with a NEW root secret. Old copied
		// macaroons stay invalid. Repeating after an interrupted bake is safe.
		legacy := raw
		raw, err = bakeLNbitsWalletCredential(ctx, rpc, root)
		if err != nil {
			return err
		}
		if rpc.AuthenticateMacaroon(ctx, legacy, lnbitsLegacyPermissions()) == nil {
			return errors.New("LNbits legacy credential remains valid; migration stopped")
		}
		if err := writeLNbitsPrivateFile(paths.MacaroonPath, raw); err != nil {
			return err
		}
	} else if err := rpc.AuthenticateMacaroon(ctx, raw, lnbitsMacaroonPermissions()); err != nil {
		return err
	}
	if journal.Resume {
		if err := runtime.lifecycle(ctx, "start"); err != nil {
			return err
		}
	}
	return os.Remove(journalPath)
}

func (s *Server) startLNbitsCredentialReconciler() {
	go func() {
		for {
			ctx, cancel := context.WithTimeout(s.shutdownContext(), 2*time.Minute)
			paths := lnbitsAppPaths()
			_, statErr := os.Lstat(paths.ComposePath)
			var err error
			done := false
			if os.IsNotExist(statErr) {
				done = true
			} else if statErr != nil {
				err = statErr
			} else if finish, ok := s.beginAppOperation("lnbits", "credential_migration"); ok {
				err = s.ensureLnbitsMacaroon(ctx, paths)
				finish()
				done = err == nil
			}
			cancel()
			if done {
				return
			}
			if err != nil && s.logger != nil {
				s.logger.Printf("LNbits wallet-only credential migration pending; inspect LNbits App Store start error before exposing the app")
			}
			select {
			case <-s.shutdownContext().Done():
				return
			case <-time.After(time.Minute):
			}
		}
	}()
}
