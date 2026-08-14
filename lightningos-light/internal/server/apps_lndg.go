package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"
)

type lndgPaths struct {
	Root              string
	DataDir           string
	PgDir             string
	LogPath           string
	ComposePath       string
	EnvPath           string
	DockerfilePath    string
	DockerignorePath  string
	EntrypointPath    string
	LndDir            string
	TLSCertPath       string
	MacaroonPath      string
	ChannelDBPath     string
	AdminPasswordPath string
	DbPasswordPath    string
	BuildHashPath     string
}

type lndgApp struct {
	server *Server
}

func newLndgApp(s *Server) appHandler {
	return lndgApp{server: s}
}

func lndgDefinition() appDefinition {
	return appDefinition{
		ID:          "lndg",
		Name:        "LNDg",
		Description: "Advanced analytics, automation, and insights for your LND node.",
		Port:        8889,
		SecurityNotices: []string{
			appSecurityNoticeElevatedLNDAccess,
			appSecurityNoticeLNDDataDirectoryRead,
		},
	}
}

func (a lndgApp) Definition() appDefinition {
	return lndgDefinition()
}

func (a lndgApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := lndgAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	info.AdminPasswordPath = paths.AdminPasswordPath
	handled, status, _, err := system.InspectAppWithBroker(ctx, appmanifest.LNDgID)
	if !handled {
		info.Status = "unknown"
		return info, errors.New("LNDg status requires privileged broker enforce mode")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a lndgApp) Install(ctx context.Context) error {
	return a.server.installLndg(ctx)
}

func (a lndgApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallLndg(ctx)
}

func (a lndgApp) Start(ctx context.Context) error {
	return a.server.startLndg(ctx)
}

func (a lndgApp) Stop(ctx context.Context) error {
	return a.server.stopLndg(ctx)
}

func lndgAppPaths() lndgPaths {
	root := filepath.Join(appsRoot, "lndg")
	dataDir := filepath.Join(appsDataRoot, "lndg", "data")
	pgDir := filepath.Join(appsDataRoot, "lndg", "pgdata")
	logPath := filepath.Join(dataDir, "lndg-controller.log")
	lndDir := filepath.Join(appsDataRoot, "lndg", "lnd")
	return lndgPaths{
		Root:              root,
		DataDir:           dataDir,
		PgDir:             pgDir,
		LogPath:           logPath,
		ComposePath:       filepath.Join(root, "docker-compose.yaml"),
		EnvPath:           filepath.Join(root, ".env"),
		DockerfilePath:    filepath.Join(root, "Dockerfile"),
		DockerignorePath:  filepath.Join(root, ".dockerignore"),
		EntrypointPath:    filepath.Join(root, "entrypoint.sh"),
		LndDir:            lndDir,
		TLSCertPath:       filepath.Join(lndDir, "tls.cert"),
		MacaroonPath:      filepath.Join(lndDir, "lndg.macaroon"),
		ChannelDBPath:     filepath.Join(lndDir, appmanifest.LNDgChannelDBFile),
		AdminPasswordPath: filepath.Join(dataDir, "lndg-admin.txt"),
		DbPasswordPath:    filepath.Join(dataDir, "lndg-db-password.txt"),
		BuildHashPath:     filepath.Join(root, ".build_hash"),
	}
}

func (s *Server) installLndg(ctx context.Context) error {
	return s.applyLndg(ctx)
}

func (s *Server) applyLndg(ctx context.Context) error {
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	paths := lndgAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := os.MkdirAll(paths.PgDir, 0750); err != nil {
		return fmt.Errorf("failed to create app db directory: %w", err)
	}
	if err := os.MkdirAll(paths.LndDir, 0750); err != nil {
		return fmt.Errorf("failed to create app LND directory: %w", err)
	}
	if err := ensureLndgLogFile(paths.LogPath); err != nil {
		return err
	}
	if err := ensureLndgChannelDBSource(paths); err != nil {
		return err
	}

	if _, err := ensureFileWithChange(paths.EntrypointPath, lndgEntrypoint); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, lndgComposeContents(paths)); err != nil {
		return err
	}

	if err := ensureLndgEnv(ctx, paths); err != nil {
		return err
	}
	if err := s.ensureLndgMacaroon(ctx, paths); err != nil {
		return err
	}
	if err := copyLndgLndCert(paths); err != nil {
		return err
	}
	if err := removeLegacyLNDgBuildAssets(paths); err != nil {
		return err
	}
	for _, variant := range appmanifest.LNDgImageVariants() {
		if handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.LNDgID, string(variant)); !handled {
			return errors.New("LNDg image preparation requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("LNDg image %s unavailable: %w", variant, err)
		}
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, appmanifest.LNDgID, "start"); !handled {
		return errors.New("LNDg lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("LNDg start failed: %w", err)
	}
	if handled, _, err := system.EnsureAppFirewallWithBroker(ctx, appmanifest.LNDgID); !handled {
		return errors.New("LNDg firewall requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("LNDg firewall failed: %w", err)
	}
	return nil
}

func (s *Server) uninstallLndg(ctx context.Context) error {
	paths := lndgAppPaths()
	if fileExists(paths.ComposePath) {
		if handled, err := system.RemoveAppWithBroker(ctx, appmanifest.LNDgID); !handled {
			return errors.New("LNDg removal requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("LNDg removal failed: %w", err)
		}
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) startLndg(ctx context.Context) error {
	return s.applyLndg(ctx)
}

func (s *Server) stopLndg(ctx context.Context) error {
	paths := lndgAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("LNDg is not installed")
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, appmanifest.LNDgID, "stop"); !handled {
		return errors.New("LNDg lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("LNDg stop failed: %w", err)
	}
	return nil
}

func (s *Server) resetLndgAdminPassword(ctx context.Context) error {
	paths := lndgAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("LNDg is not installed")
	}

	if handled, err := system.ResetAppAdminWithBroker(ctx, appmanifest.LNDgID); !handled {
		return errors.New("LNDg admin reset requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("LNDg admin reset failed: %w", err)
	}
	return nil
}

func lndgComposeContents(paths lndgPaths) string {
	return appmanifest.LNDgCompose(appmanifest.LNDgComposePaths{
		DataDir:        paths.DataDir,
		PgDir:          paths.PgDir,
		LogPath:        paths.LogPath,
		LndDir:         paths.LndDir,
		ChannelDBPath:  lndgChannelDBSource(paths),
		EntrypointPath: paths.EntrypointPath,
	})
}

func lndgChannelDBSource(paths lndgPaths) string {
	if info, err := os.Lstat(appmanifest.LNDgChannelDBPath); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return appmanifest.LNDgChannelDBPath
	}
	return paths.ChannelDBPath
}

func ensureLndgChannelDBSource(paths lndgPaths) error {
	if lndgChannelDBSource(paths) == appmanifest.LNDgChannelDBPath {
		return nil
	}
	if info, err := os.Lstat(paths.ChannelDBPath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 0 {
			return errors.New("LNDg channel database placeholder is unsafe")
		}
		return os.Chmod(paths.ChannelDBPath, 0640)
	} else if !os.IsNotExist(err) {
		return errors.New("LNDg channel database placeholder is unavailable")
	}
	file, err := os.OpenFile(paths.ChannelDBPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return errors.New("failed to create LNDg channel database placeholder")
	}
	if err := file.Close(); err != nil {
		return errors.New("failed to close LNDg channel database placeholder")
	}
	return nil
}

func (s *Server) ensureLndgMacaroon(ctx context.Context, paths lndgPaths) error {
	if info, err := os.Lstat(paths.MacaroonPath); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("LNDg LND credential must be a regular file")
		}
		credential, err := os.ReadFile(paths.MacaroonPath)
		if err != nil || len(credential) == 0 {
			return errors.New("LNDg LND credential is unavailable")
		}
		if err := validateLndgCredentialNotAdmin(credential); err != nil {
			return err
		}
		return os.Chmod(paths.MacaroonPath, 0600)
	} else if !os.IsNotExist(err) {
		return errors.New("LNDg LND credential is unavailable")
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
		Permissions: lndgMacaroonPermissions(),
		RootKeyID:   rootKeyID,
	})
	if err != nil {
		return fmt.Errorf("failed to bake LNDg macaroon: %w", err)
	}
	raw, err := hex.DecodeString(result.MacaroonHex)
	if err != nil || len(raw) == 0 {
		return errors.New("invalid LND macaroon response")
	}
	if err := validateLndgCredentialNotAdmin(raw); err != nil {
		return err
	}
	file, err := os.OpenFile(paths.MacaroonPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("failed to write LNDg macaroon: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to write LNDg macaroon: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close LNDg macaroon: %w", err)
	}
	return nil
}

