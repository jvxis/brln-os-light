package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"
)

// BTCPay Server never talks to bitcoind directly: NBXplorer tracks the chain
// over RPC + P2P (no ZMQ, no txindex, pruned OK) and BTCPay consumes NBXplorer.
// The official btcpayserver-docker stack is intentionally NOT used here — it
// ships its own bitcoind + LND. This compose reuses the node's existing
// Bitcoin source and the native LND over REST (see docs/19_BACKLOG.md §14).
const (
	btcpayAppID            = "btcpay"
	btcpayImage            = "btcpayserver/btcpayserver:latest"
	btcpayNbxplorerImage   = "nicolasdorier/nbxplorer:2.6.8"
	btcpayPostgresImage    = "postgres:16"
	btcpayTorImage         = appmanifest.RoboSatsTorImage
	btcpayPort             = 23000
	btcpayNbxplorerPort    = 32838
	btcpayLndTLSWaitSteps  = 15
	btcpayTorSOCKSEndpoint = "tor:9050"
)

type btcpayPaths struct {
	Root           string
	DataDir        string
	NbxDir         string
	PgDir          string
	LndDir         string
	ComposePath    string
	EnvPath        string
	DbInitPath     string
	DbPasswordPath string
	MacaroonPath   string
	TLSCertPath    string
}

// btcpayBitcoinWiring is the resolved bitcoin backend as seen from inside the
// compose network. Probe fields are the same endpoints as seen from the host,
// used for install-time readiness checks.
type btcpayBitcoinWiring struct {
	Source             string // "app" | "external" | "remote"
	RPCURL             string
	RPCUser            string
	RPCPass            string
	NodeEndpoint       string
	ProbeRPCHost       string
	ProbeP2P           string
	JoinBitcoinNetwork bool
	UseTorProxy        bool
}

type btcpayApp struct {
	server *Server
}

func newBtcpayApp(s *Server) appHandler {
	return btcpayApp{server: s}
}

func btcpayDefinition() appDefinition {
	return appDefinition{
		ID:          btcpayAppID,
		Name:        "BTCPay Server",
		Description: "Self-hosted payment processor (invoices, Point of Sale, payment buttons) using your existing Bitcoin source and LND.",
		Port:        btcpayPort,
		SecurityNotices: []string{
			appSecurityNoticeElevatedLNDAccess,
		},
	}
}

func (a btcpayApp) Definition() appDefinition {
	return btcpayDefinition()
}

func (a btcpayApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := btcpayAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "btcpayserver")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a btcpayApp) Install(ctx context.Context) error {
	return a.server.applyBtcpay(ctx)
}

func (a btcpayApp) Start(ctx context.Context) error {
	return a.server.applyBtcpay(ctx)
}

