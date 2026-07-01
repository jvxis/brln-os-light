package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"lightningos-light/internal/system"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Taproot Assets (tapd) — standalone, on-chain only.
//
// This app runs the official tapd daemon in Docker connected to the local
// native LND. It is Camada 1 in the LOS Taproot Assets roadmap: mint / receive /
// send assets ON-CHAIN. Sending or receiving assets over Lightning requires
// litd in integrated mode (Camada 2) and is intentionally NOT part of this app.
//
// Design notes:
//   - network_mode: host lets tapd reach LND (127.0.0.1:10009) and the LOS
//     Postgres (127.0.0.1:5432) with no host networking changes. tapd's own
//     RPC/REST are bound to loopback only, so 10029/8089 are never on the LAN.
//   - The manager talks to tapd via `docker exec <container> tapcli ... ` and
//     parses the JSON output (same pattern as resetLndgAdminPassword). No taprpc
//     proto stubs are vendored.
//   - tapd stores its data (proofs, macaroon, tls) under the mounted data dir,
//     so Uninstall keeps assets intact — it never drops the DB or deletes data.
//
// Taproot Assets is alpha on mainnet ("use at your own risk"); the store labels
// this app as experimental/advanced.

const (
	tapdAppID = "tapd"
	// tapdImage is the official Lightning Labs image, pinned. Validate the exact
	// v0.8.x tag against Docker Hub before release.
	tapdImage    = "lightninglabs/taproot-assets:v0.8.0"
	tapdNetwork  = "mainnet"
	tapdRPCPort  = 10029
	tapdRESTPort = 8089
	tapdDBName   = "tapd"
	tapdDBUser   = "tapd"
	// tapdDSNKey is where we record the provisioned DSN (for idempotent
	// reinstalls). The secret lives in the LOS secrets.env, never exposed by any
	// API.
	tapdDSNKey = "TAPD_PG_DSN"
	// lndMacaroonPathInTapd / lndTLSPathInTapd point at the LND data dir mounted
	// read-only into the tapd container.
	lndMacaroonPathInTapd = "/root/.lnd/data/chain/bitcoin/mainnet/admin.macaroon"
	lndTLSPathInTapd      = "/root/.lnd/tls.cert"
	tapdDirInContainer    = "/root/.tapd"
)

type tapdPaths struct {
	Root        string
	DataDir     string
	ComposePath string
	// ConfPath lives inside DataDir so it maps to <tapddir>/tapd.conf in the
	// container (tapd auto-reads it). It holds the DB password, so it is 0600.
	ConfPath string
}

type tapdApp struct {
	server *Server
}

func newTapdApp(s *Server) appHandler {
	return tapdApp{server: s}
}

func tapdDefinition() appDefinition {
	return appDefinition{
		ID:          tapdAppID,
		Name:        "Taproot Assets (tapd)",
		Description: "Mint, receive and send Taproot Assets on-chain with the official tapd daemon connected to your node. Experimental — alpha on mainnet, on-chain only (Lightning transfers require the community edge node).",
		Port:        0,
	}
}

func (a tapdApp) Definition() appDefinition {
	return tapdDefinition()
}

func (a tapdApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	if holder := a.server.htlcInterceptorConflict(ctx); holder != "" {
		info.Available = false
		info.UnavailableReason = tapdInterceptorConflictReason
		info.UnavailableMessage = tapdInterceptorConflictMessage(holder)
	}
	paths := tapdAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, tapdAppID)
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a tapdApp) Install(ctx context.Context) error {
	return a.server.installTapd(ctx)
}

func (a tapdApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallTapd(ctx)
}

func (a tapdApp) Start(ctx context.Context) error {
	return a.server.startTapd(ctx)
}

func (a tapdApp) Stop(ctx context.Context) error {
	return a.server.stopTapd(ctx)
}

func tapdAppPaths() tapdPaths {
	root := filepath.Join(appsRoot, tapdAppID)
	dataDir := filepath.Join(appsDataRoot, tapdAppID, "data")
	return tapdPaths{
		Root:        root,
		DataDir:     dataDir,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		ConfPath:    filepath.Join(dataDir, "tapd.conf"),
	}
}

