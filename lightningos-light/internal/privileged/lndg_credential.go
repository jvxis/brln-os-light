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

	"lightningos-light/internal/lndclient"
)

type lndgRemovalReceipt struct {
	Root    uint64 `json:"root"`
	Digest  string `json:"digest"`
	Revoked bool   `json:"revoked"`
}

type lndgCredentialRemoval struct {
	rpc               lnbitsRevocationRPC
	path, receiptPath string
	receipt           lndgRemovalReceipt
}

func (m *ComposeAppManager) lndgCredentialPaths() (string, string, string) {
	data, private, lnd := m.AppsDataRoot, m.PrivilegedAppsRoot, m.LNDDataRoot
	if data == "" {
		data = defaultAppsDataRoot
	}
	if private == "" {
		private = defaultPrivilegedAppsRoot
	}
	if lnd == "" {
		lnd = defaultLNDDataRoot
	}
	return filepath.Join(data, "lndg", "lnd", "lndg.macaroon"), filepath.Join(private, "lndg-removal-receipt.json"), lnd
}

func (m *ComposeAppManager) prepareLNDgCredentialRemoval(ctx context.Context) (*lndgCredentialRemoval, error) {
	path, receiptPath, lnd := m.lndgCredentialPaths()
	for _, dir := range []string{filepath.Dir(filepath.Dir(filepath.Dir(path))), filepath.Dir(filepath.Dir(path)), filepath.Dir(path), filepath.Dir(receiptPath)} {
		if err := validateRegularDirectory(dir); err != nil {
			return nil, errors.New("LNDg credential directory requires review")
		}
	}
	rpc := m.lndgRevocationRPC
	if rpc == nil {
		rpc = newLNDManagerCredentialClient(defaultLNDGRPCHost, filepath.Join(lnd, "tls.cert"), filepath.Join(lnd, "data", "chain", "bitcoin", "mainnet", "admin.macaroon"))
	}
	p := &lndgCredentialRemoval{rpc: rpc, path: path, receiptPath: receiptPath}
	var previous lndgRemovalReceipt
	if info, err := os.Lstat(receiptPath); err == nil {
		if !info.Mode().IsRegular() || !privilegedPathOwnerIsSafe(info) {
			return nil, errors.New("unsafe LNDg removal receipt")
		}
		raw, err := readRegularFile(receiptPath, 4096)
		if err != nil || json.Unmarshal(raw, &previous) != nil || previous.Root == 0 || len(previous.Digest) != 64 {
			return nil, errors.New("invalid LNDg removal receipt")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	raw, err := readRegularFile(path, 64*1024)
	if os.IsNotExist(err) && previous.Revoked {
		ids, e := rpc.ListMacaroonIDs(ctx)
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			if id == previous.Root {
				return nil, errors.New("LNDg removed root unexpectedly exists")
			}
		}
		p.receipt = previous
		return p, nil
	}
	if err != nil {
		return nil, errors.New("LNDg credential unavailable; removal requires review")
	}
	root, permissions, err := lndclient.InspectMacaroon(raw)
	if err != nil || root < 1_000_000_000_000 || root > uint64(time.Now().Add(24*time.Hour).UnixMilli()) {
		return nil, errors.New("LNDg dedicated root ownership requires review")
	}
	got := lndclient.MacaroonPermissionStrings(permissions)
	sort.Strings(got)
	want := []string{"address:write", "info:read", "invoices:read", "invoices:write", "message:write", "offchain:read", "offchain:write", "onchain:read", "onchain:write", "peers:read", "peers:write", "signer:generate", "signer:read"}
	if !reflect.DeepEqual(got, want) {
		return nil, errors.New("custom LNDg permissions require review")
	}
	hash := sha256.Sum256(raw)
	p.receipt = lndgRemovalReceipt{Root: root, Digest: hex.EncodeToString(hash[:])}
	matching := previous.Root == root && previous.Digest == p.receipt.Digest
	if !matching {
		enrolled := filepath.Join(filepath.Dir(receiptPath), "lndg", "lnd", "lndg.macaroon")
		for _, dir := range []string{filepath.Dir(filepath.Dir(enrolled)), filepath.Dir(enrolled)} {
			if validateRegularDirectory(dir) != nil {
				return nil, errors.New("LNDg broker enrollment unavailable")
			}
		}
		info, e := os.Lstat(enrolled)
		if e != nil || !info.Mode().IsRegular() || !lndgEnrollmentOwnerIsSafe(info) {
			return nil, errors.New("unsafe LNDg broker enrollment")
		}
		stored, e := readRegularFile(enrolled, 64*1024)
		if e != nil || !bytes.Equal(raw, stored) {
			return nil, errors.New("LNDg credential differs from broker enrollment")
		}
	}
	ids, err := rpc.ListMacaroonIDs(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, id := range ids {
		if id == root {
			found = true
		}
	}
	if found {
		if matching && previous.Revoked {
			return nil, errors.New("LNDg revoked root unexpectedly exists")
		}
		if rpc.AuthenticateMacaroon(ctx, raw, permissions) != nil {
			return nil, errors.New("LNDg credential authentication failed")
		}
	} else if !matching {
		return nil, errors.New("LNDg root absent without broker removal record")
	}
	p.receipt.Revoked = !found
	return p, nil
}

// Called only after Compose down succeeds. Persist intent before revocation so
// interruption can be retried without trusting an unauthenticated root ID.
func (p *lndgCredentialRemoval) revoke(ctx context.Context) error {
	raw, err := json.Marshal(p.receipt)
	if err != nil {
		return err
	}
	if err = writeAtomicRegularFile(p.receiptPath, raw, 0600); err != nil {
		return err
	}
	if !p.receipt.Revoked {
		if err = p.rpc.DeleteMacaroonID(ctx, p.receipt.Root); err != nil {
			return errors.New("LNDg credential revocation failed; retry removal")
		}
	}
	ids, err := p.rpc.ListMacaroonIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == p.receipt.Root {
			return errors.New("LNDg root remains active")
		}
	}
	p.receipt.Revoked = true
	raw, _ = json.Marshal(p.receipt)
	return writeAtomicRegularFile(p.receiptPath, raw, 0600)
}

// Remove only the revoked dedicated file, never data, TLS or another credential.
func (p *lndgCredentialRemoval) discardRevokedFile() error {
	raw, err := readRegularFile(p.path, 64*1024)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	if !p.receipt.Revoked || hex.EncodeToString(hash[:]) != p.receipt.Digest {
		return errors.New("LNDg credential changed during removal")
	}
	return os.Remove(p.path)
}