func (a btcpayApp) Stop(ctx context.Context) error {
	paths := btcpayAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("BTCPay Server is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func (a btcpayApp) Uninstall(ctx context.Context) error {
	paths := btcpayAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	// Keep appsDataRoot/btcpay: it may hold store hot-wallet seeds, the
	// invoice database, and the baked LND macaroon for a future reinstall.
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func btcpayAppPaths() btcpayPaths {
	root := filepath.Join(appsRoot, btcpayAppID)
	dataRoot := filepath.Join(appsDataRoot, btcpayAppID)
	lndDir := filepath.Join(dataRoot, "lnd")
	return btcpayPaths{
		Root:           root,
		DataDir:        filepath.Join(dataRoot, "data"),
		NbxDir:         filepath.Join(dataRoot, "nbxplorer"),
		PgDir:          filepath.Join(dataRoot, "pgdata"),
		LndDir:         lndDir,
		ComposePath:    filepath.Join(root, "docker-compose.yaml"),
		EnvPath:        filepath.Join(root, ".env"),
		DbInitPath:     filepath.Join(root, "init-nbxplorer.sql"),
		DbPasswordPath: filepath.Join(dataRoot, "btcpay-db-password.txt"),
		MacaroonPath:   filepath.Join(lndDir, "btcpay.macaroon"),
		TLSCertPath:    filepath.Join(lndDir, "tls.cert"),
	}
}

// applyBtcpay drives both Install and Start so a bitcoin source switch after
// install re-wires NBXplorer automatically on the next start.
func (s *Server) applyBtcpay(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := btcpayAppPaths()
	if err := ensureBtcpayPaths(paths); err != nil {
		return err
	}

	wiring, err := s.resolveBtcpayBitcoinWiring(ctx)
	if err != nil {
		return err
	}
	if err := probeBtcpayBitcoin(ctx, wiring); err != nil {
		return err
	}

	if err := s.ensureBtcpayLndMaterial(ctx, paths); err != nil {
		return err
	}

	if err := ensureBtcpayImage(ctx); err != nil {
		return err
	}
	if err := ensureDockerImage(ctx, btcpayNbxplorerImage); err != nil {
		return err
	}
	if err := ensureDockerImage(ctx, btcpayPostgresImage); err != nil {
		return err
	}
	if wiring.UseTorProxy {
		if err := ensureDockerImage(ctx, btcpayTorImage); err != nil {
			return err
		}
	}

	dbPassword, err := ensureBtcpayDbPassword(paths)
	if err != nil {
		return err
	}
	if err := ensureBtcpayEnv(paths, wiring, dbPassword); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.DbInitPath, btcpayDbInitContents()); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, btcpayComposeContents(paths, wiring)); err != nil {
		return err
	}

	// Init scripts under /docker-entrypoint-initdb.d only run while postgres is
	// creating an empty data directory. Repair an already-initialized pgdata
	// volume that is missing NBXplorer's database before starting dependants.
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "btcpay-db"); err != nil {
		return err
	}
	if err := ensureBtcpayNbxplorerDatabase(ctx, system.RunCommandWithSudo); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureBtcpayUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("btcpay: ufw rule failed: %v", err)
	}
	return nil
}

// The BTCPay image intentionally tracks upstream's latest stable release so
// security updates are picked up on both install and a user-initiated start.
// Pinned dependency images keep using the shared cache-aware helper.
func ensureBtcpayImage(ctx context.Context) error {
	if strings.HasSuffix(btcpayImage, ":latest") {
		return pullDockerImage(ctx, btcpayImage)
	}
	return ensureDockerImage(ctx, btcpayImage)
}

func ensureBtcpayPaths(paths btcpayPaths) error {
	for _, dir := range []string{paths.Root, paths.DataDir, paths.NbxDir, paths.PgDir, paths.LndDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	return nil
}

// resolveBtcpayBitcoinWiring mirrors the Elements mainchain resolution: prefer
// the configured bitcoin source, fall back to the other one so mixed setups
// still install.
func (s *Server) resolveBtcpayBitcoinWiring(ctx context.Context) (btcpayBitcoinWiring, error) {
	order := []string{"local", "remote"}
	if readBitcoinSource() == "remote" {
		order = []string{"remote", "local"}
	}
	var errs []string
	for _, source := range order {
		var wiring btcpayBitcoinWiring
		var err error
		if source == "local" {
			wiring, err = s.resolveBtcpayLocalWiring(ctx)
		} else {
			wiring, err = s.resolveBtcpayRemoteWiring(ctx)
		}
		if err == nil {
			return wiring, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", source, err))
	}
	return btcpayBitcoinWiring{}, fmt.Errorf("no usable bitcoin source for BTCPay: %s", strings.Join(errs, "; "))
}

func (s *Server) resolveBtcpayLocalWiring(ctx context.Context) (btcpayBitcoinWiring, error) {
	bitcoinPaths := bitcoinCoreAppPaths()
	cfg, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		return btcpayBitcoinWiring{}, err
	}
	if strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Pass) == "" {
		return btcpayBitcoinWiring{}, errors.New("local bitcoin RPC credentials missing")
	}
	if fileExists(bitcoinPaths.ComposePath) {
		// NBXplorer joins the Bitcoin Core app's compose network (mempool
		// precedent) so RPC and P2P stay off the host interfaces.
		return btcpayBitcoinWiring{
			Source:             "app",
			RPCURL:             "http://bitcoind:8332/",
			RPCUser:            cfg.User,
			RPCPass:            cfg.Pass,
			NodeEndpoint:       "bitcoind:8333",
			ProbeRPCHost:       "127.0.0.1:8332",
			ProbeP2P:           "127.0.0.1:8333",
			JoinBitcoinNetwork: true,
		}, nil
	}
	if !isLocalRPCHost(cfg.Host) {
		return btcpayBitcoinWiring{}, fmt.Errorf("local bitcoin RPC host is not local: %s", cfg.Host)
	}
	if err := ensureLocalExternalBitcoinConsumerNetwork(ctx); err != nil {
		return btcpayBitcoinWiring{}, err
	}
	_, port := parseMainchainRPC(cfg.Host)
	dockerHost := appmanifest.BitcoinConsumerHostGateway
	return btcpayBitcoinWiring{
		Source:             "external",
		RPCURL:             fmt.Sprintf("http://%s:%d/", dockerHost, port),
		RPCUser:            cfg.User,
		RPCPass:            cfg.Pass,
		NodeEndpoint:       net.JoinHostPort(dockerHost, "8333"),
		ProbeRPCHost:       cfg.Host,
		ProbeP2P:           "127.0.0.1:8333",
		JoinBitcoinNetwork: true,
	}, nil
}