func (s *Server) installTapd(ctx context.Context) error {
	if holder := s.htlcInterceptorConflict(ctx); holder != "" {
		return errors.New(tapdInterceptorConflictMessage(holder))
	}
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := tapdAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}

	dbPassword, err := s.ensureTapdDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to provision tapd database: %w", err)
	}

	if _, err := ensureFileWithChange(paths.ConfPath, tapdConfContents(dbPassword)); err != nil {
		return err
	}
	if err := os.Chmod(paths.ConfPath, 0600); err != nil {
		return fmt.Errorf("failed to secure tapd.conf: %w", err)
	}
	if _, err := ensureFileWithChange(paths.ComposePath, tapdComposeContents(paths)); err != nil {
		return err
	}

	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", tapdAppID)
}

func (s *Server) uninstallTapd(ctx context.Context) error {
	paths := tapdAppPaths()
	if fileExists(paths.ComposePath) {
		// down WITHOUT -v: keep the tapd data dir (proofs/macaroon) and the
		// Postgres database. Removing them would destroy access to the assets
		// (and the BTC that anchors them).
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	// Remove only the compose/app dir, never the data dir or the DB.
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) startTapd(ctx context.Context) error {
	if holder := s.htlcInterceptorConflict(ctx); holder != "" {
		return errors.New(tapdInterceptorConflictMessage(holder))
	}
	paths := tapdAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Taproot Assets is not installed")
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", tapdAppID)
}

func (s *Server) stopTapd(ctx context.Context) error {
	paths := tapdAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Taproot Assets is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

// ensureTapdDB provisions a dedicated `tapd` role + `tapd` database inside the
// Postgres cluster the LOS already runs, reusing the admin DSN. It mirrors
// bootstrapNotificationsDSN. It is idempotent: on reinstall it reuses the
// password recorded in TAPD_PG_DSN so the existing database keeps working.
func (s *Server) ensureTapdDB(ctx context.Context) (string, error) {
	logger := s.logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	if existing, err := readEnvFileValue(notificationsSecretsPath, tapdDSNKey); err == nil &&
		existing != "" && !isPlaceholderDSN(existing) && dsnHasPassword(existing) {
		if pw := passwordFromDSN(existing); pw != "" {
			_ = os.Setenv(tapdDSNKey, existing)
			return pw, nil
		}
	}

	adminDSN, err := ensureNotificationsAdminDSN(logger)
	if err != nil {
		return "", err
	}
	if adminDSN == "" {
		return "", errors.New("NOTIFICATIONS_PG_ADMIN_DSN not set")
	}

	password, err := randomPassword(32)
	if err != nil {
		return "", err
	}

	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(dbCtx, adminDSN)
	if err != nil {
		return "", err
	}
	defer pool.Close()

	adminUser := adminUserFromDSN(adminDSN)

	var roleCheck int
	roleExists := pool.QueryRow(dbCtx, "select 1 from pg_roles where rolname=$1", tapdDBUser).Scan(&roleCheck) == nil && roleCheck == 1
	if !roleExists {
		if _, err := pool.Exec(dbCtx, fmt.Sprintf("create role %s with login password '%s'", tapdDBUser, password)); err != nil {
			return "", err
		}
	} else {
		if _, err := pool.Exec(dbCtx, fmt.Sprintf("alter role %s with password '%s'", tapdDBUser, password)); err != nil {
			return "", err
		}
	}

	if adminUser != "" && adminUser != tapdDBUser {
		if _, err := pool.Exec(dbCtx, fmt.Sprintf("grant %s to %s", tapdDBUser, adminUser)); err != nil {
			logger.Printf("tapd warning: failed to grant %s to %s: %v", tapdDBUser, adminUser, err)
		}
	}

	var dbCheck int
	dbExists := pool.QueryRow(dbCtx, "select 1 from pg_database where datname=$1", tapdDBName).Scan(&dbCheck) == nil && dbCheck == 1
	if !dbExists {
		if _, err := pool.Exec(dbCtx, fmt.Sprintf("create database %s owner %s", tapdDBName, tapdDBUser)); err != nil {
			return "", err
		}
	} else {
		_, _ = pool.Exec(dbCtx, fmt.Sprintf("alter database %s owner to %s", tapdDBName, tapdDBUser))
	}

	dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:5432/%s?sslmode=disable", tapdDBUser, password, tapdDBName)
	if err := ensureSecretsDir(); err != nil {
		logger.Printf("tapd warning: failed to prepare secrets dir: %v", err)
	}
	if err := writeEnvFileValue(notificationsSecretsPath, tapdDSNKey, dsn); err != nil {
		logger.Printf("tapd warning: failed to persist %s: %v", tapdDSNKey, err)
	}
	_ = os.Setenv(tapdDSNKey, dsn)
	logger.Printf("tapd: provisioned database %s with user %s", tapdDBName, tapdDBUser)
	return password, nil
}

func passwordFromDSN(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return ""
	}
	pw, ok := parsed.User.Password()
	if !ok {
		return ""
	}
	return pw
}

// tapdConfContents builds the tapd.conf placed in the data dir (→
// <tapddir>/tapd.conf inside the container). Field names follow the lnd-style
// go-flags ini format; validate against tapd v0.8 before release.
func tapdConfContents(dbPassword string) string {
	return fmt.Sprintf(`network=%s
debuglevel=info
rpclisten=127.0.0.1:%d
restlisten=127.0.0.1:%d
tlsextradomain=localhost

lnd.host=127.0.0.1:10009
lnd.macaroonpath=%s
lnd.tlspath=%s

databasebackend=postgres
postgres.host=127.0.0.1
postgres.port=5432
postgres.user=%s
postgres.password=%s
postgres.dbname=%s
postgres.requiressl=false
`, tapdNetwork, tapdRPCPort, tapdRESTPort, lndMacaroonPathInTapd, lndTLSPathInTapd, tapdDBUser, dbPassword, tapdDBName)
}

func tapdComposeContents(paths tapdPaths) string {
	return fmt.Sprintf(`services:
  tapd:
    image: %s
    restart: unless-stopped
    network_mode: host
    command:
      - "--tapddir=%s"
    volumes:
      - /data/lnd:/root/.lnd:ro
      - %s:%s:rw
`, tapdImage, tapdDirInContainer, paths.DataDir, tapdDirInContainer)
}

const tapdInterceptorConflictReason = "requires_htlc_interceptor"

// htlcInterceptorConflict returns the display name of a running app that already
// holds LND's single HTLC interceptor, or "" if none. tapd's RFQ subsystem
// always registers that interceptor (there is no tapd flag to disable it), and
// LND grants it to only one client at a time — so tapd cannot run alongside such
// an app. Currently the only store app that holds it is the Fedimint Lightning
// Gateway (`gatewayd lnd`). Extend this as more interceptor apps are added.
func (s *Server) htlcInterceptorConflict(ctx context.Context) string {
	gw := fedimintGatewayAppPaths()
	if fileExists(gw.ComposePath) {
		if status, err := getComposeStatus(ctx, gw.Root, gw.ComposePath, "gatewayd"); err == nil && status == "running" {
			return "Fedimint Lightning Gateway"
		}
	}
	return ""
}

func tapdInterceptorConflictMessage(holder string) string {
	return fmt.Sprintf("%s is using LND's HTLC interceptor, which tapd also requires. LND allows only one at a time — stop %s before installing or starting Taproot Assets.", holder, holder)
}

// tapcli runs a tapcli command inside the running tapd container and returns its
// raw stdout (JSON). Reuses the docker-exec pattern from resetLndgAdminPassword.
func (s *Server) tapcli(ctx context.Context, args ...string) (string, error) {
	paths := tapdAppPaths()
	if !fileExists(paths.ComposePath) {
		return "", errors.New("Taproot Assets is not installed")
	}
	containerID, err := composeContainerID(ctx, paths.Root, paths.ComposePath, tapdAppID)
	if err != nil {
		return "", err
	}
	if containerID == "" {
		return "", errors.New("tapd container not running")
	}
	execArgs := append([]string{"exec", containerID, "tapcli", "--network=" + tapdNetwork}, args...)
	out, err := system.RunCommandWithSudo(ctx, "docker", execArgs...)
	if err != nil {
		return out, err
	}
	return out, nil
}