func validateLndgCredentialNotAdmin(credential []byte) error {
	equal, err := lndCredentialEqualsNativeAdmin(credential)
	if err != nil {
		return err
	}
	if equal {
		return errors.New("LNDg LND credential must not be the admin macaroon")
	}
	return nil
}

func lndgMacaroonPermissions() []lndclient.MacaroonPermission {
	return []lndclient.MacaroonPermission{
		{Entity: "address", Action: "write"},
		{Entity: "info", Action: "read"},
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "message", Action: "write"},
		{Entity: "offchain", Action: "read"},
		{Entity: "offchain", Action: "write"},
		{Entity: "onchain", Action: "read"},
		{Entity: "onchain", Action: "write"},
		{Entity: "peers", Action: "read"},
		{Entity: "peers", Action: "write"},
		{Entity: "signer", Action: "generate"},
		{Entity: "signer", Action: "read"},
	}
}

func copyLndgLndCert(paths lndgPaths) error {
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
		if !info.Mode().IsRegular() {
			return errors.New("LNDg LND certificate must be a regular file")
		}
		if existing, readErr := os.ReadFile(paths.TLSCertPath); readErr == nil && bytes.Equal(existing, raw) {
			return nil
		}
	} else if !os.IsNotExist(statErr) {
		return errors.New("LNDg LND certificate is unavailable")
	}
	if err := os.WriteFile(paths.TLSCertPath, raw, 0640); err != nil {
		return fmt.Errorf("failed to copy LND tls.cert for LNDg: %w", err)
	}
	return nil
}