func (s *Server) resolveBtcpayRemoteWiring(ctx context.Context) (btcpayBitcoinWiring, error) {
	if s.cfg == nil {
		return btcpayBitcoinWiring{}, errors.New("config unavailable")
	}
	user, pass := readBitcoinSecrets()
	networkInfo, err := fetchBitcoinNetworkInfo(ctx, s.cfg.BitcoinRemote.RPCHost, user, pass)
	if err != nil {
		return btcpayBitcoinWiring{}, fmt.Errorf("failed to discover the remote Bitcoin P2P endpoint: %w", err)
	}
	return buildBtcpayRemoteWiring(s.cfg.BitcoinRemote.RPCHost, user, pass, networkInfo.LocalAddresses)
}

func buildBtcpayRemoteWiring(rpcHost, user, pass string, localAddresses []bitcoinNetworkLocalAddress) (btcpayBitcoinWiring, error) {
	raw := strings.TrimSpace(rpcHost)
	if raw == "" {
		return btcpayBitcoinWiring{}, errors.New("bitcoin remote RPC host missing")
	}
	if user == "" || pass == "" {
		return btcpayBitcoinWiring{}, errors.New("bitcoin remote RPC credentials missing")
	}
	scheme := "http"
	if strings.HasPrefix(strings.ToLower(raw), "https://") {
		scheme = "https"
	}
	host, port := parseMainchainRPC(raw)
	wiring := btcpayBitcoinWiring{
		Source:       "remote",
		RPCURL:       fmt.Sprintf("%s://%s:%d/", scheme, host, port),
		RPCUser:      user,
		RPCPass:      pass,
		ProbeRPCHost: raw,
	}
	if onionEndpoint, ok := selectBtcpayOnionEndpoint(localAddresses); ok {
		wiring.NodeEndpoint = onionEndpoint
		wiring.UseTorProxy = true
		return wiring, nil
	}
	wiring.NodeEndpoint = net.JoinHostPort(host, "8333")
	wiring.ProbeP2P = wiring.NodeEndpoint
	return wiring, nil
}

func selectBtcpayOnionEndpoint(localAddresses []bitcoinNetworkLocalAddress) (string, bool) {
	for _, candidate := range localAddresses {
		host := strings.ToLower(strings.TrimSpace(candidate.Address))
		label := strings.TrimSuffix(host, ".onion")
		if label == host || len(label) != 56 || candidate.Port < 1 || candidate.Port > 65535 {
			continue
		}
		valid := true
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '2' || char > '7') {
				valid = false
				break
			}
		}
		if valid {
			return net.JoinHostPort(host, strconv.Itoa(candidate.Port)), true
		}
	}
	return "", false
}

// probeBtcpayBitcoin verifies the wiring is actually usable before touching
// containers: RPC must answer and the P2P port must accept TCP, because
// NBXplorer cannot follow the chain without its node endpoint.
func probeBtcpayBitcoin(ctx context.Context, wiring btcpayBitcoinWiring) error {
	if _, err := fetchBitcoinInfo(ctx, wiring.ProbeRPCHost, wiring.RPCUser, wiring.RPCPass); err != nil {
		return fmt.Errorf("bitcoin RPC check failed (%s source, %s): %w", wiring.Source, wiring.ProbeRPCHost, err)
	}
	if !wiring.UseTorProxy && wiring.ProbeP2P != "" && !testTCP(wiring.ProbeP2P) {
		return fmt.Errorf("bitcoin P2P port unreachable (%s source, %s): NBXplorer needs the node's P2P port to follow the chain", wiring.Source, wiring.ProbeP2P)
	}
	return nil
}

