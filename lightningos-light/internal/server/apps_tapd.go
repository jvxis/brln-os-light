package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
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
//   - The manager submits typed Tapd operations to the privileged broker. The
//     broker resolves the fixed container identity and constructs tapcli argv;
//     no taprpc proto stubs or raw Docker arguments are exposed to the manager.
//   - tapd stores its data (proofs, macaroon, tls) under the mounted data dir,
//     so Uninstall keeps assets intact — it never drops the DB or deletes data.
//
// Taproot Assets is alpha on mainnet ("use at your own risk"); the store labels
// this app as experimental/advanced.

const (
	tapdAppID  = appmanifest.TapdID
	tapdDBName = "tapd"
	tapdDBUser = "tapd"
	// tapdDSNKey is where we record the provisioned DSN (for idempotent
	// reinstalls). The secret lives in the LOS secrets.env, never exposed by any
	// API.
	tapdDSNKey = "TAPD_PG_DSN"
)

type tapdPaths struct {
	Root string
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
		SecurityNotices: []string{
			appSecurityNoticeElevatedLNDAccess,
		},
	}
}

func (a tapdApp) Definition() appDefinition {
	return tapdDefinition()
}

func (a tapdApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	handled, state, err := system.TapdStatusWithBroker(ctx)
	if !handled {
		return info, errors.New("Tapd status requires privileged broker enforce mode")
	}
	if state.InterceptorConflict {
		info.Available = false
		info.UnavailableReason = tapdInterceptorConflictReason
		info.UnavailableMessage = tapdInterceptorConflictMessage("Fedimint Lightning Gateway")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Installed = state.Installed
	info.Status = state.Status
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
	return tapdPaths{Root: filepath.Join(appsRoot, tapdAppID)}
}

func (s *Server) installTapd(ctx context.Context) error {
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	handled, state, err := system.TapdStatusWithBroker(ctx)
	if !handled {
		return errors.New("Tapd preparation requires privileged broker enforce mode")
	}
	if err != nil {
		return err
	}
	if state.InterceptorConflict {
		return errors.New(tapdInterceptorConflictMessage("Fedimint Lightning Gateway"))
	}
	dbPassword, err := s.ensureTapdDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to provision tapd database: %w", err)
	}
	certificate, macaroon, err := s.tapdLNDMaterial(ctx, state.HasLNDMacaroon)
	if err != nil {
		return err
	}
	if handled, err := system.EnsureTapdWithBroker(ctx, dbPassword, certificate, macaroon); !handled {
		return errors.New("Tapd preparation requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Tapd preparation failed: %w", err)
	}
	if handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.TapdID, string(appmanifest.TapdImageApp)); !handled {
		return errors.New("Tapd image preparation requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Tapd image unavailable: %w", err)
	}
	if handled, runnable, err := system.ProbeAppImageWithBroker(ctx, appmanifest.TapdID, string(appmanifest.TapdImageApp)); !handled {
		return errors.New("Tapd image verification requires privileged broker enforce mode")
	} else if err != nil || !runnable {
		return errors.New("official Tapd image verification failed")
	}
	if handled, err := system.TapdLifecycleWithBroker(ctx, "start"); !handled {
		return errors.New("Tapd lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Tapd start failed: %w", err)
	}
	return nil
}

func (s *Server) uninstallTapd(ctx context.Context) error {
	if handled, err := system.RemoveTapdWithBroker(ctx); !handled {
		return errors.New("Tapd removal requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Tapd removal failed: %w", err)
	}
	paths := tapdAppPaths()
	// Remove only the compose/app dir, never the data dir or the DB.
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) startTapd(ctx context.Context) error {
	// Re-apply the closed declaration on every start. This transparently
	// migrates legacy installs while preserving their data and database.
	return s.installTapd(ctx)
}

func (s *Server) stopTapd(ctx context.Context) error {
	if handled, err := system.TapdLifecycleWithBroker(ctx, "stop"); !handled {
		return errors.New("Tapd lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Tapd stop failed: %w", err)
	}
	return nil
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

const tapdInterceptorConflictReason = "requires_htlc_interceptor"

// htlcInterceptorConflict returns the display name of a running app that already
// holds LND's single HTLC interceptor, or "" if none. tapd's RFQ subsystem
// always registers that interceptor (there is no tapd flag to disable it), and
// LND grants it to only one client at a time — so tapd cannot run alongside such
// an app. Currently the only store app that holds it is the Fedimint Lightning
// Gateway (`gatewayd lnd`). Extend this as more interceptor apps are added.
func tapdInterceptorConflictMessage(holder string) string {
	return fmt.Sprintf("%s is using LND's HTLC interceptor, which tapd also requires. LND allows only one at a time — stop %s before installing or starting Taproot Assets.", holder, holder)
}

func (s *Server) tapdLNDMaterial(ctx context.Context, hasExistingMacaroon bool) ([]byte, []byte, error) {
	certificate, err := os.ReadFile("/data/lnd/tls.cert")
	if err != nil || len(certificate) == 0 {
		return nil, nil, errors.New("LND TLS certificate is unavailable")
	}
	if hasExistingMacaroon {
		return certificate, nil, nil
	}
	if s == nil || s.lnd == nil {
		return nil, nil, errors.New("LND client unavailable")
	}
	ids, err := s.lnd.ListMacaroonIDs(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list LND macaroon IDs: %w", err)
	}
	rootKeyID, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
	if err != nil {
		return nil, nil, err
	}
	baked, err := s.lnd.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{
		Permissions: tapdMacaroonPermissions(), RootKeyID: rootKeyID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to bake dedicated Tapd macaroon: %w", err)
	}
	macaroon, err := hex.DecodeString(baked.MacaroonHex)
	if err != nil || len(macaroon) == 0 {
		return nil, nil, errors.New("invalid LND macaroon response")
	}
	admin, err := os.ReadFile(lndAdminMacaroonPath)
	if err != nil {
		return nil, nil, errors.New("native LND admin credential is unavailable")
	}
	if bytes.Equal(macaroon, admin) {
		return nil, nil, errors.New("Tapd LND credential must not be the admin macaroon")
	}
	return certificate, macaroon, nil
}

// Derived from the LND RPCs used by the official taproot-assets v0.8.0 source.
func tapdMacaroonPermissions() []lndclient.MacaroonPermission {
	return []lndclient.MacaroonPermission{
		{Entity: "address", Action: "read"},
		{Entity: "info", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "offchain", Action: "read"},
		{Entity: "offchain", Action: "write"},
		{Entity: "onchain", Action: "read"},
		{Entity: "onchain", Action: "write"},
		{Entity: "peers", Action: "read"},
		{Entity: "signer", Action: "generate"},
	}
}

func (s *Server) tapcli(ctx context.Context, request appmanifest.TapdCLIRequest) (string, error) {
	handled, output, err := system.TapdCLIWithBroker(ctx, request)
	if !handled {
		return "", errors.New("Tapd commands require privileged broker enforce mode")
	}
	return output, err
}