func ensureLndgEnv(ctx context.Context, paths lndgPaths) error {
	allowedHosts, _ := lndgHosts(mergeLNDgAccessHosts(
		splitEnvList(readEnvValue(paths.EnvPath, "LNDG_ALLOWED_HOSTS")),
		lndgAccessHost(ctx),
	))
	adminPassword := readEnvValue(paths.EnvPath, "LNDG_ADMIN_PASSWORD")
	if adminPassword == "" {
		adminPassword = readSecretFile(paths.AdminPasswordPath)
	}
	if adminPassword == "" {
		var err error
		adminPassword, err = randomToken(20)
		if err != nil {
			return err
		}
	}
	dbPassword := readEnvValue(paths.EnvPath, "LNDG_DB_PASSWORD")
	if dbPassword == "" {
		dbPassword = readSecretFile(paths.DbPasswordPath)
	}
	if dbPassword == "" {
		var err error
		dbPassword, err = randomToken(24)
		if err != nil {
			return err
		}
	}
	env, err := appmanifest.LNDgRuntimeEnv(appmanifest.LNDgRuntime{
		AdminPassword: adminPassword,
		DBPassword:    dbPassword,
		AllowedHosts:  allowedHosts,
	})
	if err != nil {
		return err
	}
	if err := writeFile(paths.EnvPath, env, 0600); err != nil {
		return err
	}
	if err := os.Chmod(paths.EnvPath, 0600); err != nil {
		return err
	}
	if err := writePrivateLNDgFile(paths.AdminPasswordPath, []byte(adminPassword+"\n")); err != nil {
		return err
	}
	if err := writePrivateLNDgFile(paths.DbPasswordPath, []byte(dbPassword+"\n")); err != nil {
		return err
	}
	return nil
}

func writePrivateLNDgFile(path string, raw []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("LNDg secret target is unsafe")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("LNDg secret target is unavailable")
	}
	if err := writeFile(path, string(raw), 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func removeLegacyLNDgBuildAssets(paths lndgPaths) error {
	for _, path := range []string{paths.DockerfilePath, paths.DockerignorePath, paths.BuildHashPath} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("legacy LNDg build asset is unsafe")
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove legacy LNDg build asset: %w", err)
		}
	}
	return nil
}

func ensureLndgLogFile(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			entries, readErr := os.ReadDir(path)
			if readErr != nil {
				return fmt.Errorf("failed to inspect %s: %w", path, readErr)
			}
			if len(entries) == 0 {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("failed to remove %s: %w", path, err)
				}
			} else {
				backup := path + ".bak-" + time.Now().Format("20060102150405")
				if err := os.Rename(path, backup); err != nil {
					return fmt.Errorf("failed to move %s to %s: %w", path, backup, err)
				}
			}
		} else {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close %s: %w", path, err)
	}
	return nil
}

func defaultLndgHosts(ctx context.Context) ([]string, []string) {
	return lndgHosts([]string{lndgAccessHost(ctx)})
}

func lndgHosts(dynamic []string) ([]string, []string) {
	hosts := []string{"localhost", "127.0.0.1", "host.docker.internal"}
	for _, ip := range dynamic {
		if !stringInSlice(ip, hosts) {
			hosts = append(hosts, ip)
		}
	}
	origins := []string{}
	for _, host := range hosts {
		for _, scheme := range []string{"http", "https"} {
			origin := fmt.Sprintf("%s://%s", scheme, host)
			if !stringInSlice(origin, origins) {
				origins = append(origins, origin)
			}
			originWithPort := fmt.Sprintf("%s://%s:8889", scheme, host)
			if !stringInSlice(originWithPort, origins) {
				origins = append(origins, originWithPort)
			}
		}
	}
	return hosts, origins
}

type lndgAccessHostContextKey struct{}

func withLNDgAccessHost(ctx context.Context, requestHost string) context.Context {
	host := strings.TrimSpace(requestHost)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() {
		return ctx
	}
	return context.WithValue(ctx, lndgAccessHostContextKey{}, ip.To4().String())
}

func lndgAccessHost(ctx context.Context) string {
	host, _ := ctx.Value(lndgAccessHostContextKey{}).(string)
	return host
}

func mergeLNDgAccessHosts(existing []string, current string) []string {
	hosts := []string{}
	for _, candidate := range append(existing, current) {
		ip := net.ParseIP(strings.TrimSpace(candidate))
		if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() {
			continue
		}
		value := ip.To4().String()
		if !stringInSlice(value, hosts) {
			hosts = append(hosts, value)
		}
	}
	return hosts
}

const lndgEntrypoint = appmanifest.LNDgEntrypoint