// ensureBtcpayLndMaterial prepares the LND surface BTCPay needs: REST access
// from Docker (shared lnbits helper), a private copy of tls.cert, and a
// dedicated macaroon baked with the minimal permission set documented by
// BTCPay — never the admin macaroon.
func (s *Server) ensureBtcpayLndMaterial(ctx context.Context, paths btcpayPaths) error {
	if err := ensureLnbitsRestAccess(ctx); err != nil {
		return err
	}
	if err := copyBtcpayLndCert(paths); err != nil {
		return err
	}
	if fileExists(paths.MacaroonPath) {
		return nil
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
		Permissions: btcpayMacaroonPermissions(),
		RootKeyID:   rootKeyID,
	})
	if err != nil {
		return fmt.Errorf("failed to bake BTCPay macaroon: %w", err)
	}
	raw, err := hex.DecodeString(result.MacaroonHex)
	if err != nil || len(raw) == 0 {
		return errors.New("invalid LND macaroon response")
	}
	if err := os.WriteFile(paths.MacaroonPath, raw, 0600); err != nil {
		return fmt.Errorf("failed to write BTCPay macaroon: %w", err)
	}
	return nil
}

// btcpayMacaroonPermissions is the minimal LND permission set BTCPay documents
// for a dedicated macaroon (info:read covers the GetInfo sync check that the
// invoice macaroon lacks).
func btcpayMacaroonPermissions() []lndclient.MacaroonPermission {
	return []lndclient.MacaroonPermission{
		{Entity: "address", Action: "read"},
		{Entity: "address", Action: "write"},
		{Entity: "info", Action: "read"},
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "onchain", Action: "read"},
	}
}

// copyBtcpayLndCert keeps a private copy of tls.cert in the app data dir. The
// copy (not /data/lnd itself) is what gets mounted: a whole-dir mount would
// expose admin.macaroon and a single-file mount would pin a stale inode after
// LND regenerates the cert. ensureLnbitsRestAccess may have just restarted LND,
// so wait for the regenerated cert to appear.
func copyBtcpayLndCert(paths btcpayPaths) error {
	const certSource = "/data/lnd/tls.cert"
	var raw []byte
	var err error
	for attempt := 0; attempt < btcpayLndTLSWaitSteps; attempt++ {
		raw, err = os.ReadFile(certSource)
		if err == nil && len(raw) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", certSource, err)
	}
	if len(raw) == 0 {
		return fmt.Errorf("%s is empty", certSource)
	}
	existing, readErr := os.ReadFile(paths.TLSCertPath)
	if readErr == nil && string(existing) == string(raw) {
		return nil
	}
	if err := os.WriteFile(paths.TLSCertPath, raw, 0640); err != nil {
		return fmt.Errorf("failed to copy LND tls.cert: %w", err)
	}
	return nil
}

// ensureBtcpayDbPassword persists the postgres password in the data dir so it
// survives uninstall/reinstall alongside the pgdata volume it unlocks.
func ensureBtcpayDbPassword(paths btcpayPaths) (string, error) {
	password := readSecretFile(paths.DbPasswordPath)
	if password != "" {
		return password, nil
	}
	password, err := randomToken(24)
	if err != nil {
		return "", fmt.Errorf("failed to generate db password: %w", err)
	}
	if err := writeFile(paths.DbPasswordPath, password+"\n", 0600); err != nil {
		return "", err
	}
	return password, nil
}

