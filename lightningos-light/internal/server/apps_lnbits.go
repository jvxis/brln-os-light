package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"
)

const lnbitsPort = appmanifest.LNbitsPort

type lnbitsPaths struct {
	Root         string
	DataDir      string
	ComposePath  string
	EnvPath      string
	LndDir       string
	TLSCertPath  string
	MacaroonPath string
}

type lnbitsApp struct {
	server *Server
}

func newLnbitsApp(s *Server) appHandler {
	return lnbitsApp{server: s}
}

func lnbitsDefinition() appDefinition {
	return appDefinition{
		ID:          appmanifest.LNbitsID,
		Name:        "LNbits",
		Description: "Lightning wallet/accounts system and extension platform powered by your local LND.",
		Port:        lnbitsPort,
		SecurityNotices: []string{
			appSecurityNoticeElevatedLNDAccess,
		},
	}
}

func (a lnbitsApp) Definition() appDefinition {
	return lnbitsDefinition()
}

func (a lnbitsApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	paths := lnbitsAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	handled, status, _, err := system.InspectAppWithBroker(ctx, appmanifest.LNbitsID)
	if !handled {
		info.Status = "unknown"
		return info, errors.New("LNbits status requires privileged broker enforce mode")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a lnbitsApp) Install(ctx context.Context) error {
	return a.server.applyLnbits(ctx)
}

func (a lnbitsApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallLnbits(ctx)
}

func (a lnbitsApp) Start(ctx context.Context) error {
	return a.server.applyLnbits(ctx)
}

func (a lnbitsApp) Stop(ctx context.Context) error {
	return a.server.stopLnbits(ctx)
}

func lnbitsAppPaths() lnbitsPaths {
	root := filepath.Join(appsRoot, appmanifest.LNbitsID)
	dataDir := filepath.Join(appsDataRoot, appmanifest.LNbitsID, "data")
	lndDir := filepath.Join(appsDataRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir)
	return lnbitsPaths{
		Root:         root,
		DataDir:      dataDir,
		ComposePath:  filepath.Join(root, appmanifest.LNbitsComposeFile),
		EnvPath:      filepath.Join(root, appmanifest.LNbitsEnvFile),
		LndDir:       lndDir,
		TLSCertPath:  filepath.Join(lndDir, appmanifest.LNbitsTLSCertFile),
		MacaroonPath: filepath.Join(lndDir, appmanifest.LNbitsMacaroonFile),
	}
}

func (s *Server) applyLnbits(ctx context.Context) error {
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	paths := lnbitsAppPaths()
	if err := ensureLnbitsPaths(paths); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, lnbitsComposeContents(paths)); err != nil {
		return err
	}
	if err := ensureLnbitsEnv(paths); err != nil {
		return err
	}
	if err := s.ensureLnbitsMacaroon(ctx, paths); err != nil {
		return err
	}
	if err := copyLnbitsLndCert(paths); err != nil {
		return err
	}
	if handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.LNbitsID, string(appmanifest.LNbitsImageApp)); !handled {
		return errors.New("LNbits image preparation requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("LNbits image unavailable: %w", err)
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, appmanifest.LNbitsID, "start"); !handled {
		return errors.New("LNbits lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("LNbits start failed: %w", err)
	}
	if handled, _, err := system.EnsureAppFirewallWithBroker(ctx, appmanifest.LNbitsID); !handled {
		return errors.New("LNbits firewall requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("LNbits firewall failed: %w", err)
	}
	return nil
}

func (s *Server) uninstallLnbits(ctx context.Context) error {
	paths := lnbitsAppPaths()
	if fileExists(paths.ComposePath) {
		if handled, err := system.RemoveAppWithBroker(ctx, appmanifest.LNbitsID); !handled {
			return errors.New("LNbits removal requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("LNbits removal failed: %w", err)
		}
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) stopLnbits(ctx context.Context) error {
	paths := lnbitsAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("LNbits is not installed")
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, appmanifest.LNbitsID, "stop"); !handled {
		return errors.New("LNbits lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("LNbits stop failed: %w", err)
	}
	return nil
}

func ensureLnbitsPaths(paths lnbitsPaths) error {
	for _, directory := range []struct {
		path string
		name string
	}{
		{paths.Root, "app"},
		{paths.DataDir, "app data"},
		{filepath.Join(paths.DataDir, "extensions"), "extension data"},
		{paths.LndDir, "LND credential"},
	} {
		if err := os.MkdirAll(directory.path, 0750); err != nil {
			return fmt.Errorf("failed to create %s directory: %w", directory.name, err)
		}
	}
	return nil
}

func lnbitsComposeContents(paths lnbitsPaths) string {
	return appmanifest.LNbitsCompose(appmanifest.LNbitsComposePaths{
		DataDir:      paths.DataDir,
		TLSCertPath:  paths.TLSCertPath,
		MacaroonPath: paths.MacaroonPath,
	})
}

func ensureLnbitsEnv(paths lnbitsPaths) error {
	var raw []byte
	if fileExists(paths.EnvPath) {
		var err error
		raw, err = os.ReadFile(paths.EnvPath)
		if err != nil {
			return fmt.Errorf("failed to read LNbits environment: %w", err)
		}
	}
	normalized, err := appmanifest.NormalizeLNbitsEnv(raw)
	if err != nil {
		return err
	}
	if err := os.WriteFile(paths.EnvPath, normalized, 0600); err != nil {
		return fmt.Errorf("failed to normalize LNbits environment: %w", err)
	}
	if err := os.Chmod(paths.EnvPath, 0600); err != nil {
		return fmt.Errorf("failed to secure LNbits environment: %w", err)
	}
	return nil
}

func (s *Server) ensureLnbitsMacaroon(ctx context.Context, paths lnbitsPaths) error {
	if info, err := os.Lstat(paths.MacaroonPath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("LNbits LND credential must be a regular file")
		}
		credential, err := os.ReadFile(paths.MacaroonPath)
		if err != nil || len(credential) == 0 {
			return errors.New("LNbits LND credential is unavailable")
		}
		if err := validateLnbitsCredentialNotAdmin(credential); err != nil {
			return err
		}
		return os.Chmod(paths.MacaroonPath, 0600)
	} else if !os.IsNotExist(err) {
		return errors.New("LNbits LND credential is unavailable")
	}
	if s.lnd == nil {
		return errors.New("LND client unavailable")
	}
	ids, err := s.lnd.ListMacaroonIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to list LND macaroon IDs: %w", err)
	}
	rootKeyID, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
	if err != nil {
		return err
	}
	result, err := s.lnd.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{
		Permissions: lnbitsMacaroonPermissions(),
		RootKeyID:   rootKeyID,
	})
	if err != nil {
		return fmt.Errorf("failed to bake LNbits macaroon: %w", err)
	}
	raw, err := hex.DecodeString(result.MacaroonHex)
	if err != nil || len(raw) == 0 {
		return errors.New("invalid LND macaroon response")
	}
	if err := validateLnbitsCredentialNotAdmin(raw); err != nil {
		return err
	}
	file, err := os.OpenFile(paths.MacaroonPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("failed to write LNbits macaroon: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to write LNbits macaroon: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close LNbits macaroon: %w", err)
	}
	return nil
}

func validateLnbitsCredentialNotAdmin(credential []byte) error {
	admin, err := os.ReadFile(lndAdminMacaroonPath)
	if err != nil {
		return errors.New("native LND admin credential is unavailable")
	}
	if bytes.Equal(credential, admin) {
		return errors.New("LNbits LND credential must not be the admin macaroon")
	}
	return nil
}

// LNbits v1.5.6 uses this credential for both LndRestWallet and its built-in
// LndRestNode manager. The latter adds node info, peer, channel, fee-policy,
// and on-chain balance/open/close RPCs to the wallet's invoice/payment calls.
func lnbitsMacaroonPermissions() []lndclient.MacaroonPermission {
	return []lndclient.MacaroonPermission{
		{Entity: "info", Action: "read"},
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "offchain", Action: "read"},
		{Entity: "offchain", Action: "write"},
		{Entity: "onchain", Action: "read"},
		{Entity: "onchain", Action: "write"},
		{Entity: "peers", Action: "read"},
		{Entity: "peers", Action: "write"},
	}
}

func copyLnbitsLndCert(paths lnbitsPaths) error {
	const source = "/data/lnd/tls.cert"
	var raw []byte
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		raw, err = os.ReadFile(source)
		if err == nil && len(raw) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", source, err)
	}
	if len(raw) == 0 {
		return fmt.Errorf("%s is empty", source)
	}
	if info, statErr := os.Lstat(paths.TLSCertPath); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("LNbits LND certificate must be a regular file")
		}
		if existing, readErr := os.ReadFile(paths.TLSCertPath); readErr == nil && bytes.Equal(existing, raw) {
			return nil
		}
	} else if !os.IsNotExist(statErr) {
		return errors.New("LNbits LND certificate is unavailable")
	}
	if err := os.WriteFile(paths.TLSCertPath, raw, 0640); err != nil {
		return fmt.Errorf("failed to copy LND tls.cert for LNbits: %w", err)
	}
	return nil
}

// envValueState remains shared by the compatibility implementations which
// have not yet moved their environment files into the broker boundary.
func envValueState(path string, key string) (bool, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, key+"=") {
			return true, strings.TrimPrefix(line, key+"="), nil
		}
	}
	return false, "", nil
}
