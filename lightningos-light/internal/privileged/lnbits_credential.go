package privileged

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
)

type lnbitsRevocationRPC interface {
	ListMacaroonIDs(context.Context) ([]uint64, error)
	AuthenticateMacaroon(context.Context, []byte, []lndclient.MacaroonPermission) error
	DeleteMacaroonID(context.Context, uint64) error
}

type lnbitsRevocationReceipt struct {
	Root    uint64 `json:"root"`
	Digest  string `json:"digest"`
	Revoked bool   `json:"revoked"`
}

// RevokeLNbitsLegacyCredential deliberately accepts NO caller-provided path,
// credential, or root key ID. Only the former catalog credential, authenticated
// by LND, is eligible. The Manager never receives macaroon:write authority.
func (manager *ComposeAppManager) RevokeLNbitsLegacyCredential(ctx context.Context, dryRun bool) error {
	dataRoot := manager.AppsDataRoot
	if dataRoot == "" {
		dataRoot = defaultAppsDataRoot
	}
	lndRoot := manager.LNDDataRoot
	if lndRoot == "" {
		lndRoot = defaultLNDDataRoot
	}
	privateRoot := manager.PrivilegedAppsRoot
	if privateRoot == "" {
		privateRoot = defaultPrivilegedAppsRoot
	}
	credentialDir := filepath.Join(dataRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir)
	for _, dir := range []string{dataRoot, filepath.Join(dataRoot, appmanifest.LNbitsID), credentialDir} {
		if err := validateRegularDirectory(dir); err != nil {
			return errors.New("unsafe LNbits credential directory")
		}
	}
	raw, err := readRegularFile(filepath.Join(credentialDir, appmanifest.LNbitsMacaroonFile), 64*1024)
	if err != nil {
		return errors.New("LNbits legacy credential unavailable")
	}
	root, permissions, err := lndclient.InspectMacaroon(raw)
	if err != nil || root < 1_000_000_000_000 || root > uint64(time.Now().Add(24*time.Hour).UnixMilli()) {
		return errors.New("LNbits legacy credential ownership is unknown")
	}
	got := lndclient.MacaroonPermissionStrings(permissions)
	sort.Strings(got)
	want := []string{"info:read", "invoices:read", "invoices:write", "offchain:read", "offchain:write", "onchain:read", "onchain:write", "peers:read", "peers:write"}
	if !reflect.DeepEqual(got, want) {
		return errors.New("credential is not the former LNbits catalog policy")
	}
	digest := sha256.Sum256(raw)
	receipt := lnbitsRevocationReceipt{Root: root, Digest: hex.EncodeToString(digest[:])}
	receiptPath := filepath.Join(privateRoot, "lnbits-credential-revocation.json")
	pending := false
	if info, statErr := os.Lstat(receiptPath); statErr == nil {
		if !info.Mode().IsRegular() || !privilegedPathOwnerIsSafe(info) {
			return errors.New("unsafe LNbits revocation receipt")
		}
		stored, readErr := readRegularFile(receiptPath, 4096)
		var previous lnbitsRevocationReceipt
		if readErr != nil || json.Unmarshal(stored, &previous) != nil {
			return errors.New("invalid LNbits revocation receipt")
		}
		if previous.Root == receipt.Root && previous.Digest == receipt.Digest {
			if previous.Revoked {
				return nil
			}
			pending = true
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	// Never infer ownership from a timestamp alone. The same credential must
	// already be enrolled in this app's root-owned execution snapshot.
	if !pending {
		snapshotDir := filepath.Join(privateRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir)
		for _, dir := range []string{privateRoot, filepath.Dir(snapshotDir), snapshotDir} {
			if err := validateRegularDirectory(dir); err != nil {
				return errors.New("LNbits credential enrollment requires operator review")
			}
		}
		snapshotPath := filepath.Join(snapshotDir, appmanifest.LNbitsMacaroonFile)
		info, err := os.Lstat(snapshotPath)
		if err != nil || !info.Mode().IsRegular() || !lnbitsEnrollmentOwnerIsSafe(info) {
			return errors.New("LNbits credential enrollment is unsafe")
		}
		enrolled, err := readRegularFile(snapshotPath, 64*1024)
		if err != nil || !bytes.Equal(enrolled, raw) {
			return errors.New("LNbits credential differs from its broker enrollment")
		}
	}
	rpc := manager.lnbitsRevocationRPC
	if rpc == nil {
		rpc = newLNDManagerCredentialClient(defaultLNDGRPCHost, filepath.Join(lndRoot, "tls.cert"), filepath.Join(lndRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon"))
	}
	ids, err := rpc.ListMacaroonIDs(ctx)
	if err != nil {
		return errors.New("LND unavailable for LNbits credential migration")
	}
	found := false
	for _, id := range ids {
		if id == root {
			found = true
			break
		}
	}
	if found {
		if err := rpc.AuthenticateMacaroon(ctx, raw, permissions); err != nil {
			return errors.New("LNbits legacy credential authentication failed")
		}
	} else if !pending {
		return errors.New("LNbits legacy root was not authenticated by this broker")
	}
	if dryRun {
		return nil
	}
	if err := ensureDirectoryTreeNoSymlink(privateRoot, 0700); err != nil {
		return err
	}
	encoded, _ := json.Marshal(receipt)
	if err := writeAtomicRegularFile(receiptPath, encoded, 0600); err != nil {
		return err
	}
	if found {
		if err := rpc.DeleteMacaroonID(ctx, root); err != nil {
			return errors.New("LNbits legacy credential revocation failed")
		}
	}
	receipt.Revoked = true
	encoded, _ = json.Marshal(receipt)
	return writeAtomicRegularFile(receiptPath, encoded, 0600)
}