func ensureBtcpayEnv(paths btcpayPaths, wiring btcpayBitcoinWiring, dbPassword string) error {
	required := [][2]string{
		{"BTCPAY_DB_PASSWORD", dbPassword},
		{"NBXPLORER_BTCRPCURL", wiring.RPCURL},
		{"NBXPLORER_BTCRPCUSER", wiring.RPCUser},
		{"NBXPLORER_BTCRPCPASSWORD", wiring.RPCPass},
		{"NBXPLORER_BTCNODEENDPOINT", wiring.NodeEndpoint},
	}
	if wiring.UseTorProxy {
		required = append(required, [2]string{"NBXPLORER_SOCKSENDPOINT", btcpayTorSOCKSEndpoint})
	}
	if !fileExists(paths.EnvPath) {
		lines := make([]string, 0, len(required)+1)
		for _, kv := range required {
			lines = append(lines, kv[0]+"="+kv[1])
		}
		lines = append(lines, "")
		return writeFile(paths.EnvPath, strings.Join(lines, "\n"), 0600)
	}
	for _, kv := range required {
		exists, value, err := envValueState(paths.EnvPath, kv[0])
		if err != nil {
			return err
		}
		if !exists {
			if err := appendEnvLine(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
			continue
		}
		if strings.TrimSpace(value) != kv[1] {
			if err := setEnvValue(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// btcpayDbInitContents creates the second database on first postgres boot.
// LC_CTYPE/LC_COLLATE 'C' follows the upstream NBXplorer requirement.
func btcpayDbInitContents() string {
	return `CREATE DATABASE nbxplorer TEMPLATE 'template0' LC_CTYPE 'C' LC_COLLATE 'C' ENCODING 'UTF8';
GRANT ALL PRIVILEGES ON DATABASE nbxplorer TO btcpay;
`
}

type btcpayCommandRunner func(context.Context, string, ...string) (string, error)

// ensureBtcpayNbxplorerDatabase makes postgres initialization idempotent. The
// official postgres entrypoint deliberately ignores init scripts once pgdata
// exists, so this also repairs installs left half-initialized by an earlier
// failed attempt.
func ensureBtcpayNbxplorerDatabase(ctx context.Context, run btcpayCommandRunner) error {
	var readinessOutput string
	for attempt := 0; attempt < 30; attempt++ {
		out, err := run(ctx, "docker", "exec", "btcpay-db", "pg_isready", "-U", "btcpay", "-d", "btcpayserver")
		readinessOutput = strings.TrimSpace(out)
		if err == nil {
			break
		}
		if attempt == 29 {
			if readinessOutput == "" {
				readinessOutput = err.Error()
			}
			return fmt.Errorf("BTCPay postgres did not become ready: %s", readinessOutput)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	out, err := run(
		ctx,
		"docker", "exec", "btcpay-db",
		"psql", "-U", "btcpay", "-d", "btcpayserver", "-tAc",
		"SELECT 1 FROM pg_database WHERE datname = 'nbxplorer'",
	)
	if err != nil {
		return fmt.Errorf("failed to check the NBXplorer database: %w", err)
	}
	if strings.TrimSpace(out) == "1" {
		return nil
	}

	if out, err = run(
		ctx,
		"docker", "exec", "btcpay-db",
		"createdb", "-U", "btcpay", "--owner=btcpay", "--template=template0",
		"--encoding=UTF8", "--lc-collate=C", "--lc-ctype=C", "nbxplorer",
	); err != nil {
		detail := strings.TrimSpace(out)
		if detail != "" {
			return fmt.Errorf("failed to create the NBXplorer database: %s: %w", detail, err)
		}
		return fmt.Errorf("failed to create the NBXplorer database: %w", err)
	}
	return nil
}

func btcpayLightningConnectionString() string {
	return "type=lnd-rest;server=https://host.docker.internal:8080/;macaroonfilepath=/etc/lnd/btcpay.macaroon;certfilepath=/etc/lnd/tls.cert"
}

// btcpayComposeContents builds the compose file. NBXplorer and postgres are
// expose-only (never published on the host); only the BTCPay UI port is
// published. When the bitcoin source is the Bitcoin Core app, nbxplorer joins
// that app's external network to reach bitcoind:8332/8333 directly. Remote
// Onion nodes get an isolated Tor service on the internal compose network.
func btcpayComposeContents(paths btcpayPaths, wiring btcpayBitcoinWiring) string {
	nbxNetworks := ""
	torService := ""
	torDependency := ""
	torEnvironment := ""
	torVolumes := ""
	networksBlock := fmt.Sprintf(`
networks:
  default:
    name: %s_default
`, btcpayAppID)
	if wiring.UseTorProxy {
		torService = fmt.Sprintf(`  tor:
    image: %s
    container_name: btcpay-tor
    restart: unless-stopped
    volumes:
      - btcpay-tor-data:/var/lib/tor
      - btcpay-tor-log:/var/log/tor

`, btcpayTorImage)
		torDependency = "      - tor\n"
		torEnvironment = "      NBXPLORER_SOCKSENDPOINT: ${NBXPLORER_SOCKSENDPOINT}\n"
		torVolumes = `volumes:
  btcpay-tor-data:
  btcpay-tor-log:
`
	}
	if wiring.JoinBitcoinNetwork {
		nbxNetworks = `    networks:
      - default
      - bitcoincore
`
		networksBlock = fmt.Sprintf(`
networks:
  default:
    name: %s_default
  bitcoincore:
    external: true
    name: bitcoincore_default
`, btcpayAppID)
	}
	return fmt.Sprintf(`services:
%[14]s
  btcpay-db:
    image: %[1]s
    container_name: btcpay-db
    restart: unless-stopped
    environment:
      POSTGRES_USER: btcpay
      POSTGRES_PASSWORD: ${BTCPAY_DB_PASSWORD}
      POSTGRES_DB: btcpayserver
    expose:
      - "5432"
    volumes:
      - %[2]s:/var/lib/postgresql/data
      - %[3]s:/docker-entrypoint-initdb.d/init-nbxplorer.sql:ro

  nbxplorer:
    image: %[4]s
    container_name: btcpay-nbxplorer
    restart: unless-stopped
    depends_on:
      - btcpay-db
%[15]s    environment:
      NBXPLORER_NETWORK: mainnet
      NBXPLORER_CHAINS: btc
      NBXPLORER_BIND: 0.0.0.0:%[5]d
      NBXPLORER_DATADIR: /datadir
      NBXPLORER_SIGNALFILESDIR: /datadir
      NBXPLORER_BTCRPCURL: ${NBXPLORER_BTCRPCURL}
      NBXPLORER_BTCRPCUSER: ${NBXPLORER_BTCRPCUSER}
      NBXPLORER_BTCRPCPASSWORD: ${NBXPLORER_BTCRPCPASSWORD}
      NBXPLORER_BTCNODEENDPOINT: ${NBXPLORER_BTCNODEENDPOINT}
%[16]s      NBXPLORER_POSTGRES: User ID=btcpay;Password=${BTCPAY_DB_PASSWORD};Host=btcpay-db;Port=5432;Application Name=nbxplorer;MaxPoolSize=20;Database=nbxplorer
    expose:
      - "%[5]d"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    volumes:
      - %[6]s:/datadir
%[7]s
  btcpayserver:
    image: %[8]s
    container_name: btcpay-server
    restart: unless-stopped
    depends_on:
      - nbxplorer
    environment:
      BTCPAY_NETWORK: mainnet
      BTCPAY_CHAINS: btc
      BTCPAY_BIND: 0.0.0.0:%[9]d
      BTCPAY_ROOTPATH: /
      BTCPAY_DATADIR: /datadir
      BTCPAY_BTCEXPLORERURL: http://nbxplorer:%[5]d/
      BTCPAY_POSTGRES: User ID=btcpay;Password=${BTCPAY_DB_PASSWORD};Host=btcpay-db;Port=5432;Application Name=btcpayserver;Database=btcpayserver
      BTCPAY_EXPLORERPOSTGRES: User ID=btcpay;Password=${BTCPAY_DB_PASSWORD};Host=btcpay-db;Port=5432;Application Name=btcpayserver;MaxPoolSize=80;Database=nbxplorer
      BTCPAY_BTCLIGHTNING: "%[10]s"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%[9]d:%[9]d"
    volumes:
      - %[11]s:/datadir
      - %[6]s:/root/.nbxplorer:ro
      - %[12]s:/etc/lnd:ro
%[17]s%[13]s`,
		btcpayPostgresImage,
		paths.PgDir,
		paths.DbInitPath,
		btcpayNbxplorerImage,
		btcpayNbxplorerPort,
		paths.NbxDir,
		nbxNetworks,
		btcpayImage,
		btcpayPort,
		btcpayLightningConnectionString(),
		paths.DataDir,
		paths.LndDir,
		networksBlock,
		torService,
		torDependency,
		torEnvironment,
		torVolumes,
	)
}

func ensureBtcpayUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", btcpayPort))
	return err
}
