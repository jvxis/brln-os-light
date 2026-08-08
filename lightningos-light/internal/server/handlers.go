package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"
)

const (
	secretsPath                    = "/etc/lightningos/secrets.env"
	lndConfPath                    = "/data/lnd/lnd.conf"
	lndPasswordPath                = "/data/lnd/password.txt"
	lndWalletDBPath                = "/data/lnd/data/chain/bitcoin/mainnet/wallet.db"
	lndChannelDBPath               = "/data/lnd/data/graph/mainnet/channel.db"
	lndAdminMacaroonPath           = "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"
	lndFixPermsScript              = "/usr/local/sbin/lightningos-fix-lnd-perms"
	mempoolBaseURL                 = "https://mempool.space/api/v1/lightning"
	boostPeersDefaultLimit         = 3
	boostPeersMaxLimit             = 10
	boostPeersPersistentLimit      = 1
	lndRPCTimeout                  = 15 * time.Second
	lndConnectTimeout              = 30 * time.Second
	lndOpenChannelTimeout          = 60 * time.Second
	lndBatchOpenChannelTimeout     = 90 * time.Second
	lndWalletPaymentPreviewTimeout = 120 * time.Second
	lndWalletPaymentTimeout        = 120 * time.Second
	batchOpenMaxChannels           = 50
	pendingOpenBumpReferenceVbytes = int64(110)
	lndWarmupPeriod                = 90 * time.Second
)

var (
	ansiOSCRegexp       = regexp.MustCompile(`\x1b\][^\x07]*(?:\x07|\x1b\\)`)
	ansiCSIRegexp       = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiSingleCharRegex = regexp.MustCompile(`\x1b[@-Z\\-_]`)
)

type healthIssue struct {
	Component string `json:"component"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

type healthResponse struct {
	Status    string        `json:"status"`
	Issues    []healthIssue `json:"issues"`
	Timestamp string        `json:"timestamp"`
}

type waitingCloseRecoveryResponse struct {
	Attempts          int    `json:"attempts"`
	LastAttemptAt     string `json:"last_attempt_at,omitempty"`
	LastResult        string `json:"last_result,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	LastRecoveredTxid string `json:"last_recovered_txid,omitempty"`
	SuggestForceClose bool   `json:"suggest_force_close"`
}

type pendingChannelResponse struct {
	lndclient.PendingChannelInfo
	WaitingCloseRecovery              *waitingCloseRecoveryResponse `json:"waiting_close_recovery,omitempty"`
	FundingTxStatus                   string                        `json:"funding_tx_status,omitempty"`
	FundingTxFeeSat                   int64                         `json:"funding_tx_fee_sat,omitempty"`
	FundingTxVsize                    float64                       `json:"funding_tx_vsize,omitempty"`
	FundingTxEffectiveFeeRateSatVbyte float64                       `json:"funding_tx_effective_fee_rate_sat_vb,omitempty"`
	FundingTxRBF                      *bool                         `json:"funding_tx_rbf,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	issues := []healthIssue{}
	status := "OK"

	lndCtx, lndCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer lndCancel()
	lndStatus, err := s.lnd.GetStatus(lndCtx)
	if err != nil {
		serviceCtx, serviceCancel := context.WithTimeout(r.Context(), 3*time.Second)
		serviceActive := activeLNDService(serviceCtx) != ""
		serviceCancel()
		endpointReachable := false
		if serviceActive && s.cfg != nil {
			endpointReachable = testTCP(s.cfg.LND.GRPCHost)
		}
		issue := classifyLNDHealthError(err, s.lndWarmupActive(), serviceActive, endpointReachable)
		issues = append(issues, issue)
		status = elevate(status, issue.Level)
	} else if lndStatus.WalletState == "locked" {
		issues = append(issues, healthIssue{Component: "lnd", Level: "ERR", Message: "LND wallet locked"})
		status = elevate(status, "ERR")
	}

	bitcoinSource := readBitcoinSource()
	btcCtx, btcCancel := context.WithTimeout(r.Context(), bitcoinActiveHandlerTimeout(bitcoinSource))
	defer btcCancel()
	bitcoin, err := s.bitcoinActiveStatusCached(btcCtx)
	if err != nil {
		if bitcoinSource == "local" {
			issues = append(issues, healthIssue{Component: "bitcoin", Level: "WARN", Message: "Bitcoin local check failed"})
		} else {
			issues = append(issues, healthIssue{Component: "bitcoin", Level: "WARN", Message: "Bitcoin remote check failed"})
		}
		status = elevate(status, "WARN")
	}
	if err == nil {
		if bitcoin.RPCStale {
			issues = append(issues, healthIssue{Component: "bitcoin", Level: "WARN", Message: "Bitcoin RPC check stale"})
			status = elevate(status, "WARN")
		} else if !bitcoin.RPCOk {
			issues = append(issues, healthIssue{Component: "bitcoin", Level: "ERR", Message: "Bitcoin RPC unreachable"})
			status = elevate(status, "ERR")
		}
		if !bitcoin.ZMQRawBlockOk || !bitcoin.ZMQRawTxOk {
			issues = append(issues, healthIssue{Component: "bitcoin", Level: "WARN", Message: "Bitcoin ZMQ unreachable"})
			status = elevate(status, "WARN")
		}
	}

	pgCtx, pgCancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer pgCancel()
	if !system.SystemctlIsActive(pgCtx, "postgresql") {
		issues = append(issues, healthIssue{Component: "postgres", Level: "ERR", Message: "Postgres inactive"})
		status = elevate(status, "ERR")
	}

	resp := healthResponse{
		Status:    status,
		Issues:    issues,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	writeJSON(w, http.StatusOK, resp)
}

func classifyLNDHealthError(err error, warmup, serviceActive, endpointReachable bool) healthIssue {
	issue := healthIssue{Component: "lnd", Level: "ERR", Message: lndStatusMessage(err)}
	if err == nil {
		return issue
	}
	if isTimeoutError(err) && warmup {
		issue.Level = "WARN"
		issue.Message = "LND warming up after restart (GetInfo timeout)"
		return issue
	}
	if serviceActive && endpointReachable && !lndHealthErrorRequiresAction(err) {
		issue.Level = "WARN"
		if isTimeoutError(err) {
			issue.Message = "LND GetInfo timeout (gRPC endpoint reachable)"
		} else {
			issue.Message = "LND RPC temporarily busy (gRPC endpoint reachable)"
		}
	}
	return issue
}

func lndHealthErrorRequiresAction(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "wallet locked") ||
		strings.Contains(msg, "macaroon") ||
		strings.Contains(msg, "tls") ||
		strings.Contains(msg, "certificate") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "unauthenticated") ||
		strings.Contains(msg, "authentication")
}

func elevate(current string, next string) string {
	if current == "ERR" || next == "OK" {
		return current
	}
	if next == "ERR" {
		return "ERR"
	}
	if current == "OK" && next == "WARN" {
		return "WARN"
	}
	return current
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context deadline exceeded")
}

func lndStatusMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "wallet locked") || strings.Contains(msg, "unlock") {
		return "LND wallet locked"
	}
	if strings.Contains(msg, "macaroon") {
		if strings.Contains(msg, "permission denied") {
			return "LND macaroon unreadable (check permissions)"
		}
		if strings.Contains(msg, "no such file") {
			return "LND macaroon missing"
		}
		return "LND macaroon error"
	}
	if strings.Contains(msg, "tls") || strings.Contains(msg, "cert") {
		if strings.Contains(msg, "permission denied") {
			return "LND TLS cert unreadable (check permissions)"
		}
		if strings.Contains(msg, "no such file") {
			return "LND TLS cert missing"
		}
		return "LND TLS error"
	}
	if strings.Contains(msg, "connection refused") {
		return "LND gRPC connection refused"
	}
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "deadline exceeded") {
		return "LND gRPC timeout (retrying)"
	}
	return "LND not reachable"
}

func lndRPCErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "LND error"
	}
	lower := strings.ToLower(msg)
	if idx := strings.Index(lower, "desc ="); idx != -1 {
		detail := strings.TrimSpace(msg[idx+len("desc ="):])
		if detail != "" {
			return detail
		}
	}
	return msg
}

func lndDetailedErrorMessage(err error) string {
	msg := lndRPCErrorMessage(err)
	if msg == "" || msg == "LND error" {
		return lndStatusMessage(err)
	}
	return msg
}

func lndCloseErrorMessage(err error) string {
	msg := lndDetailedErrorMessage(err)
	if !isCloseFeeProposalExceededError(err) {
		return msg
	}
	return msg + ". Cooperative close fee negotiation failed. Retry with a higher sat/vB or leave fee empty (auto negotiation)."
}

func isCloseFeeProposalExceededError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "latest fee proposal exceeds max fee")
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	stats, err := system.GetSystemStats(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "system stats error")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleDisk(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	disks, err := system.ReadDiskSmart(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "smartctl error")
		return
	}
	writeJSON(w, http.StatusOK, disks)
}

type postgresResponse struct {
	ServiceActive bool               `json:"service_active"`
	DBName        string             `json:"db_name"`
	DBSizeMB      int64              `json:"db_size_mb"`
	Connections   int64              `json:"connections"`
	Version       string             `json:"version"`
	Databases     []postgresDatabase `json:"databases,omitempty"`
}

type postgresDatabase struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	SizeMB      int64  `json:"size_mb"`
	Connections int64  `json:"connections"`
	Available   bool   `json:"available"`
}

func (s *Server) handlePostgres(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	resp := s.postgresStatus(ctx)

	writeJSON(w, http.StatusOK, resp)
}

type postgresDSNEntry struct {
	Source string
	DSN    string
}

func postgresDSNEntries() []postgresDSNEntry {
	entries := []postgresDSNEntry{}
	if dsn := strings.TrimSpace(os.Getenv("LND_PG_DSN")); dsn != "" && !isPlaceholderDSN(dsn) {
		entries = append(entries, postgresDSNEntry{Source: "lnd", DSN: dsn})
	}
	if dsn := strings.TrimSpace(os.Getenv("NOTIFICATIONS_PG_DSN")); dsn != "" && !isPlaceholderDSN(dsn) {
		entries = append(entries, postgresDSNEntry{Source: "lightningos", DSN: dsn})
	}
	return entries
}

func databaseNameFromDSN(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	name := strings.TrimPrefix(parsed.Path, "/")
	return strings.TrimSpace(name)
}

type bitcoinStatus struct {
	Mode                  string               `json:"mode"`
	RPCHost               string               `json:"rpchost"`
	ZMQRawBlock           string               `json:"zmq_rawblock"`
	ZMQRawTx              string               `json:"zmq_rawtx"`
	RPCOk                 bool                 `json:"rpc_ok"`
	RPCStale              bool                 `json:"rpc_stale,omitempty"`
	RPCLastOKAgeSeconds   int64                `json:"rpc_last_ok_age_seconds,omitempty"`
	Installed             bool                 `json:"installed,omitempty"`
	Status                string               `json:"status,omitempty"`
	Source                string               `json:"source,omitempty"`
	DataDir               string               `json:"data_dir,omitempty"`
	ZMQRawBlockOk         bool                 `json:"zmq_rawblock_ok"`
	ZMQRawTxOk            bool                 `json:"zmq_rawtx_ok"`
	Connections           int                  `json:"connections,omitempty"`
	Version               int                  `json:"version,omitempty"`
	Subversion            string               `json:"subversion,omitempty"`
	Chain                 string               `json:"chain,omitempty"`
	Blocks                int64                `json:"blocks,omitempty"`
	Headers               int64                `json:"headers,omitempty"`
	VerificationProgress  float64              `json:"verification_progress,omitempty"`
	InitialBlockDownload  bool                 `json:"initial_block_download,omitempty"`
	BestBlockHash         string               `json:"best_block_hash,omitempty"`
	BestBlockTime         int64                `json:"best_block_time,omitempty"`
	Pruned                bool                 `json:"pruned,omitempty"`
	PruneHeight           int64                `json:"prune_height,omitempty"`
	PruneTargetSize       int64                `json:"prune_target_size,omitempty"`
	SizeOnDisk            int64                `json:"size_on_disk,omitempty"`
	BlockCadenceWindowSec int64                `json:"block_cadence_window_sec,omitempty"`
	BlockCadence          []blockCadenceBucket `json:"block_cadence,omitempty"`
}

type mempoolConnectivityNode struct {
	PublicKey string `json:"publicKey"`
	Alias     string `json:"alias"`
}

type mempoolNodeInfo struct {
	PublicKey string `json:"public_key"`
	Alias     string `json:"alias"`
	Sockets   string `json:"sockets"`
}

type mempoolFeeRecommendation struct {
	FastestFee  int `json:"fastestFee"`
	HalfHourFee int `json:"halfHourFee"`
	HourFee     int `json:"hourFee"`
	EconomyFee  int `json:"economyFee"`
	MinimumFee  int `json:"minimumFee"`
}

type boostPeersRequest struct {
	Limit     int  `json:"limit"`
	Permanent bool `json:"permanent"`
}

type boostPeerResult struct {
	Pubkey string `json:"pubkey"`
	Alias  string `json:"alias"`
	Socket string `json:"socket,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type boostPeersResponse struct {
	Requested int               `json:"requested"`
	Permanent bool              `json:"permanent"`
	Attempted int               `json:"attempted"`
	Connected int               `json:"connected"`
	Skipped   int               `json:"skipped"`
	Failed    int               `json:"failed"`
	Results   []boostPeerResult `json:"results"`
}

type bitcoinRPCConfig struct {
	Host     string
	User     string
	Pass     string
	ZMQBlock string
	ZMQTx    string
}

func (s *Server) handleBitcoin(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	status, err := s.bitcoinStatus(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bitcoin status error")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleBitcoinActive(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), bitcoinActiveHandlerTimeout(readBitcoinSource()))
	defer cancel()

	status, err := s.bitcoinActiveStatusCached(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bitcoin status error")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleBitcoinSourceGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"source": readBitcoinSource()})
}

func (s *Server) handleBitcoinSourcePost(w http.ResponseWriter, r *http.Request) {
	if s.rejectLNDMaintenanceAction(w, r, "Bitcoin source change") {
		return
	}
	var req struct {
		Source        string `json:"source"`
		AllowUnsynced bool   `json:"allow_unsynced"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source == "" {
		source = "remote"
	}
	if source != "remote" && source != "local" {
		writeError(w, http.StatusBadRequest, "source must be local or remote")
		return
	}
	if source == "local" && !req.AllowUnsynced {
		readyCtx, readyCancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer readyCancel()
		ready, _ := s.bitcoinLocalReady(readyCtx)
		if !ready {
			writeError(w, http.StatusBadRequest, "local bitcoin is not fully synced")
			return
		}
	}

	remoteUser, remotePass := readBitcoinSecrets()
	if source == "remote" && (remoteUser == "" || remotePass == "") {
		writeError(w, http.StatusBadRequest, "remote RPC credentials missing")
		return
	}
	remoteCfg := bitcoinRPCConfig{
		Host:     s.cfg.BitcoinRemote.RPCHost,
		User:     remoteUser,
		Pass:     remotePass,
		ZMQBlock: s.cfg.BitcoinRemote.ZMQRawBlock,
		ZMQTx:    s.cfg.BitcoinRemote.ZMQRawTx,
	}

	localCfg, localUpdated, err := readBitcoinLocalRPCConfig(r.Context())
	if err != nil && source == "local" {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err != nil && source != "local" {
		localCfg = bitcoinRPCConfig{
			Host:     "127.0.0.1:8332",
			ZMQBlock: "tcp://127.0.0.1:28332",
			ZMQTx:    "tcp://127.0.0.1:28333",
		}
		localUpdated = false
	}

	if err := updateLNDConfBitcoinSource(source, remoteCfg, localCfg); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update lnd.conf")
		return
	}
	if err := storeBitcoinSource(source); err != nil {
		s.logger.Printf("failed to store bitcoin source: %v", err)
	}
	s.invalidateBitcoinStatusCaches()

	needsBitcoinRestart := source == "local" && localUpdated
	if source == "local" && !needsBitcoinRestart {
		rpcCtx, rpcCancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer rpcCancel()
		if _, err := fetchBitcoinInfo(rpcCtx, localCfg.Host, localCfg.User, localCfg.Pass); err != nil {
			var statusErr rpcStatusError
			if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusForbidden {
				needsBitcoinRestart = true
			}
		}
	}

	if needsBitcoinRestart {
		paths := bitcoinCoreAppPaths()
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if err := runCompose(ctx, paths.Root, paths.ComposePath, "restart", "bitcoind"); err != nil {
			writeError(w, http.StatusInternalServerError, "bitcoin restart failed")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := restartLNDService(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			s.markLNDRestart()
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"warning": "LND restart is taking longer than expected. Check status in a moment.",
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "lnd restart failed")
		return
	}
	s.markLNDRestart()

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) bitcoinStatus(ctx context.Context) (bitcoinStatus, error) {
	rpcUser := os.Getenv("BITCOIN_RPC_USER")
	rpcPass := os.Getenv("BITCOIN_RPC_PASS")
	if rpcUser == "" || rpcPass == "" {
		fileUser, filePass := readBitcoinSecrets()
		if rpcUser == "" {
			rpcUser = fileUser
		}
		if rpcPass == "" {
			rpcPass = filePass
		}
	}
	status := bitcoinStatus{
		Mode:        "remote",
		RPCHost:     s.cfg.BitcoinRemote.RPCHost,
		ZMQRawBlock: s.cfg.BitcoinRemote.ZMQRawBlock,
		ZMQRawTx:    s.cfg.BitcoinRemote.ZMQRawTx,
	}

	if rpcUser != "" && rpcPass != "" {
		info, err := fetchBitcoinInfo(ctx, s.cfg.BitcoinRemote.RPCHost, rpcUser, rpcPass)
		if err == nil {
			status.RPCOk = true
			status.Chain = info.Chain
			status.Blocks = info.Blocks
			status.Headers = info.Headers
			status.VerificationProgress = info.VerificationProgress
			status.InitialBlockDownload = info.InitialBlockDownload
			status.BestBlockHash = info.BestBlockHash
			if netInfo, netErr := fetchBitcoinNetworkInfo(ctx, s.cfg.BitcoinRemote.RPCHost, rpcUser, rpcPass); netErr == nil {
				status.Version = netInfo.Version
				status.Subversion = netInfo.Subversion
			}
		} else {
			status.RPCOk = false
		}
	}

	status.ZMQRawBlockOk = testTCP(s.cfg.BitcoinRemote.ZMQRawBlock)
	status.ZMQRawTxOk = testTCP(s.cfg.BitcoinRemote.ZMQRawTx)

	return status, nil
}

func (s *Server) bitcoinLocalStatusActive(ctx context.Context) (bitcoinStatus, error) {
	paths := bitcoinCoreAppPaths()
	status := bitcoinStatus{
		Mode:        "local",
		RPCHost:     "127.0.0.1:8332",
		ZMQRawBlock: "tcp://127.0.0.1:28332",
		ZMQRawTx:    "tcp://127.0.0.1:28333",
	}
	if !fileExists(paths.ComposePath) {
		cfg, _, err := readBitcoinLocalRPCConfig(ctx)
		if err == nil && strings.TrimSpace(cfg.Host) != "" {
			status.RPCHost = cfg.Host
			if strings.TrimSpace(cfg.ZMQBlock) != "" {
				status.ZMQRawBlock = cfg.ZMQBlock
			}
			if strings.TrimSpace(cfg.ZMQTx) != "" {
				status.ZMQRawTx = cfg.ZMQTx
			}
		}
	}

	localStatus, err := s.bitcoinLocalStatusCached(ctx)
	if err != nil {
		return status, err
	}
	applyBitcoinLocalStatusToStatus(&status, localStatus)
	status.ZMQRawBlockOk = testTCP(status.ZMQRawBlock)
	status.ZMQRawTxOk = testTCP(status.ZMQRawTx)
	return status, nil
}

func readBitcoinLocalRPCConfig(ctx context.Context) (bitcoinRPCConfig, bool, error) {
	paths := bitcoinCoreAppPaths()
	if !fileExists(paths.ComposePath) {
		for _, candidate := range localBitcoinConfigCandidates(paths) {
			if cfg, ok := readBitcoinConfRPCConfig(candidate); ok {
				return cfg, false, nil
			}
		}
		if cfg, ok := readBitcoinTaggedRPCConfigFromLNDConf("local"); ok {
			return cfg, false, nil
		}
		if cfg, ok := readBitcoindRPCConfigFromLNDConf(); ok {
			if isLocalRPCHost(cfg.Host) {
				return cfg, false, nil
			}
		}
		return bitcoinRPCConfig{}, false, errors.New("bitcoin core is not installed")
	}
	raw, updated, err := syncBitcoinCoreRPCAllowList(ctx, paths)
	if err != nil {
		return bitcoinRPCConfig{}, false, fmt.Errorf("failed to read local bitcoin.conf: %w", err)
	}
	user, pass, zmqBlock, zmqTx := parseBitcoinCoreRPCConfig(raw)
	if user == "" || pass == "" {
		return bitcoinRPCConfig{}, false, errors.New("local RPC credentials missing")
	}
	zmqBlock = normalizeLocalZMQ(zmqBlock, "tcp://127.0.0.1:28332")
	zmqTx = normalizeLocalZMQ(zmqTx, "tcp://127.0.0.1:28333")
	return bitcoinRPCConfig{
		Host:     "127.0.0.1:8332",
		User:     user,
		Pass:     pass,
		ZMQBlock: zmqBlock,
		ZMQTx:    zmqTx,
	}, updated, nil
}

func localBitcoinConfigCandidates(paths bitcoinCorePaths) []string {
	return []string{
		paths.ConfigPath,
		"/etc/bitcoin/bitcoin.conf",
		"/var/lib/bitcoind/bitcoin.conf",
		"/home/bitcoin/.bitcoin/bitcoin.conf",
		"/home/admin/.bitcoin/bitcoin.conf",
		"/root/.bitcoin/bitcoin.conf",
	}
}

func readBitcoinConfRPCConfig(path string) (bitcoinRPCConfig, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return bitcoinRPCConfig{}, false
	}
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	user, pass, zmqBlock, zmqTx := parseBitcoinCoreRPCConfig(normalized)
	if strings.TrimSpace(user) == "" || strings.TrimSpace(pass) == "" {
		return bitcoinRPCConfig{}, false
	}
	zmqBlock = normalizeLocalZMQ(zmqBlock, "tcp://127.0.0.1:28332")
	zmqTx = normalizeLocalZMQ(zmqTx, "tcp://127.0.0.1:28333")

	host := "127.0.0.1:8332"
	if port, ok := parseBitcoinRPCPortFromConf(normalized); ok {
		host = fmt.Sprintf("127.0.0.1:%d", port)
	}
	return bitcoinRPCConfig{
		Host:     host,
		User:     user,
		Pass:     pass,
		ZMQBlock: zmqBlock,
		ZMQTx:    zmqTx,
	}, true
}

func parseBitcoinRPCPortFromConf(raw string) (int, bool) {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key != "rpcport" {
			continue
		}
		value := strings.TrimSpace(parts[1])
		if value == "" {
			continue
		}
		port, err := strconv.Atoi(value)
		if err != nil || port <= 0 {
			return 0, false
		}
		return port, true
	}
	return 0, false
}

func readBitcoindRPCConfigFromLNDConf() (bitcoinRPCConfig, bool) {
	raw, err := os.ReadFile(lndConfPath)
	if err != nil {
		return bitcoinRPCConfig{}, false
	}
	return parseBitcoindRPCConfigFromLNDConf(string(raw))
}

func parseBitcoindRPCConfigFromLNDConf(raw string) (bitcoinRPCConfig, bool) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	inBitcoind := false
	cfg := bitcoinRPCConfig{
		Host:     "127.0.0.1:8332",
		ZMQBlock: "tcp://127.0.0.1:28332",
		ZMQTx:    "tcp://127.0.0.1:28333",
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inBitcoind = strings.EqualFold(trimmed, "[Bitcoind]")
			continue
		}
		if !inBitcoind {
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "bitcoind.rpchost":
			if val != "" {
				cfg.Host = bitcoinRPCHostPort(val, 8332)
			}
		case "bitcoind.rpcuser":
			cfg.User = val
		case "bitcoind.rpcpass":
			cfg.Pass = val
		case "bitcoind.zmqpubrawblock":
			cfg.ZMQBlock = normalizeLocalZMQ(val, cfg.ZMQBlock)
		case "bitcoind.zmqpubrawtx":
			cfg.ZMQTx = normalizeLocalZMQ(val, cfg.ZMQTx)
		}
	}

	if strings.TrimSpace(cfg.Host) == "" {
		return bitcoinRPCConfig{}, false
	}
	if strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Pass) == "" {
		return bitcoinRPCConfig{}, false
	}
	return cfg, true
}

func readBitcoinTaggedRPCConfigFromLNDConf(tag string) (bitcoinRPCConfig, bool) {
	raw, err := os.ReadFile(lndConfPath)
	if err != nil {
		return bitcoinRPCConfig{}, false
	}
	return parseBitcoinTaggedRPCConfigFromLNDConf(string(raw), tag)
}

func parseBitcoinTaggedRPCConfigFromLNDConf(raw string, tag string) (bitcoinRPCConfig, bool) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	inBitcoind := false
	inTarget := false
	cfg := bitcoinRPCConfig{
		Host:     "127.0.0.1:8332",
		ZMQBlock: "tcp://127.0.0.1:28332",
		ZMQTx:    "tcp://127.0.0.1:28333",
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inBitcoind = strings.EqualFold(trimmed, "[Bitcoind]")
			inTarget = false
			continue
		}
		if !inBitcoind {
			continue
		}
		if trimmed == "" {
			continue
		}
		marker := strings.TrimSpace(strings.TrimLeft(trimmed, "#;"))
		if strings.EqualFold(marker, "LightningOS Bitcoin Remote") {
			inTarget = strings.EqualFold(tag, "remote")
			continue
		}
		if strings.EqualFold(marker, "LightningOS Bitcoin Local") {
			inTarget = strings.EqualFold(tag, "local")
			continue
		}
		if !inTarget {
			continue
		}
		clean := strings.TrimSpace(strings.TrimLeft(trimmed, "#;"))
		parts := strings.SplitN(clean, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "bitcoind.rpchost":
			if val != "" {
				cfg.Host = bitcoinRPCHostPort(val, 8332)
			}
		case "bitcoind.rpcuser":
			cfg.User = val
		case "bitcoind.rpcpass":
			cfg.Pass = val
		case "bitcoind.zmqpubrawblock":
			cfg.ZMQBlock = normalizeLocalZMQ(val, cfg.ZMQBlock)
		case "bitcoind.zmqpubrawtx":
			cfg.ZMQTx = normalizeLocalZMQ(val, cfg.ZMQTx)
		}
	}

	if strings.TrimSpace(cfg.Host) == "" {
		return bitcoinRPCConfig{}, false
	}
	if strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Pass) == "" {
		return bitcoinRPCConfig{}, false
	}
	return cfg, true
}

func readBitcoinSecrets() (string, string) {
	content, err := os.ReadFile(secretsPath)
	if err != nil {
		return "", ""
	}
	var user string
	var pass string
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "BITCOIN_RPC_USER=") {
			user = strings.TrimPrefix(line, "BITCOIN_RPC_USER=")
		}
		if strings.HasPrefix(line, "BITCOIN_RPC_PASS=") {
			pass = strings.TrimPrefix(line, "BITCOIN_RPC_PASS=")
		}
	}
	return strings.TrimSpace(user), strings.TrimSpace(pass)
}

func testTCP(addr string) bool {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return false
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func splitHostPort(input string) (string, string, error) {
	if strings.HasPrefix(input, "tcp://") {
		input = strings.TrimPrefix(input, "tcp://")
	}
	if strings.Contains(input, "://") {
		return "", "", fmt.Errorf("invalid address")
	}
	host, port, err := net.SplitHostPort(input)
	return host, port, err
}

type lndStatusResponse struct {
	ServiceActive   bool                  `json:"service_active"`
	WalletState     string                `json:"wallet_state"`
	SyncedToChain   bool                  `json:"synced_to_chain"`
	SyncedToGraph   bool                  `json:"synced_to_graph"`
	BlockHeight     int64                 `json:"block_height"`
	Version         string                `json:"version"`
	Pubkey          string                `json:"pubkey"`
	URI             string                `json:"uri"`
	URIs            []string              `json:"uris,omitempty"`
	InfoKnown       bool                  `json:"info_known"`
	InfoStale       bool                  `json:"info_stale"`
	InfoAgeSeconds  int64                 `json:"info_age_seconds"`
	DBBackend       string                `json:"db_backend"`
	ChannelDBSizeGB *float64              `json:"channel_db_size_gb,omitempty"`
	GraphSync       *lndGraphSyncProgress `json:"graph_sync,omitempty"`
	Channels        struct {
		Active   int `json:"active"`
		Inactive int `json:"inactive"`
		Pending  int `json:"pending"`
	} `json:"channels"`
	Peers struct {
		Connected int `json:"connected"`
	} `json:"peers"`
	Balances struct {
		OnchainSat   int64 `json:"onchain_sat"`
		LightningSat int64 `json:"lightning_sat"`
	} `json:"balances"`
}

func (s *Server) handleLNDStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()
	force, _ := strconv.ParseBool(strings.TrimSpace(r.URL.Query().Get("force")))

	resp, _ := s.lndStatus(ctx, force)

	writeJSON(w, http.StatusOK, resp)
}

func detectLNDDBBackend() string {
	raw, err := os.ReadFile(lndConfPath)
	if err != nil {
		return "unknown"
	}

	backend := ""
	postgresDSN := ""
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(strings.TrimSpace(strings.Trim(trimmed, "[]")))
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "db.backend":
			backend = strings.ToLower(value)
		case "db.postgres.dsn":
			postgresDSN = value
		}

		if section == "db" && key == "backend" {
			backend = strings.ToLower(value)
		}
		if section == "postgres" && key == "dsn" {
			postgresDSN = value
		}
	}

	if backend == "postgres" {
		return "postgres"
	}
	if strings.TrimSpace(postgresDSN) != "" {
		return "postgres"
	}
	return "bolt"
}

func lndChannelDBSizeGB() (float64, error) {
	sizeBytes, err := lndChannelDBSizeBytes()
	if err != nil {
		return 0, err
	}
	return float64(sizeBytes) / (1000.0 * 1000.0 * 1000.0), nil
}

func lndChannelDBSizeBytes() (int64, error) {
	info, err := os.Stat(lndChannelDBPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *Server) handleWizardStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"wallet_exists": walletExists(),
	})
}

type wizardBitcoinReq struct {
	RPCUser string `json:"rpcuser"`
	RPCPass string `json:"rpcpass"`
}

func (s *Server) handleWizardBitcoinRemote(w http.ResponseWriter, r *http.Request) {
	var req wizardBitcoinReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	user := strings.TrimSpace(req.RPCUser)
	pass := strings.TrimSpace(req.RPCPass)
	if user == "" || pass == "" {
		writeError(w, http.StatusBadRequest, "rpcuser and rpcpass required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	info, err := fetchBitcoinInfo(ctx, s.cfg.BitcoinRemote.RPCHost, user, pass)
	if err != nil {
		msg := "bitcoin rpc check failed"
		msg = fmt.Sprintf("bitcoin rpc check failed: %v", err)
		s.logger.Printf("bitcoin rpc check failed: %v", err)
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	if err := storeBitcoinSecrets(user, pass); err != nil {
		s.logger.Printf("failed to store secrets: %v", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store secrets: %v", err))
		return
	}

	if err := updateLNDConfRPC(
		ctx,
		user,
		pass,
		s.cfg.BitcoinRemote.RPCHost,
		s.cfg.BitcoinRemote.ZMQRawBlock,
		s.cfg.BitcoinRemote.ZMQRawTx,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update lnd.conf")
		return
	}

	_ = storeBitcoinSource("remote")

	_ = restartLNDService(ctx)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": info})
}

func (s *Server) handleCreateWallet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WalletPassword string `json:"wallet_password"`
		SeedPassphrase string `json:"seed_passphrase"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	if walletExists() {
		writeError(w, http.StatusConflict, "wallet already exists")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	seedPassphrase := strings.TrimSpace(req.SeedPassphrase)
	seed, err := s.lnd.GenSeed(ctx, seedPassphrase)
	if err != nil {
		s.logger.Printf("gen seed failed: %v", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("gen seed failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"seed_words": seed})
}

func walletExists() bool {
	if walletPasswordAvailable() {
		return true
	}
	info, err := os.Stat(lndWalletDBPath)
	if err != nil {
		return false
	}
	return info.Size() > 0
}

func (s *Server) handleInitWallet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WalletPassword string   `json:"wallet_password"`
		SeedWords      []string `json:"seed_words"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.WalletPassword == "" || len(req.SeedWords) == 0 {
		writeError(w, http.StatusBadRequest, "wallet_password and seed_words required")
		return
	}
	if walletExists() {
		writeError(w, http.StatusConflict, "wallet already exists")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	if err := s.lnd.InitWallet(ctx, req.WalletPassword, req.SeedWords); err != nil {
		writeError(w, http.StatusInternalServerError, "init wallet failed")
		return
	}
	if err := storeWalletUnlock(req.WalletPassword); err != nil {
		s.logger.Printf("wallet unlock setup failed: %v", err)
		writeError(w, http.StatusInternalServerError, "wallet unlock setup failed")
		return
	}
	s.scheduleLNDPermissionsFix("init wallet")

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUnlockWallet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WalletPassword string `json:"wallet_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.WalletPassword == "" {
		writeError(w, http.StatusBadRequest, "wallet_password required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	if err := s.lnd.UnlockWallet(ctx, req.WalletPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "unlock failed")
		return
	}
	if err := storeWalletUnlock(req.WalletPassword); err != nil {
		s.logger.Printf("wallet unlock setup failed: %v", err)
		writeError(w, http.StatusInternalServerError, "wallet unlock setup failed")
		return
	}
	s.scheduleLNDPermissionsFix("unlock wallet")

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service string `json:"service"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	service := mapService(req.Service)
	if service == "" {
		writeError(w, http.StatusBadRequest, "unsupported service")
		return
	}
	startedAt := time.Now().UTC()

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	var err error
	if service == "lnd" {
		err = system.SystemctlRestartNoBlock(ctx, service)
	} else {
		err = system.SystemctlRestart(ctx, service)
	}
	if err != nil {
		if service == "lnd" && s.logger != nil {
			s.logger.Printf("lnd restart command failed: %v", err)
		}
		if service == "lnd" {
			writeError(w, http.StatusInternalServerError, "lnd restart command failed; check manager sudoers for systemctl restart --no-block lnd")
			return
		}
		writeError(w, http.StatusInternalServerError, "restart failed")
		return
	}
	if service == "lnd" {
		s.markLNDRestart()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"started_at": startedAt.Format(time.RFC3339Nano),
	})
}

func (s *Server) handleSystemAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case "restart":
		action = "reboot"
	case "shutdown":
		action = "poweroff"
	}
	if action != "reboot" && action != "poweroff" {
		writeError(w, http.StatusBadRequest, "unsupported action")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	if err := system.SystemctlPower(ctx, action); err != nil {
		writeError(w, http.StatusInternalServerError, "system action failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func mapService(name string) string {
	switch name {
	case "lnd":
		return "lnd"
	case "autofee":
		return "autofee"
	case "lightningos-manager":
		return "lightningos-manager"
	case "lightningos-elements", "elementsd":
		return elementsServiceName
	case "lightningos-peerswapd", "peerswapd":
		return peerswapServiceName
	case "lightningos-psweb", "psweb":
		return pswebServiceName
	case "lnd-upgrade", "lightningos-lnd-upgrade":
		return lndUpgradeUnitName
	case "app-upgrade", "lightningos-app-upgrade":
		return appUpgradeUnitName
	case "tor-upgrade", "lightningos-tor-upgrade":
		return torUpgradeUnitName
	case "postgresql":
		return "postgresql"
	default:
		return ""
	}
}

func parsePeerAddress(address string) (string, string, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return "", "", errors.New("address required")
	}
	parts := strings.Split(trimmed, "@")
	if len(parts) != 2 {
		return "", "", errors.New("address must be pubkey@host")
	}
	pubkey := strings.TrimSpace(parts[0])
	host := strings.TrimSpace(parts[1])
	if pubkey == "" || host == "" {
		return "", "", errors.New("address must be pubkey@host")
	}
	return pubkey, host, nil
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	serviceRaw := strings.TrimSpace(r.URL.Query().Get("service"))
	linesRaw := r.URL.Query().Get("lines")
	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))

	lines := 200
	if linesRaw != "" {
		if v, err := strconv.Atoi(linesRaw); err == nil {
			lines = v
		}
	}

	if isBitcoinLogService(serviceRaw) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()

		out, source, err := s.readBitcoinLocalLogLines(ctx, lines, sinceRaw)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("log read failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"service": "bitcoin", "source": source, "lines": sanitizeLogLines(out)})
		return
	}

	if isFedimintLogService(serviceRaw) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()

		out, source, err := readFedimintComposeLogLines(ctx, serviceRaw, lines, sinceRaw)
		if err != nil {
			if errors.Is(err, errFedimintLogServiceNotInstalled) {
				writeError(w, http.StatusBadRequest, "Fedimint app is not installed")
				return
			}
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("log read failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"service": strings.ToLower(strings.TrimSpace(serviceRaw)), "source": source, "lines": sanitizeLogLines(out)})
		return
	}

	service := mapService(serviceRaw)
	if service == "" {
		writeError(w, http.StatusBadRequest, "unsupported service")
		return
	}

	timeout := 4 * time.Second
	switch service {
	case "lnd":
		timeout = 12 * time.Second
	case "lightningos-manager", "postgresql":
		timeout = 8 * time.Second
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if service == "autofee" {
		lines := parseAutofeeLimit(linesRaw)
		out, err := s.readAutofeeLogLines(ctx, lines)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("log read failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"service": service, "lines": sanitizeLogLines(out)})
		return
	}

	out, err := system.JournalTailSince(ctx, service, lines, sinceRaw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("log read failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"service": service, "lines": sanitizeLogLines(out)})
}

func isBitcoinLogService(service string) bool {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "bitcoin", "bitcoind", "bitcoin-core", bitcoinCoreAppID:
		return true
	default:
		return false
	}
}

func (s *Server) readBitcoinLocalLogLines(ctx context.Context, lines int, since string) ([]string, string, error) {
	paths := bitcoinCoreAppPaths()
	var dockerErr error
	if fileExists(paths.ComposePath) {
		out, err := readBitcoinComposeLogLines(ctx, paths, lines, since)
		if err == nil {
			return out, "docker:bitcoind", nil
		}
		dockerErr = err
	}

	if service := bitcoinSystemdLogService(ctx); service != "" {
		out, err := system.JournalTailSince(ctx, service, lines, since)
		if err == nil {
			return out, "systemd:" + service, nil
		}
		if dockerErr != nil {
			return nil, "", fmt.Errorf("docker log read failed: %v; systemd log read failed: %w", dockerErr, err)
		}
		return nil, "", err
	}

	if dockerErr != nil {
		return nil, "", dockerErr
	}

	out, err := system.JournalTailSince(ctx, "bitcoind", lines, since)
	if err != nil {
		return nil, "", errors.New("local bitcoin logs not available")
	}
	return out, "systemd:bitcoind", nil
}

func readBitcoinComposeLogLines(ctx context.Context, paths bitcoinCorePaths, lines int, since string) ([]string, error) {
	return readComposeServiceLogLines(ctx, paths.Root, paths.ComposePath, "bitcoind", lines, since)
}

func readComposeServiceLogLines(ctx context.Context, root string, composePath string, serviceName string, lines int, since string) ([]string, error) {
	if lines <= 0 {
		lines = 200
	}
	cmd, baseArgs, err := resolveCompose(ctx)
	if err != nil {
		return nil, err
	}
	fullArgs := append(baseArgs, composeBaseArgs(root, composePath)...)
	fullArgs = append(fullArgs, "logs", "--no-color", "--tail", strconv.Itoa(lines))
	if strings.TrimSpace(since) != "" {
		fullArgs = append(fullArgs, "--since", strings.TrimSpace(since))
	}
	fullArgs = append(fullArgs, serviceName)

	out, err := system.RunCommandWithSudo(ctx, cmd, fullArgs...)
	if err != nil {
		return nil, err
	}
	return splitLogLines(out), nil
}

func bitcoinSystemdLogService(ctx context.Context) string {
	for _, service := range []string{
		"bitcoind",
		"bitcoin",
		"bitcoin-core",
		"snap.bitcoin-core.bitcoind",
	} {
		if systemdUnitLoaded(ctx, service) {
			return service
		}
	}
	return ""
}

func systemdUnitLoaded(ctx context.Context, service string) bool {
	out, err := system.RunCommand(ctx, "systemctl", "show", "-p", "LoadState", "--value", service)
	if err != nil {
		return false
	}
	state := strings.TrimSpace(out)
	return state != "" && state != "not-found"
}

func splitLogLines(out string) []string {
	raw := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		sanitized := sanitizeLogLine(line)
		if strings.TrimSpace(sanitized) == "" {
			continue
		}
		lines = append(lines, sanitized)
	}
	return lines
}

func sanitizeLogLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		sanitized := sanitizeLogLine(line)
		if strings.TrimSpace(sanitized) == "" {
			continue
		}
		out = append(out, sanitized)
	}
	return out
}

func sanitizeLogLine(line string) string {
	line = ansiOSCRegexp.ReplaceAllString(line, "")
	line = ansiCSIRegexp.ReplaceAllString(line, "")
	line = ansiSingleCharRegex.ReplaceAllString(line, "")
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, line)
}

func (s *Server) handleLNDConfigGet(w http.ResponseWriter, r *http.Request) {
	raw, _ := os.ReadFile(lndConfPath)

	current := parseLNDUserConf(string(raw))

	resp := map[string]any{
		"supported": map[string]bool{
			"alias":                         true,
			"color":                         true,
			"min_channel_size_sat":          true,
			"max_channel_size_sat":          true,
			"network_mode":                  true,
			"graph_sync_peers":              true,
			"disconnect_unresponsive_peers": true,
		},
		"current": map[string]any{
			"alias":                               current.Alias,
			"color":                               current.Color,
			"min_channel_size_sat":                current.MinChanSize,
			"max_channel_size_sat":                current.MaxChanSize,
			"network_mode":                        current.networkMode(),
			"graph_sync_peers":                    current.GraphSyncPeers,
			"disconnect_unresponsive_peers":       !current.NoDisconnectOnPongFailure,
			"tor_active":                          current.TorActive,
			"tor_skip_proxy_for_clearnet_targets": current.TorSkipProxyForClearnet,
			"tor_stream_isolation":                current.TorStreamIsolation,
		},
		"raw_user_conf": string(raw),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLNChannels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	currentBlockHeight := int64(0)
	if status, statusErr := s.lnd.GetStatus(ctx); statusErr == nil {
		currentBlockHeight = status.BlockHeight
	}

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	pending, pendingErr := s.lnd.ListPendingChannels(ctx)
	if pendingErr != nil {
		pending = nil
	}
	pendingResp := make([]pendingChannelResponse, 0, len(pending))
	for _, item := range pending {
		row := pendingChannelResponse{PendingChannelInfo: item}
		if item.Status == "waiting_close" && strings.TrimSpace(item.ClosingTxid) == "" && s.notifier != nil {
			if info, ok := s.notifier.getWaitingCloseRecoveryInfo(item.ChannelPoint); ok {
				row.WaitingCloseRecovery = buildWaitingCloseRecoveryResponse(info)
			}
		}
		pendingResp = append(pendingResp, row)
	}

	if s.db != nil {
		dbCtx, dbCancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer dbCancel()
		if err := s.applyPersistedChannelDowntime(dbCtx, channels); err != nil {
			s.logger.Printf("channel downtime persistence sync failed: %v", err)
		}
		rows, err := s.db.Query(dbCtx, `
      select channel_id, class_label
      from autofee_state
      where class_label is not null and class_label <> ''
    `)
		if err != nil {
			s.logger.Printf("autofee channel class lookup failed: %v", err)
		} else {
			labels := make(map[uint64]string)
			for rows.Next() {
				var channelID uint64
				var label string
				if err := rows.Scan(&channelID, &label); err != nil {
					s.logger.Printf("autofee channel class scan failed: %v", err)
					continue
				}
				if label != "" {
					labels[channelID] = label
				}
			}
			if err := rows.Err(); err != nil {
				s.logger.Printf("autofee channel class rows failed: %v", err)
			}
			rows.Close()
			if len(labels) > 0 {
				for i := range channels {
					if label, ok := labels[channels[i].ChannelID]; ok {
						channels[i].ClassLabel = label
					}
				}
			}
		}

		movementByChannel, err := s.loadChannelMovement7d(dbCtx)
		if err != nil {
			s.logger.Printf("channel movement lookup failed: %v", err)
		} else {
			for i := range channels {
				movement := movementByChannel[channels[i].ChannelID]
				channels[i].Movement7d = &lndclient.ChannelMovement7d{
					ForwardCount:          movement.ForwardCount,
					ForwardAmountSat:      movement.ForwardAmountSat,
					ForwardInCount:        movement.ForwardInCount,
					ForwardInAmountSat:    movement.ForwardInAmountSat,
					ForwardOutCount:       movement.ForwardOutCount,
					ForwardOutAmountSat:   movement.ForwardOutAmountSat,
					RebalanceCount:        movement.RebalanceCount,
					RebalanceAmountSat:    movement.RebalanceAmountSat,
					RebalanceInCount:      movement.RebalanceInCount,
					RebalanceInAmountSat:  movement.RebalanceInAmountSat,
					RebalanceOutCount:     movement.RebalanceOutCount,
					RebalanceOutAmountSat: movement.RebalanceOutAmountSat,
					LightningOutCount:     movement.LightningOutCount,
					LightningOutAmountSat: movement.LightningOutAmountSat,
					LightningInCount:      movement.LightningInCount,
					LightningInAmountSat:  movement.LightningInAmountSat,
				}
			}
		}

		forwardFeeMsat := make(map[uint64]int64)
		forwardAmtMsat := make(map[uint64]int64)
		forwardRows, err := s.db.Query(dbCtx, `
      select coalesce(chan_id_out, channel_id) as chan_id,
        coalesce(sum(
          case
            when fee_msat > 0 then fee_msat
            when fee_sat > 0 then fee_sat * 1000
            when amount_in_msat > 0 and amount_out_msat > 0 and amount_in_msat > amount_out_msat then amount_in_msat - amount_out_msat
            else 0
          end
        ), 0),
        coalesce(sum(case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end), 0)
      from notifications
      where type='forward' and occurred_at >= now() - interval '7 day'
        and coalesce(chan_id_out, channel_id) is not null
      group by coalesce(chan_id_out, channel_id)
    `)
		if err != nil {
			s.logger.Printf("channel economics forward lookup failed: %v", err)
		} else {
			for forwardRows.Next() {
				var channelID int64
				var fee int64
				var amt int64
				if err := forwardRows.Scan(&channelID, &fee, &amt); err != nil {
					s.logger.Printf("channel economics forward scan failed: %v", err)
					continue
				}
				if channelID <= 0 {
					continue
				}
				cid := uint64(channelID)
				forwardFeeMsat[cid] = fee
				forwardAmtMsat[cid] = amt
			}
			if err := forwardRows.Err(); err != nil {
				s.logger.Printf("channel economics forward rows failed: %v", err)
			}
			forwardRows.Close()
		}

		rebalFeeMsat := make(map[uint64]int64)
		rebalAmtMsat := make(map[uint64]int64)
		rebalRows, err := s.db.Query(dbCtx, `
      select coalesce(rebal_target_chan_id, channel_id) as chan_id,
        coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0),
        coalesce(sum(
          case
            when amount_sat > 0 then amount_sat * 1000
            when amount_out_msat > 0 then amount_out_msat
            else 0
          end
        ), 0)
      from notifications
      where type='rebalance' and occurred_at >= now() - interval '7 day'
        and status in ('SETTLED', 'SUCCEEDED')
        and coalesce(rebal_target_chan_id, channel_id) is not null
      group by coalesce(rebal_target_chan_id, channel_id)
    `)
		if err != nil {
			s.logger.Printf("channel economics rebalance lookup failed: %v", err)
		} else {
			for rebalRows.Next() {
				var channelID int64
				var fee int64
				var amt int64
				if err := rebalRows.Scan(&channelID, &fee, &amt); err != nil {
					s.logger.Printf("channel economics rebalance scan failed: %v", err)
					continue
				}
				if channelID <= 0 {
					continue
				}
				cid := uint64(channelID)
				rebalFeeMsat[cid] = fee
				rebalAmtMsat[cid] = amt
			}
			if err := rebalRows.Err(); err != nil {
				s.logger.Printf("channel economics rebalance rows failed: %v", err)
			}
			rebalRows.Close()
		}

		for i := range channels {
			chID := channels[i].ChannelID
			fwdFeeMsat := forwardFeeMsat[chID]
			fwdAmt := forwardAmtMsat[chID]
			rebFeeMsat := rebalFeeMsat[chID]
			rebAmt := rebalAmtMsat[chID]

			hasForward := fwdFeeMsat > 0 || fwdAmt > 0
			hasRebal := rebFeeMsat > 0 || rebAmt > 0
			if !hasForward && !hasRebal {
				continue
			}

			if hasForward {
				outPpm := ppmMsat(fwdFeeMsat, fwdAmt)
				channels[i].OutPpm7d = &outPpm
				fwdSat := msatToSatCeil(fwdFeeMsat)
				channels[i].ForwardFee7dSat = &fwdSat
			}
			if hasRebal {
				rebPpm := ppmMsat(rebFeeMsat, rebAmt)
				channels[i].RebalPpm7d = &rebPpm
				rebSat := msatToSatCeil(rebFeeMsat)
				channels[i].RebalFee7dSat = &rebSat
			}

			profitSat := msatToSatCeil(fwdFeeMsat) - msatToSatCeil(rebFeeMsat)
			channels[i].ProfitFee7dSat = &profitSat
		}
	}

	fundingTxCtx, fundingTxCancel := context.WithTimeout(r.Context(), 4*time.Second)
	enrichPendingOpenFundingTransactions(fundingTxCtx, pendingResp)
	fundingTxCancel()

	active := 0
	inactive := 0
	for _, ch := range channels {
		if ch.Active {
			active++
		} else {
			inactive++
		}
	}

	pendingOpen := 0
	pendingClose := 0
	for _, ch := range pendingResp {
		if ch.Status == "opening" {
			pendingOpen++
			continue
		}
		pendingClose++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active_count":         active,
		"inactive_count":       inactive,
		"pending_open_count":   pendingOpen,
		"pending_close_count":  pendingClose,
		"current_block_height": currentBlockHeight,
		"channels":             channels,
		"pending_channels":     pendingResp,
	})
}

func (s *Server) loadChannelMovement7d(ctx context.Context) (map[uint64]lndclient.ChannelMovement7d, error) {
	stats := make(map[uint64]lndclient.ChannelMovement7d)
	if s == nil || s.db == nil {
		return stats, nil
	}

	rows, err := s.db.Query(ctx, `
with movement as (
  select
    chan_id_in as channel_id,
    'forward_in'::text as metric,
    coalesce(case when amount_in_msat > 0 then amount_in_msat / 1000 else amount_sat end, 0) as amount_sat
  from notifications
  where type='forward'
    and occurred_at >= now() - interval '7 day'
    and chan_id_in is not null

  union all

  select
    chan_id_out as channel_id,
    'forward_out'::text as metric,
    coalesce(case when amount_out_msat > 0 then amount_out_msat / 1000 else amount_sat end, 0) as amount_sat
  from notifications
  where type='forward'
    and occurred_at >= now() - interval '7 day'
    and chan_id_out is not null

  union all

  select
    rebal_target_chan_id as channel_id,
    'rebalance_in'::text as metric,
    coalesce(case when amount_sat > 0 then amount_sat when amount_out_msat > 0 then amount_out_msat / 1000 else 0 end, 0) as amount_sat
  from notifications
  where type='rebalance'
    and status in ('SETTLED', 'SUCCEEDED')
    and occurred_at >= now() - interval '7 day'
    and rebal_target_chan_id is not null

  union all

  select
    rebal_source_chan_id as channel_id,
    'rebalance_out'::text as metric,
    coalesce(case when amount_sat > 0 then amount_sat when amount_out_msat > 0 then amount_out_msat / 1000 else 0 end, 0) as amount_sat
  from notifications
  where type='rebalance'
    and status in ('SETTLED', 'SUCCEEDED')
    and occurred_at >= now() - interval '7 day'
    and rebal_source_chan_id is not null

  union all

  select
    channel_id,
    'lightning_out'::text as metric,
    coalesce(amount_sat, 0) as amount_sat
  from notifications
  where type in ('lightning', 'keysend')
    and action='sent'
    and status='SUCCEEDED'
    and occurred_at >= now() - interval '7 day'
    and channel_id is not null

  union all

  select
    channel_id,
    'lightning_in'::text as metric,
    coalesce(amount_sat, 0) as amount_sat
  from notifications
  where type in ('lightning', 'keysend')
    and action='received'
    and status='SETTLED'
    and occurred_at >= now() - interval '7 day'
    and channel_id is not null
)
select channel_id, metric, count(*)::int, coalesce(sum(amount_sat), 0)
from movement
where channel_id > 0
group by channel_id, metric
`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	for rows.Next() {
		var channelID int64
		var metric string
		var count int
		var amountSat int64
		if err := rows.Scan(&channelID, &metric, &count, &amountSat); err != nil {
			return stats, err
		}
		if channelID <= 0 {
			continue
		}
		cid := uint64(channelID)
		entry := stats[cid]
		switch metric {
		case "forward_in":
			entry.ForwardInCount = count
			entry.ForwardInAmountSat = amountSat
			entry.ForwardCount += count
			entry.ForwardAmountSat += amountSat
		case "forward_out":
			entry.ForwardOutCount = count
			entry.ForwardOutAmountSat = amountSat
			entry.ForwardCount += count
			entry.ForwardAmountSat += amountSat
		case "rebalance_in":
			entry.RebalanceInCount = count
			entry.RebalanceInAmountSat = amountSat
			entry.RebalanceCount += count
			entry.RebalanceAmountSat += amountSat
		case "rebalance_out":
			entry.RebalanceOutCount = count
			entry.RebalanceOutAmountSat = amountSat
			entry.RebalanceCount += count
			entry.RebalanceAmountSat += amountSat
		case "lightning_out":
			entry.LightningOutCount = count
			entry.LightningOutAmountSat = amountSat
		case "lightning_in":
			entry.LightningInCount = count
			entry.LightningInAmountSat = amountSat
		}
		stats[cid] = entry
	}

	return stats, rows.Err()
}

func buildWaitingCloseRecoveryResponse(info waitingCloseRecoveryInfo) *waitingCloseRecoveryResponse {
	resp := &waitingCloseRecoveryResponse{
		Attempts:          info.Attempts,
		LastResult:        strings.TrimSpace(info.LastResult),
		LastError:         strings.TrimSpace(info.LastError),
		LastRecoveredTxid: strings.TrimSpace(info.LastRecoveredTxid),
	}
	if !info.LastAttemptAt.IsZero() {
		resp.LastAttemptAt = info.LastAttemptAt.UTC().Format(time.RFC3339)
	}
	resp.SuggestForceClose = shouldSuggestWaitingCloseForce(info)
	return resp
}

func shouldSuggestWaitingCloseForce(info waitingCloseRecoveryInfo) bool {
	result := strings.TrimSpace(info.LastResult)
	switch result {
	case "recover_failed":
		return info.Attempts >= 3
	case "no_raw_tx_available", "recovery_submitted_no_txid", "rebroadcast_submitted_no_txid":
		return info.Attempts >= 6
	case "rebroadcast_ok", "closing_txid_detected":
		return false
	default:
		return info.Attempts >= 6
	}
}

func (s *Server) handleLNPeers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	peers, err := s.lnd.ListPeers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	peers = s.enrichPeerAliasesFromGraph(ctx, peers)
	writeJSON(w, http.StatusOK, map[string]any{"peers": peers})
}

func (s *Server) handleLNChannelPeerRecommendations(w http.ResponseWriter, r *http.Request) {
	channelPoint := strings.TrimSpace(r.URL.Query().Get("channel_point"))
	if channelPoint == "" {
		writeError(w, http.StatusBadRequest, "channel_point required")
		return
	}

	limit := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if parsed > 20 {
			parsed = 20
		}
		limit = parsed
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	var selected *lndclient.ChannelInfo
	excludePubkeys := make(map[string]struct{}, len(channels)+2)
	for i := range channels {
		pubkey := strings.TrimSpace(channels[i].RemotePubkey)
		if pubkey != "" {
			excludePubkeys[strings.ToLower(pubkey)] = struct{}{}
		}
		if channels[i].ChannelPoint == channelPoint {
			selected = &channels[i]
		}
	}
	if selected == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	if pubkey := strings.TrimSpace(selected.RemotePubkey); pubkey != "" {
		excludePubkeys[strings.ToLower(pubkey)] = struct{}{}
	}

	if status, err := s.lnd.GetStatus(ctx); err == nil {
		if pubkey := strings.TrimSpace(status.Pubkey); pubkey != "" {
			excludePubkeys[strings.ToLower(pubkey)] = struct{}{}
		}
	}

	recommendations, selectionTier, err := s.lnd.GetPeerNeighborRecommendations(ctx, selected.RemotePubkey, excludePubkeys, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channel_point":         selected.ChannelPoint,
		"peer_pubkey":           selected.RemotePubkey,
		"peer_alias":            selected.PeerAlias,
		"selection_tier":        selectionTier,
		"recommendations":       recommendations,
		"recommendations_count": len(recommendations),
	})
}

func (s *Server) handleLNClosedChannels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	channels, err := s.lnd.ListClosedChannels(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}
	enrichClosedChannelTimes(ctx, channels)

	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (s *Server) handleLNWatchtowers(w http.ResponseWriter, r *http.Request) {
	includeSessions := false
	excludeExhaustedSessions := false
	if raw := strings.TrimSpace(r.URL.Query().Get("include_sessions")); raw != "" {
		includeSessions = raw == "1" || strings.EqualFold(raw, "true")
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("exclude_exhausted_sessions")); raw != "" {
		excludeExhaustedSessions = raw == "1" || strings.EqualFold(raw, "true")
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	towers, err := s.lnd.ListWatchtowers(ctx, includeSessions, excludeExhaustedSessions)
	if err != nil {
		writeError(w, http.StatusInternalServerError, watchtowerRPCErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"towers": towers})
}

func (s *Server) handleLNWatchtowerAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	pubkey, host, err := parsePeerAddress(req.Address)
	if err != nil {
		writeError(w, http.StatusBadRequest, "address must be pubkey@host:port")
		return
	}
	if !strings.Contains(host, ":") {
		writeError(w, http.StatusBadRequest, "address must be pubkey@host:port")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	if err := s.lnd.AddWatchtower(ctx, pubkey, host); err != nil {
		writeError(w, http.StatusInternalServerError, watchtowerRPCErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNWatchtowerRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pubkey  string `json:"pubkey"`
		Address string `json:"address"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	pubkey := strings.TrimSpace(req.Pubkey)
	address := strings.TrimSpace(req.Address)
	if strings.Contains(address, "@") {
		parsedPubkey, parsedAddress, err := parsePeerAddress(address)
		if err != nil {
			writeError(w, http.StatusBadRequest, "address must be host:port or pubkey@host:port")
			return
		}
		if pubkey == "" {
			pubkey = parsedPubkey
		}
		address = parsedAddress
	}
	if pubkey == "" {
		writeError(w, http.StatusBadRequest, "pubkey required")
		return
	}
	if address != "" && !strings.Contains(address, ":") {
		writeError(w, http.StatusBadRequest, "address must be host:port or pubkey@host:port")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	if err := s.lnd.RemoveWatchtower(ctx, pubkey, address); err != nil {
		writeError(w, http.StatusInternalServerError, watchtowerRPCErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNSignMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		writeError(w, http.StatusBadRequest, "message required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	signature, err := s.lnd.SignMessage(ctx, message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"signature": signature})
}

func (s *Server) handleLNConnectPeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
		Pubkey  string `json:"pubkey"`
		Host    string `json:"host"`
		Perm    *bool  `json:"perm"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	pubkey := strings.TrimSpace(req.Pubkey)
	host := strings.TrimSpace(req.Host)
	if req.Address != "" {
		parsedPubkey, parsedHost, err := parsePeerAddress(req.Address)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		pubkey = parsedPubkey
		host = parsedHost
	}
	if pubkey == "" || host == "" {
		writeError(w, http.StatusBadRequest, "pubkey and host required")
		return
	}

	perm := true
	if req.Perm != nil {
		perm = *req.Perm
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndConnectTimeout)
	defer cancel()

	alreadyConnected := false
	if err := s.lnd.ConnectPeerWithTimeout(ctx, pubkey, host, perm, uint64(lndConnectTimeout/time.Second)); err != nil {
		if !isAlreadyConnected(err) {
			writeError(w, http.StatusInternalServerError, peerConnectErrorMessage(err))
			return
		}
		alreadyConnected = true
	}

	writeJSON(w, http.StatusOK, map[string]bool{
		"ok":                true,
		"already_connected": alreadyConnected,
	})
}

func (s *Server) handleLNDisconnectPeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pubkey string `json:"pubkey"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	pubkey := strings.TrimSpace(req.Pubkey)
	if pubkey == "" {
		writeError(w, http.StatusBadRequest, "pubkey required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	if err := s.lnd.DisconnectPeer(ctx, pubkey); err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNBoostPeers(w http.ResponseWriter, r *http.Request) {
	var req boostPeersRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	limit := normalizeBoostPeerModeLimit(req.Limit, req.Permanent)

	peersCtx, peersCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	peers, err := s.lnd.ListPeers(peersCtx)
	peersCancel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	existing := map[string]bool{}
	for _, peer := range peers {
		if peer.PubKey != "" {
			existing[peer.PubKey] = true
		}
	}

	rankingCtx, rankingCancel := context.WithTimeout(r.Context(), 10*time.Second)
	ranking, err := fetchMempoolConnectivity(rankingCtx)
	rankingCancel()
	if err != nil {
		s.logger.Printf("mempool connectivity fetch failed: %v", err)
		writeError(w, http.StatusInternalServerError, "mempool connectivity fetch failed")
		return
	}

	if limit > len(ranking) {
		limit = len(ranking)
	}

	resp := boostPeersResponse{
		Requested: limit,
		Permanent: req.Permanent,
	}
	results := make([]boostPeerResult, 0, limit)

	// Search past peers that are already connected or have no usable socket so
	// the requested number describes successful new connections, not merely the
	// number of ranking rows inspected. Bound the scan to keep the request fast.
	candidateLimit := limit * 3
	if candidateLimit < 5 {
		candidateLimit = 5
	}
	if candidateLimit > boostPeersMaxLimit {
		candidateLimit = boostPeersMaxLimit
	}
	if candidateLimit > len(ranking) {
		candidateLimit = len(ranking)
	}
	for i := 0; i < candidateLimit && resp.Connected < limit; i++ {
		node := ranking[i]
		pubkey := strings.TrimSpace(node.PublicKey)
		alias := strings.TrimSpace(node.Alias)
		if pubkey == "" {
			results = append(results, boostPeerResult{
				Alias:  alias,
				Status: "skipped",
				Error:  "missing pubkey",
			})
			resp.Skipped++
			continue
		}
		if existing[pubkey] {
			results = append(results, boostPeerResult{
				Pubkey: pubkey,
				Alias:  alias,
				Status: "skipped",
				Error:  "already connected",
			})
			resp.Skipped++
			continue
		}

		infoCtx, infoCancel := context.WithTimeout(r.Context(), 8*time.Second)
		info, err := fetchMempoolNodeInfo(infoCtx, pubkey)
		infoCancel()
		if err != nil {
			results = append(results, boostPeerResult{
				Pubkey: pubkey,
				Alias:  alias,
				Status: "failed",
				Error:  "mempool node lookup failed",
			})
			resp.Failed++
			continue
		}
		if alias == "" {
			alias = strings.TrimSpace(info.Alias)
		}
		socket := firstSocket(info.Sockets)
		if socket == "" {
			results = append(results, boostPeerResult{
				Pubkey: pubkey,
				Alias:  alias,
				Status: "skipped",
				Error:  "no socket found",
			})
			resp.Skipped++
			continue
		}

		connectCtx, connectCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
		// Temporary boost remains the default. The explicit persistent-anchor
		// mode is capped at one peer because permanent peers are pinned gossip
		// syncers in LND and can bypass numgraphsyncpeers.
		err = s.lnd.ConnectPeer(connectCtx, pubkey, socket, req.Permanent)
		connectCancel()
		resp.Attempted++
		if err != nil {
			if isAlreadyConnected(err) {
				results = append(results, boostPeerResult{
					Pubkey: pubkey,
					Alias:  alias,
					Socket: socket,
					Status: "skipped",
					Error:  "already connected",
				})
				resp.Skipped++
			} else {
				results = append(results, boostPeerResult{
					Pubkey: pubkey,
					Alias:  alias,
					Socket: socket,
					Status: "failed",
					Error:  err.Error(),
				})
				resp.Failed++
			}
			continue
		}

		existing[pubkey] = true
		results = append(results, boostPeerResult{
			Pubkey: pubkey,
			Alias:  alias,
			Socket: socket,
			Status: "connected",
		})
		resp.Connected++
	}

	resp.Results = results
	writeJSON(w, http.StatusOK, resp)
}

func normalizeBoostPeerLimit(requested int) int {
	if requested <= 0 {
		return boostPeersDefaultLimit
	}
	if requested > boostPeersMaxLimit {
		return boostPeersMaxLimit
	}
	return requested
}

func normalizeBoostPeerModeLimit(requested int, permanent bool) int {
	limit := normalizeBoostPeerLimit(requested)
	if permanent && limit > boostPeersPersistentLimit {
		return boostPeersPersistentLimit
	}
	return limit
}

func fetchMempoolConnectivity(ctx context.Context) ([]mempoolConnectivityNode, error) {
	var nodes []mempoolConnectivityNode
	url := mempoolBaseURL + "/nodes/rankings/connectivity"
	if err := fetchMempoolJSON(ctx, url, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func fetchMempoolNodeInfo(ctx context.Context, pubkey string) (mempoolNodeInfo, error) {
	var info mempoolNodeInfo
	url := mempoolBaseURL + "/nodes/" + pubkey
	if err := fetchMempoolJSON(ctx, url, &info); err != nil {
		return mempoolNodeInfo{}, err
	}
	return info, nil
}

func fetchMempoolJSON(ctx context.Context, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("mempool api error: %s", msg)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func firstSocket(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return ""
	}
	socket := strings.TrimSpace(parts[0])
	if socket == "" {
		return ""
	}
	if strings.Contains(socket, "@") {
		pieces := strings.SplitN(socket, "@", 2)
		if len(pieces) == 2 {
			socket = strings.TrimSpace(pieces[1])
		}
	}
	return socket
}

func isAlreadyConnected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already connected") ||
		strings.Contains(msg, "already have a connection") ||
		strings.Contains(msg, "already connected to peer")
}

func peerConnectErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if isTimeoutError(err) {
		return "Peer connection timed out"
	}
	msg := lndRPCErrorMessage(err)
	if msg == "" || msg == "LND error" {
		msg = lndStatusMessage(err)
	}
	if msg == "" {
		msg = "Peer connection failed"
	}
	return msg
}

func watchtowerRPCErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	raw := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(raw, "unknown service wtclientrpc.watchtowerclient") ||
		strings.Contains(raw, "unknown service") && strings.Contains(raw, "wtclientrpc.watchtowerclient") ||
		strings.Contains(raw, "unimplemented") {
		return "Watchtower client RPC unavailable on this LND."
	}

	msg := lndDetailedErrorMessage(err)
	if strings.TrimSpace(msg) == "" {
		return "Watchtower operation failed"
	}
	return msg
}

func (s *Server) handleLNOpenChannel(w http.ResponseWriter, r *http.Request) {
	if s.rejectLNDMaintenanceAction(w, r, "channel open") {
		return
	}
	var req struct {
		PeerAddress     string   `json:"peer_address"`
		Pubkey          string   `json:"pubkey"`
		LocalFundingSat int64    `json:"local_funding_sat"`
		PushSat         int64    `json:"push_sat"`
		CloseAddress    string   `json:"close_address"`
		Private         bool     `json:"private"`
		SatPerVbyte     int64    `json:"sat_per_vbyte"`
		Outpoints       []string `json:"outpoints"`
		ConfirmPassword string   `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	peerAddress := strings.TrimSpace(req.PeerAddress)
	if peerAddress == "" {
		peerAddress = strings.TrimSpace(req.Pubkey)
	}
	if peerAddress == "" {
		writeError(w, http.StatusBadRequest, "peer_address required")
		return
	}
	if req.LocalFundingSat <= 0 {
		writeError(w, http.StatusBadRequest, "local_funding_sat must be positive")
		return
	}
	if req.PushSat < 0 {
		writeError(w, http.StatusBadRequest, "push_sat must be zero or positive")
		return
	}
	if req.PushSat > req.LocalFundingSat {
		writeError(w, http.StatusBadRequest, "push_sat cannot exceed local_funding_sat")
		return
	}
	if req.SatPerVbyte < 0 {
		writeError(w, http.StatusBadRequest, "sat_per_vbyte must be zero or positive")
		return
	}

	pubkey, host, err := parsePeerAddress(peerAddress)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.Contains(host, ":") {
		writeError(w, http.StatusBadRequest, "peer host must include host:port")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	connectCtx, connectCancel := context.WithTimeout(r.Context(), lndConnectTimeout)
	if err := s.lnd.ConnectPeerWithTimeout(connectCtx, pubkey, host, false, uint64(lndConnectTimeout/time.Second)); err != nil && !isAlreadyConnected(err) {
		connectCancel()
		writeError(w, http.StatusInternalServerError, lndRPCErrorMessage(err))
		return
	}
	connectCancel()

	openCtx, openCancel := context.WithTimeout(r.Context(), lndOpenChannelTimeout)
	defer openCancel()

	var (
		channelPoint string
		openErr      error
	)
	if len(req.Outpoints) > 0 {
		channelPoint, openErr = s.lnd.OpenChannelWithOutpoints(openCtx, lndclient.OpenChannelParams{
			PubkeyHex:       pubkey,
			LocalFundingSat: req.LocalFundingSat,
			PushSat:         req.PushSat,
			CloseAddress:    req.CloseAddress,
			Private:         req.Private,
			SatPerVbyte:     req.SatPerVbyte,
			Outpoints:       req.Outpoints,
		})
	} else {
		channelPoint, openErr = s.lnd.OpenChannelWithPush(openCtx, pubkey, req.LocalFundingSat, req.PushSat, req.CloseAddress, req.Private, req.SatPerVbyte)
	}
	if openErr != nil {
		s.recordAuditEventAsync(r, "channel.open.failed", pubkey, map[string]any{
			"local_funding_sat": req.LocalFundingSat,
			"private":           req.Private,
			"sat_per_vbyte":     req.SatPerVbyte,
		})
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(openErr))
		return
	}
	s.recordAuditEventAsync(r, "channel.open.submitted", channelPoint, map[string]any{
		"peer_pubkey":       pubkey,
		"local_funding_sat": req.LocalFundingSat,
		"private":           req.Private,
		"sat_per_vbyte":     req.SatPerVbyte,
	})

	writeJSON(w, http.StatusOK, map[string]string{"channel_point": channelPoint})
}

func (s *Server) handleLNOpenChannelPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocalFundingSat int64 `json:"local_funding_sat"`
		PushSat         int64 `json:"push_sat"`
		SatPerVbyte     int64 `json:"sat_per_vbyte"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.PushSat < 0 {
		writeError(w, http.StatusBadRequest, "push_sat must be zero or positive")
		return
	}
	if req.SatPerVbyte < 0 {
		writeError(w, http.StatusBadRequest, "sat_per_vbyte must be zero or positive")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	preview, err := s.lnd.PreviewOpenChannel(ctx, req.LocalFundingSat, req.PushSat, req.SatPerVbyte)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleLNBatchOpenChannel(w http.ResponseWriter, r *http.Request) {
	if s.rejectLNDMaintenanceAction(w, r, "batch channel open") {
		return
	}
	var req struct {
		Channels []struct {
			PeerAddress     string `json:"peer_address"`
			Pubkey          string `json:"pubkey"`
			Host            string `json:"host"`
			LocalFundingSat int64  `json:"local_funding_sat"`
			CloseAddress    string `json:"close_address"`
			Private         bool   `json:"private"`
		} `json:"channels"`
		SatPerVbyte     int64  `json:"sat_per_vbyte"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.Channels) == 0 {
		writeError(w, http.StatusBadRequest, "channels required")
		return
	}
	if len(req.Channels) > batchOpenMaxChannels {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many channels (max %d)", batchOpenMaxChannels))
		return
	}
	if req.SatPerVbyte < 0 {
		writeError(w, http.StatusBadRequest, "sat_per_vbyte must be zero or positive")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	peersCtx, peersCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	peers, err := s.lnd.ListPeers(peersCtx)
	peersCancel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}
	connected := make(map[string]bool, len(peers))
	for _, peer := range peers {
		pubkey := strings.ToLower(strings.TrimSpace(peer.PubKey))
		if pubkey != "" {
			connected[pubkey] = true
		}
	}

	dedupe := make(map[string]bool, len(req.Channels))
	batch := make([]lndclient.BatchOpenChannelParams, 0, len(req.Channels))
	for idx, item := range req.Channels {
		localFunding := item.LocalFundingSat
		if localFunding <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("channels[%d].local_funding_sat must be positive", idx))
			return
		}

		peerAddress := strings.TrimSpace(item.PeerAddress)
		pubkey := strings.TrimSpace(item.Pubkey)
		host := strings.TrimSpace(item.Host)
		if peerAddress == "" && pubkey != "" && host != "" {
			peerAddress = pubkey + "@" + host
		} else if peerAddress == "" && pubkey != "" {
			peerAddress = pubkey
		}
		if peerAddress == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("channels[%d].peer_address required", idx))
			return
		}

		if strings.Contains(peerAddress, "@") {
			parsedPubkey, parsedHost, err := parsePeerAddress(peerAddress)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("channels[%d]: %v", idx, err))
				return
			}
			pubkey = parsedPubkey
			host = parsedHost
		} else if pubkey == "" {
			pubkey = peerAddress
		}

		if pubkey == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("channels[%d].pubkey required", idx))
			return
		}
		if host != "" && !strings.Contains(host, ":") {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("channels[%d].host must include host:port", idx))
			return
		}

		pubkeyKey := strings.ToLower(pubkey)
		if dedupe[pubkeyKey] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("duplicate pubkey in batch: %s", pubkey))
			return
		}
		dedupe[pubkeyKey] = true

		if !connected[pubkeyKey] {
			if host == "" {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("peer %s is not connected; use pubkey@host:port", pubkey))
				return
			}
			connectCtx, connectCancel := context.WithTimeout(r.Context(), lndConnectTimeout)
			err := s.lnd.ConnectPeerWithTimeout(connectCtx, pubkey, host, false, uint64(lndConnectTimeout/time.Second))
			connectCancel()
			if err != nil && !isAlreadyConnected(err) {
				writeError(w, http.StatusInternalServerError, peerConnectErrorMessage(err))
				return
			}
			connected[pubkeyKey] = true
		}

		batch = append(batch, lndclient.BatchOpenChannelParams{
			PubkeyHex:       pubkey,
			LocalFundingSat: localFunding,
			Private:         item.Private,
			CloseAddress:    item.CloseAddress,
		})
	}

	openCtx, openCancel := context.WithTimeout(r.Context(), lndBatchOpenChannelTimeout)
	defer openCancel()

	results, err := s.lnd.BatchOpenChannel(openCtx, batch, req.SatPerVbyte)
	if err != nil {
		s.recordAuditEventAsync(r, "channel.open_batch.failed", "batch", map[string]any{
			"channel_count": len(batch),
			"sat_per_vbyte": req.SatPerVbyte,
		})
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}
	s.recordAuditEventAsync(r, "channel.open_batch.submitted", "batch", map[string]any{
		"channel_count": len(results),
		"sat_per_vbyte": req.SatPerVbyte,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"pending_channels": results,
		"count":            len(results),
	})
}

func (s *Server) handleLNBatchOpenChannelPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channels []struct {
			LocalFundingSat int64 `json:"local_funding_sat"`
		} `json:"channels"`
		SatPerVbyte int64 `json:"sat_per_vbyte"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.Channels) == 0 {
		writeError(w, http.StatusBadRequest, "channels required")
		return
	}
	if req.SatPerVbyte < 0 {
		writeError(w, http.StatusBadRequest, "sat_per_vbyte must be zero or positive")
		return
	}

	batch := make([]lndclient.BatchOpenChannelParams, 0, len(req.Channels))
	for idx, item := range req.Channels {
		if item.LocalFundingSat <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("channels[%d].local_funding_sat must be positive", idx))
			return
		}
		batch = append(batch, lndclient.BatchOpenChannelParams{
			LocalFundingSat: item.LocalFundingSat,
		})
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	preview, err := s.lnd.PreviewBatchOpenChannel(ctx, batch, req.SatPerVbyte)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleLNPendingOpenBumpFee(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelPoint    string `json:"channel_point"`
		Preset          string `json:"preset"`
		SatPerVbyte     int64  `json:"sat_per_vbyte"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	channelPoint := strings.TrimSpace(req.ChannelPoint)
	if channelPoint == "" {
		writeError(w, http.StatusBadRequest, "channel_point required")
		return
	}
	if req.SatPerVbyte < 0 {
		writeError(w, http.StatusBadRequest, "sat_per_vbyte must be zero or positive")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	pending, err := s.lnd.ListPendingChannels(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	var item *lndclient.PendingChannelInfo
	for idx := range pending {
		if !strings.EqualFold(strings.TrimSpace(pending[idx].ChannelPoint), channelPoint) {
			continue
		}
		item = &pending[idx]
		break
	}
	if item == nil || !strings.EqualFold(strings.TrimSpace(item.Status), "opening") {
		writeError(w, http.StatusNotFound, "pending open channel not found")
		return
	}
	if !item.FundingBumpEligible || strings.TrimSpace(item.FundingBumpOutpoint) == "" {
		writeError(w, http.StatusBadRequest, "pending open channel is not eligible for funding bump")
		return
	}

	fees := closeManagerLoadBumpFeeRecommendation(ctx)
	plan, err := resolvePendingOpenBumpPlan(*item, req.Preset, req.SatPerVbyte, fees)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.lnd.BumpFee(ctx, lndclient.BumpFeeParams{
		Outpoint:    item.FundingBumpOutpoint,
		SatPerVbyte: plan.SatPerVbyte,
		Immediate:   plan.Immediate,
	}); err != nil {
		s.recordAuditEventAsync(r, "channel.pending_open_bump.failed", channelPoint, map[string]any{
			"preset":        plan.Preset,
			"sat_per_vbyte": plan.SatPerVbyte,
		})
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}
	s.recordAuditEventAsync(r, "channel.pending_open_bump.submitted", channelPoint, map[string]any{
		"preset":            plan.Preset,
		"sat_per_vbyte":     plan.SatPerVbyte,
		"estimated_fee_sat": plan.EstimatedFeeSat,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                   true,
		"preset":               plan.Preset,
		"sat_per_vbyte":        plan.SatPerVbyte,
		"immediate":            plan.Immediate,
		"outpoint":             item.FundingBumpOutpoint,
		"candidate_amount_sat": item.FundingBumpAmountSat,
		"estimated_fee_sat":    plan.EstimatedFeeSat,
		"reference_vbytes":     plan.ReferenceVbytes,
	})
}

type pendingOpenBumpPlan struct {
	Preset          string
	SatPerVbyte     int64
	Immediate       bool
	EstimatedFeeSat int64
	ReferenceVbytes int64
}

func resolvePendingOpenBumpPlan(item lndclient.PendingChannelInfo, preset string, explicitSatPerVbyte int64, fees *mempoolFeeRecommendation) (pendingOpenBumpPlan, error) {
	current := item.FundingFeeRateSatVbyte
	normalizedPreset := strings.ToLower(strings.TrimSpace(preset))
	if explicitSatPerVbyte > 0 {
		if explicitSatPerVbyte <= current {
			explicitSatPerVbyte = current + 1
		}
		return pendingOpenBumpPlan{
			Preset:          "custom",
			SatPerVbyte:     explicitSatPerVbyte,
			Immediate:       explicitSatPerVbyte >= pendingOpenBumpUrgentFeeTarget(fees),
			EstimatedFeeSat: explicitSatPerVbyte * pendingOpenBumpReferenceVbytes,
			ReferenceVbytes: pendingOpenBumpReferenceVbytes,
		}, nil
	}

	if normalizedPreset == "" {
		normalizedPreset = "normal"
	}
	economic := pendingOpenBumpEconomicFeeTarget(fees)
	normal := pendingOpenBumpNormalFeeTarget(fees)
	urgent := pendingOpenBumpUrgentFeeTarget(fees)

	switch normalizedPreset {
	case "economic":
		satPerVbyte := closeManagerMaxInt64(current+1, economic)
		return pendingOpenBumpPlan{
			Preset:          normalizedPreset,
			SatPerVbyte:     satPerVbyte,
			Immediate:       false,
			EstimatedFeeSat: satPerVbyte * pendingOpenBumpReferenceVbytes,
			ReferenceVbytes: pendingOpenBumpReferenceVbytes,
		}, nil
	case "normal":
		satPerVbyte := closeManagerMaxInt64(current+2, normal)
		return pendingOpenBumpPlan{
			Preset:          normalizedPreset,
			SatPerVbyte:     satPerVbyte,
			Immediate:       false,
			EstimatedFeeSat: satPerVbyte * pendingOpenBumpReferenceVbytes,
			ReferenceVbytes: pendingOpenBumpReferenceVbytes,
		}, nil
	case "urgent":
		satPerVbyte := closeManagerMaxInt64(current+5, urgent)
		return pendingOpenBumpPlan{
			Preset:          normalizedPreset,
			SatPerVbyte:     satPerVbyte,
			Immediate:       true,
			EstimatedFeeSat: satPerVbyte * pendingOpenBumpReferenceVbytes,
			ReferenceVbytes: pendingOpenBumpReferenceVbytes,
		}, nil
	default:
		return pendingOpenBumpPlan{}, fmt.Errorf("unsupported bump preset: %s", preset)
	}
}

func pendingOpenBumpEconomicFeeTarget(fees *mempoolFeeRecommendation) int64 {
	if fees != nil {
		switch {
		case fees.EconomyFee > 0:
			return int64(fees.EconomyFee)
		case fees.MinimumFee > 0:
			return int64(fees.MinimumFee)
		}
	}
	return 1
}

func pendingOpenBumpNormalFeeTarget(fees *mempoolFeeRecommendation) int64 {
	if fees != nil {
		switch {
		case fees.HourFee > 0:
			return int64(fees.HourFee)
		case fees.HalfHourFee > 0:
			return int64(fees.HalfHourFee)
		}
	}
	return pendingOpenBumpEconomicFeeTarget(fees) + 1
}

func pendingOpenBumpUrgentFeeTarget(fees *mempoolFeeRecommendation) int64 {
	if fees != nil && fees.FastestFee > 0 {
		return int64(fees.FastestFee)
	}
	return pendingOpenBumpNormalFeeTarget(fees) + 3
}

func (s *Server) handleMempoolFees(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	url := "https://mempool.space/api/v1/fees/recommended"
	var fees mempoolFeeRecommendation
	if err := fetchMempoolJSON(ctx, url, &fees); err != nil {
		writeError(w, http.StatusInternalServerError, "mempool fee fetch failed")
		return
	}
	writeJSON(w, http.StatusOK, fees)
}

func (s *Server) handleLNCloseChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelPoint    string `json:"channel_point"`
		Force           bool   `json:"force"`
		SatPerVbyte     int64  `json:"sat_per_vbyte"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "channel_point required")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if req.SatPerVbyte < 0 {
		writeError(w, http.StatusBadRequest, "sat_per_vbyte must be zero or positive")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	satPerVbyte := req.SatPerVbyte
	if req.Force {
		satPerVbyte = 0
	}

	closingTxid, err := s.lnd.CloseChannel(ctx, req.ChannelPoint, req.Force, satPerVbyte)
	if err != nil {
		s.recordAuditEventAsync(r, "channel.close.failed", strings.TrimSpace(req.ChannelPoint), map[string]any{
			"force":         req.Force,
			"sat_per_vbyte": satPerVbyte,
		})
		writeError(w, http.StatusInternalServerError, lndCloseErrorMessage(err))
		return
	}

	channelPoint := strings.TrimSpace(req.ChannelPoint)
	closingTxid = strings.TrimSpace(closingTxid)
	s.recordAuditEventAsync(r, "channel.close.submitted", channelPoint, map[string]any{
		"force":         req.Force,
		"sat_per_vbyte": satPerVbyte,
		"closing_txid":  closingTxid,
	})
	if s.notifier != nil && channelPoint != "" && closingTxid != "" {
		eventKey := "channel:closing:" + channelPoint
		evt := Notification{
			OccurredAt:   time.Now().UTC(),
			Type:         "channel",
			Action:       "closing",
			Direction:    "neutral",
			Status:       "PENDING",
			ChannelPoint: channelPoint,
			Txid:         closingTxid,
		}
		nctx, ncancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = s.notifier.upsertNotification(nctx, eventKey, evt)
		ncancel()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"closing_txid": closingTxid,
	})
}

func (s *Server) handleLNUpdateFees(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelPoint      string `json:"channel_point"`
		ApplyAll          bool   `json:"apply_all"`
		BaseFeeMsat       int64  `json:"base_fee_msat"`
		FeeRatePpm        int64  `json:"fee_rate_ppm"`
		TimeLockDelta     int64  `json:"time_lock_delta"`
		InboundEnabled    bool   `json:"inbound_enabled"`
		InboundBaseMsat   int64  `json:"inbound_base_msat"`
		InboundFeeRatePpm int64  `json:"inbound_fee_rate_ppm"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.BaseFeeMsat < 0 || req.FeeRatePpm < 0 || req.TimeLockDelta < 0 {
		writeError(w, http.StatusBadRequest, "fees must be zero or positive")
		return
	}
	if req.ApplyAll && strings.TrimSpace(req.ChannelPoint) != "" {
		writeError(w, http.StatusBadRequest, "use apply_all or channel_point, not both")
		return
	}
	if !req.ApplyAll && strings.TrimSpace(req.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "channel_point required unless apply_all=true")
		return
	}
	if req.BaseFeeMsat == 0 && req.FeeRatePpm == 0 && req.TimeLockDelta == 0 && !req.InboundEnabled {
		writeError(w, http.StatusBadRequest, "at least one fee field is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	if err := s.lnd.UpdateChannelFees(ctx, req.ChannelPoint, req.ApplyAll, req.BaseFeeMsat, req.FeeRatePpm, req.TimeLockDelta, req.InboundEnabled, req.InboundBaseMsat, req.InboundFeeRatePpm); err != nil {
		if isTimeoutError(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"warning": "Update sent. LND is syncing; policy may already be updated.",
			})
			return
		}
		writeError(w, http.StatusInternalServerError, lndRPCErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLNUpdateChanStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelPoint string `json:"channel_point"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "channel_point required")
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	if err := s.lnd.UpdateChanStatus(ctx, req.ChannelPoint, *req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNChannelFees(w http.ResponseWriter, r *http.Request) {
	channelPoint := strings.TrimSpace(r.URL.Query().Get("channel_point"))
	if channelPoint == "" {
		writeError(w, http.StatusBadRequest, "channel_point required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	policy, err := s.lnd.GetChannelPolicy(ctx, channelPoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channel_point":        policy.ChannelPoint,
		"base_fee_msat":        policy.BaseFeeMsat,
		"fee_rate_ppm":         policy.FeeRatePpm,
		"time_lock_delta":      policy.TimeLockDelta,
		"min_htlc_msat":        policy.MinHtlcMsat,
		"max_htlc_msat":        policy.MaxHtlcMsat,
		"inbound_base_msat":    policy.InboundBaseMsat,
		"inbound_fee_rate_ppm": policy.InboundFeeRatePpm,
	})
}

func (s *Server) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	if s.chat == nil {
		writeError(w, http.StatusServiceUnavailable, "chat unavailable")
		return
	}
	peerPubkey := strings.TrimSpace(r.URL.Query().Get("peer_pubkey"))
	if peerPubkey == "" {
		writeError(w, http.StatusBadRequest, "peer_pubkey required")
		return
	}
	if !isValidPubkeyHex(peerPubkey) {
		writeError(w, http.StatusBadRequest, "invalid peer_pubkey")
		return
	}
	limit := chatMessageLimitDefault
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := s.chat.Messages(peerPubkey, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat messages")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleChatInbox(w http.ResponseWriter, r *http.Request) {
	if s.chat == nil {
		writeError(w, http.StatusServiceUnavailable, "chat unavailable")
		return
	}
	items, err := s.chat.Inbox()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat inbox")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleChatRead(w http.ResponseWriter, r *http.Request) {
	if s.chat == nil {
		writeError(w, http.StatusServiceUnavailable, "chat unavailable")
		return
	}
	var req struct {
		PeerPubkey string `json:"peer_pubkey"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	peerPubkey := strings.TrimSpace(req.PeerPubkey)
	if !isValidPubkeyHex(peerPubkey) {
		writeError(w, http.StatusBadRequest, "invalid peer_pubkey")
		return
	}
	readAt, err := s.chat.MarkRead(peerPubkey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist chat read state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"peer_pubkey":  peerPubkey,
		"last_read_at": readAt,
	})
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if s.chat == nil {
		writeError(w, http.StatusServiceUnavailable, "chat unavailable")
		return
	}
	var req struct {
		PeerPubkey string `json:"peer_pubkey"`
		Message    string `json:"message"`
		AmountSat  *int64 `json:"amount_sat,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	peerPubkey := strings.TrimSpace(req.PeerPubkey)
	if peerPubkey == "" {
		writeError(w, http.StatusBadRequest, "peer_pubkey required")
		return
	}
	if !isValidPubkeyHex(peerPubkey) {
		writeError(w, http.StatusBadRequest, "invalid peer_pubkey")
		return
	}
	message := strings.TrimSpace(req.Message)
	if err := validateChatMessage(message); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	amountSat, err := resolveChatAmountSat(req.AmountSat)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	msg, err := s.chat.SendMessage(ctx, peerPubkey, amountSat, message)
	if err != nil {
		detail := lndRPCErrorMessage(err)
		if isTimeoutError(err) {
			detail = lndStatusMessage(err)
		}
		if detail == "" || detail == "LND error" {
			detail = "Keysend failed"
		}
		writeErrorCode(w, http.StatusInternalServerError, chatSendErrorCode(detail), detail)
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func resolveChatAmountSat(amount *int64) (int64, error) {
	if amount == nil {
		return chatDefaultAmountSat, nil
	}
	if *amount <= 0 {
		return 0, errors.New("amount_sat must be positive")
	}
	return *amount, nil
}

func chatSendErrorCode(detail string) string {
	lower := strings.ToLower(strings.TrimSpace(detail))
	switch {
	case strings.Contains(lower, "incorrect payment details"):
		return "chat_keysend_incorrect_payment_details"
	case strings.Contains(lower, "no route") || strings.Contains(lower, "unable to find a path") || strings.Contains(lower, "temporary channel failure"):
		return "chat_keysend_route_failed"
	case strings.Contains(lower, "insufficient") || strings.Contains(lower, "balance"):
		return "chat_keysend_insufficient_balance"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return "chat_keysend_timeout"
	default:
		return "chat_keysend_failed"
	}
}

type lndUserConf struct {
	Alias                     string
	Color                     string
	MinChanSize               int64
	MaxChanSize               int64
	GraphSyncPeers            int
	NoDisconnectOnPongFailure bool
	TorActive                 bool
	TorSkipProxyForClearnet   bool
	TorStreamIsolation        bool
}

func parseLNDUserConf(raw string) lndUserConf {
	// These are LND's effective defaults when an option is absent. Keeping the
	// defaults here makes older installations readable without rewriting them.
	conf := lndUserConf{GraphSyncPeers: 3}
	section := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch section {
		case "application options":
			switch key {
			case "alias":
				conf.Alias = value
			case "color":
				conf.Color = value
			case "minchansize":
				conf.MinChanSize, _ = strconv.ParseInt(value, 10, 64)
			case "maxchansize":
				conf.MaxChanSize, _ = strconv.ParseInt(value, 10, 64)
			case "numgraphsyncpeers":
				conf.GraphSyncPeers, _ = strconv.Atoi(value)
			case "no-disconnect-on-pong-failure":
				conf.NoDisconnectOnPongFailure, _ = strconv.ParseBool(value)
			}
		case "tor":
			switch key {
			case "tor.active", "active":
				conf.TorActive, _ = strconv.ParseBool(value)
			case "tor.skip-proxy-for-clearnet-targets", "skip-proxy-for-clearnet-targets":
				conf.TorSkipProxyForClearnet, _ = strconv.ParseBool(value)
			case "tor.streamisolation", "streamisolation":
				conf.TorStreamIsolation, _ = strconv.ParseBool(value)
			}
		}
	}
	return conf
}

func (c lndUserConf) networkMode() string {
	if c.TorActive && !c.TorSkipProxyForClearnet && c.TorStreamIsolation {
		return "private"
	}
	if c.TorActive && c.TorSkipProxyForClearnet && !c.TorStreamIsolation {
		return "hybrid"
	}
	return "custom"
}

func validateLNDNetworkCombination(raw string) error {
	conf := parseLNDUserConf(raw)
	if conf.TorSkipProxyForClearnet && conf.TorStreamIsolation {
		return errors.New("Tor stream isolation must be disabled when clearnet targets skip the Tor proxy")
	}
	return nil
}

func buildLNDConfigUpdate(
	raw string,
	updateBasic bool,
	alias string,
	color string,
	minChanSize int64,
	maxChanSize int64,
	networkMode *string,
	graphSyncPeers *int,
	disconnectUnresponsivePeers *bool,
) (string, error) {
	// Always update the complete existing config in place. In particular, an
	// existing node may use a custom wallet-unlock-password-file path that must
	// never be replaced merely because an unrelated setting was saved.
	updated := raw
	if updateBasic {
		updated = updateLNDConfOptions(
			updated, alias, color, minChanSize, maxChanSize,
		)
	}
	updated = updateLNDNetworkOptions(
		updated, networkMode, graphSyncPeers,
		disconnectUnresponsivePeers,
	)
	if err := validateLNDNetworkCombination(updated); err != nil {
		return "", err
	}

	return updated, nil
}

func (s *Server) handleLNDConfigPost(w http.ResponseWriter, r *http.Request) {
	if s.rejectLNDMaintenanceAction(w, r, "LND config update") {
		return
	}
	var req struct {
		Alias                       *string `json:"alias"`
		Color                       *string `json:"color"`
		MinChannelSizeSat           *int64  `json:"min_channel_size_sat"`
		MaxChannelSizeSat           *int64  `json:"max_channel_size_sat"`
		NetworkMode                 *string `json:"network_mode"`
		GraphSyncPeers              *int    `json:"graph_sync_peers"`
		DisconnectUnresponsivePeers *bool   `json:"disconnect_unresponsive_peers"`
		ApplyNow                    bool    `json:"apply_now"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.MinChannelSizeSat != nil && *req.MinChannelSizeSat < 0 || req.MaxChannelSizeSat != nil && *req.MaxChannelSizeSat < 0 {
		writeError(w, http.StatusBadRequest, "channel sizes must be positive")
		return
	}
	if req.Alias != nil && len(*req.Alias) > 32 {
		writeError(w, http.StatusBadRequest, "alias must be at most 32 bytes")
		return
	}
	if req.Color != nil && strings.TrimSpace(*req.Color) != "" && !isHexColor(*req.Color) {
		writeError(w, http.StatusBadRequest, "color must be hex (#RRGGBB)")
		return
	}
	if req.NetworkMode != nil && *req.NetworkMode != "private" && *req.NetworkMode != "hybrid" {
		writeError(w, http.StatusBadRequest, "network mode must be private or hybrid")
		return
	}
	if req.GraphSyncPeers != nil && (*req.GraphSyncPeers < 1 || *req.GraphSyncPeers > 100) {
		writeError(w, http.StatusBadRequest, "graph sync peers must be between 1 and 100")
		return
	}

	raw, err := os.ReadFile(lndConfPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read lnd.conf")
		return
	}
	current := parseLNDUserConf(string(raw))
	alias := current.Alias
	color := current.Color
	minChanSize := current.MinChanSize
	maxChanSize := current.MaxChanSize
	if req.Alias != nil {
		alias = *req.Alias
	}
	if req.Color != nil {
		color = *req.Color
	}
	if req.MinChannelSizeSat != nil {
		minChanSize = *req.MinChannelSizeSat
	}
	if req.MaxChannelSizeSat != nil {
		maxChanSize = *req.MaxChannelSizeSat
	}
	if minChanSize > 0 && maxChanSize > 0 && minChanSize >= maxChanSize {
		writeError(w, http.StatusBadRequest, "min channel must be lower than max")
		return
	}

	updateBasic := req.Alias != nil || req.Color != nil ||
		req.MinChannelSizeSat != nil || req.MaxChannelSizeSat != nil
	updated, err := buildLNDConfigUpdate(
		string(raw), updateBasic, alias, color, minChanSize, maxChanSize,
		req.NetworkMode, req.GraphSyncPeers,
		req.DisconnectUnresponsivePeers,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.WriteFile(lndConfPath, []byte(updated), 0660); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write lnd.conf")
		return
	}

	if req.ApplyNow {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := restartLNDService(ctx); err != nil {
			_ = os.WriteFile(lndConfPath, raw, 0660)
			writeError(w, http.StatusInternalServerError, "lnd restart failed, rollback applied")
			return
		}
		s.markLNDRestart()
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNDConfigRaw(w http.ResponseWriter, r *http.Request) {
	if s.rejectLNDMaintenanceAction(w, r, "raw LND config update") {
		return
	}
	var req struct {
		RawUserConf string `json:"raw_user_conf"`
		ApplyNow    bool   `json:"apply_now"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	prev, _ := os.ReadFile(lndConfPath)
	// The advanced editor is authoritative. Auto-unlock directives are only
	// created by the wallet setup/unlock flow after LND validates the password.
	updated := req.RawUserConf
	if err := validateLNDNetworkCombination(updated); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.WriteFile(lndConfPath, []byte(updated), 0660); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write lnd.conf")
		return
	}

	if req.ApplyNow {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := restartLNDService(ctx); err != nil {
			_ = os.WriteFile(lndConfPath, prev, 0660)
			writeError(w, http.StatusInternalServerError, "lnd restart failed, rollback applied")
			return
		}
		s.markLNDRestart()
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func restartLNDService(ctx context.Context) error {
	service := "lnd"
	if !system.SystemctlIsActive(ctx, service) && system.SystemctlIsActive(ctx, "lnd@default") {
		service = "lnd@default"
	}
	return system.SystemctlRestartNoBlock(ctx, service)
}

type lndOptionUpdate struct {
	value  string
	remove bool
	seen   bool
}

type lndSectionOption struct {
	key   string
	value string
}

func updateLNDNetworkOptions(raw string, mode *string, graphSyncPeers *int, disconnectUnresponsivePeers *bool) string {
	updated := raw
	applicationOptions := make([]lndSectionOption, 0, 2)
	if graphSyncPeers != nil {
		applicationOptions = append(applicationOptions, lndSectionOption{
			key:   "numgraphsyncpeers",
			value: strconv.Itoa(*graphSyncPeers),
		})
	}
	if disconnectUnresponsivePeers != nil {
		applicationOptions = append(applicationOptions, lndSectionOption{
			key:   "no-disconnect-on-pong-failure",
			value: strconv.FormatBool(!*disconnectUnresponsivePeers),
		})
	}
	if len(applicationOptions) > 0 {
		updated = updateLNDSectionOptions(updated, "Application Options", applicationOptions)
	}

	if mode != nil {
		torOptions := []lndSectionOption{{key: "tor.active", value: "true"}}
		switch *mode {
		case "private":
			torOptions = append(torOptions,
				lndSectionOption{key: "tor.skip-proxy-for-clearnet-targets", value: "false"},
				lndSectionOption{key: "tor.streamisolation", value: "true"},
			)
		case "hybrid":
			torOptions = append(torOptions,
				lndSectionOption{key: "tor.skip-proxy-for-clearnet-targets", value: "true"},
				lndSectionOption{key: "tor.streamisolation", value: "false"},
			)
		}
		updated = updateLNDSectionOptions(updated, "tor", torOptions)
	}

	return updated
}

func updateLNDSectionOptions(raw string, sectionName string, options []lndSectionOption) string {
	if len(options) == 0 {
		return raw
	}

	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	start := -1
	end := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
			continue
		}
		name := strings.TrimSpace(strings.Trim(trimmed, "[]"))
		if strings.EqualFold(name, sectionName) {
			start = i
			continue
		}
		if start != -1 && i > start {
			end = i
			break
		}
	}

	if start == -1 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+sectionName+"]")
		start = len(lines) - 1
		end = len(lines)
	}

	seen := make(map[string]bool, len(options))
	values := make(map[string]string, len(options))
	for _, option := range options {
		values[strings.ToLower(option.key)] = option.value
	}
	for i := start + 1; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value, ok := values[key]
		if !ok {
			continue
		}
		lines[i] = fmt.Sprintf("%s=%s", strings.TrimSpace(parts[0]), value)
		seen[key] = true
	}

	extra := make([]string, 0, len(options))
	for _, option := range options {
		if seen[strings.ToLower(option.key)] {
			continue
		}
		extra = append(extra, fmt.Sprintf("%s=%s", option.key, option.value))
	}
	if len(extra) > 0 {
		lines = append(lines[:end], append(extra, lines[end:]...)...)
	}

	return strings.Join(lines, "\n")
}

func updateLNDConfOptions(raw string, alias string, color string, minChanSize int64, maxChanSize int64) string {
	updates := map[string]*lndOptionUpdate{
		"alias":       {value: alias, remove: strings.TrimSpace(alias) == ""},
		"color":       {value: color, remove: strings.TrimSpace(color) == ""},
		"minchansize": {value: strconv.FormatInt(minChanSize, 10), remove: minChanSize <= 0},
		"maxchansize": {value: strconv.FormatInt(maxChanSize, 10), remove: maxChanSize <= 0},
	}

	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	start := -1
	end := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if strings.EqualFold(trimmed, "[Application Options]") {
				start = i
				continue
			}
			if start != -1 && i > start {
				end = i
				break
			}
		}
	}

	if start == -1 {
		lines = append(lines, "[Application Options]")
		start = len(lines) - 1
		end = len(lines)
	}

	for i := start + 1; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		upd, ok := updates[key]
		if !ok {
			continue
		}
		upd.seen = true
		if upd.remove {
			lines[i] = ""
			continue
		}
		lines[i] = fmt.Sprintf("%s=%s", key, upd.value)
	}

	extra := []string{}
	for key, upd := range updates {
		if upd.seen || upd.remove {
			continue
		}
		extra = append(extra, fmt.Sprintf("%s=%s", key, upd.value))
	}
	if len(extra) > 0 {
		lines = append(lines[:end], append(extra, lines[end:]...)...)
	}

	return strings.Join(lines, "\n")
}

func isHexColor(value string) bool {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != 7 || !strings.HasPrefix(trimmed, "#") {
		return false
	}
	for _, r := range trimmed[1:] {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func (s *Server) markLNDRestart() {
	if s.lnd != nil {
		s.lnd.InvalidateStatusCache()
	}
	s.lndRestartMu.Lock()
	s.lastLNDRestart = time.Now()
	s.lndRestartMu.Unlock()
}

func (s *Server) lndWarmupActive() bool {
	s.lndRestartMu.RLock()
	last := s.lastLNDRestart
	s.lndRestartMu.RUnlock()
	if last.IsZero() {
		return false
	}
	return time.Since(last) <= lndWarmupPeriod
}

func (s *Server) handleOnchainUtxos(w http.ResponseWriter, r *http.Request) {
	minConfs := int32(0)
	maxConfs := int32(0)
	maxConfSet := false
	if raw := strings.TrimSpace(r.URL.Query().Get("include_unconfirmed")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil && !parsed {
			minConfs = 1
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("min_conf")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			minConfs = int32(parsed)
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("max_conf")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			maxConfs = int32(parsed)
			maxConfSet = true
		}
	}
	if maxConfs > 0 && maxConfs < minConfs {
		maxConfs = minConfs
	}
	if !maxConfSet {
		maxConfs = int32(1 << 30)
	}

	limit := 500
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	items, err := s.lnd.ListOnchainUtxos(ctx, minConfs, maxConfs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
		return
	}

	enriched := s.enrichOnchainUtxos(ctx, items)

	if limit > 0 && len(enriched) > limit {
		enriched = enriched[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": enriched})
}

func (s *Server) handleOnchainTransactions(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	minConfs := int32(0)
	maxConfs := int32(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("include_unconfirmed")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil && !parsed {
			minConfs = 1
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("min_conf")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			minConfs = int32(parsed)
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("max_conf")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			maxConfs = int32(parsed)
		}
	}
	if maxConfs > 0 && maxConfs < minConfs {
		maxConfs = minConfs
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	items, err := s.lnd.ListOnchainTransactions(ctx, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
		return
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.After(items[j].Timestamp)
	})

	if minConfs > 0 || maxConfs > 0 {
		filtered := items[:0]
		for _, item := range items {
			confs := item.Confirmations
			if minConfs > 0 && confs < minConfs {
				continue
			}
			if maxConfs > 0 && confs > maxConfs {
				continue
			}
			filtered = append(filtered, item)
		}
		items = filtered
	}

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleWalletSummary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	balances, err := s.lnd.GetBalances(ctx)
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err != nil {
		if isTimeoutError(err) && s.lndWarmupActive() {
			writeJSON(w, http.StatusOK, map[string]any{
				"balances": map[string]int64{
					"onchain_sat":                                     0,
					"lightning_sat":                                   0,
					"onchain_confirmed_sat":                           0,
					"onchain_unconfirmed_sat":                         0,
					"lightning_local_sat":                             0,
					"lightning_unsettled_local_sat":                   0,
					"lightning_remote_sat":                            0,
					"lightning_unsettled_remote_sat":                  0,
					"lightning_pending_open_local_sat":                0,
					"lightning_pending_open_remote_sat":               0,
					"lightning_closing_pending_sat":                   0,
					"lightning_closing_pending_count":                 0,
					"lightning_coop_closing_sat":                      0,
					"lightning_coop_closing_count":                    0,
					"lightning_force_closing_sat":                     0,
					"lightning_force_closing_count":                   0,
					"lightning_force_closing_min_blocks_til_maturity": 0,
					"lightning_force_closing_max_blocks_til_maturity": 0,
					"lightning_waiting_close_sat":                     0,
					"lightning_waiting_close_count":                   0,
				},
				"activity":   []any{},
				"warning":    "LND warming up after restart",
				"updated_at": updatedAt,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
		return
	}

	resp := map[string]any{
		"balances": map[string]int64{
			"onchain_sat":                                     balances.OnchainSat,
			"lightning_sat":                                   balances.LightningSat,
			"onchain_confirmed_sat":                           balances.OnchainConfirmedSat,
			"onchain_unconfirmed_sat":                         balances.OnchainUnconfirmedSat,
			"lightning_local_sat":                             balances.LightningLocalSat,
			"lightning_unsettled_local_sat":                   balances.LightningUnsettledLocalSat,
			"lightning_remote_sat":                            balances.LightningRemoteSat,
			"lightning_unsettled_remote_sat":                  balances.LightningUnsettledRemoteSat,
			"lightning_pending_open_local_sat":                balances.LightningPendingOpenLocalSat,
			"lightning_pending_open_remote_sat":               balances.LightningPendingOpenRemoteSat,
			"lightning_closing_pending_sat":                   balances.LightningClosingPendingSat,
			"lightning_closing_pending_count":                 balances.LightningClosingPendingCount,
			"lightning_coop_closing_sat":                      balances.LightningCoopClosingSat,
			"lightning_coop_closing_count":                    balances.LightningCoopClosingCount,
			"lightning_force_closing_sat":                     balances.LightningForceClosingSat,
			"lightning_force_closing_count":                   balances.LightningForceClosingCount,
			"lightning_force_closing_min_blocks_til_maturity": balances.LightningForceClosingMinBlocksTilMaturity,
			"lightning_force_closing_max_blocks_til_maturity": balances.LightningForceClosingMaxBlocksTilMaturity,
			"lightning_waiting_close_sat":                     balances.LightningWaitingCloseSat,
			"lightning_waiting_close_count":                   balances.LightningWaitingCloseCount,
		},
		"activity":   []any{},
		"updated_at": updatedAt,
	}
	if len(balances.Warnings) > 0 {
		resp["warning"] = strings.Join(balances.Warnings, " ")
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWalletActivity(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	now := time.Now().UTC()
	rangeKey := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	start := now.Add(-7 * 24 * time.Hour)
	switch rangeKey {
	case "", "7d":
		rangeKey = "7d"
	case "1m":
		start = now.AddDate(0, -1, 0)
	case "1a":
		start = now.AddDate(-1, 0, 0)
	default:
		writeError(w, http.StatusBadRequest, "invalid range")
		return
	}

	limit := walletActivityFetchLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if parsed < limit {
			limit = parsed
		}
	}

	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		offset = parsed
	}

	if offset >= walletActivityFetchLimit {
		writeJSON(w, http.StatusOK, map[string]any{
			"range":       rangeKey,
			"offset":      offset,
			"limit":       limit,
			"has_more":    false,
			"next_offset": offset,
			"items":       []any{},
		})
		return
	}

	fetchLimit := offset + limit + 1
	if fetchLimit > walletActivityFetchLimit {
		fetchLimit = walletActivityFetchLimit
	}

	var (
		items []lndclient.RecentActivity
		err   error
	)
	if s.db != nil {
		lightningItems, lightningErr := s.walletLightningActivity(ctx, start, now, fetchLimit)
		if lightningErr != nil {
			if s.logger != nil {
				s.logger.Printf("wallet activity: notifications query failed, falling back to lnd: %v", lightningErr)
			}
		} else {
			onchainItems, onchainErr := s.lnd.ListOnchainRange(ctx, start, now, fetchLimit)
			if onchainErr != nil {
				writeError(w, http.StatusInternalServerError, lndStatusMessage(onchainErr))
				return
			}
			items = append(lightningItems, onchainItems...)
			sort.Slice(items, func(i, j int) bool {
				return items[i].Timestamp.After(items[j].Timestamp)
			})
			if len(items) > fetchLimit {
				items = items[:fetchLimit]
			}
		}
	}
	if items == nil {
		items, err = s.lnd.ListActivityRange(ctx, start, now, fetchLimit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
			return
		}
	}

	if offset > len(items) {
		offset = len(items)
	}
	endIndex := offset + limit
	hasMore := len(items) > endIndex
	if endIndex > len(items) {
		endIndex = len(items)
	}
	pageItems := items[offset:endIndex]

	writeJSON(w, http.StatusOK, map[string]any{
		"range":       rangeKey,
		"offset":      offset,
		"limit":       limit,
		"has_more":    hasMore,
		"next_offset": endIndex,
		"items":       pageItems,
	})
}

func (s *Server) handleWalletAddress(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	addr, err := s.lnd.NewAddress(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"address": addr,
		"type":    "p2wpkh",
	})
}

func (s *Server) handleWalletInvoice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AmountSat                   int64  `json:"amount_sat"`
		Memo                        string `json:"memo"`
		ExpirySeconds               int64  `json:"expiry_seconds"`
		Blinded                     bool   `json:"blinded"`
		BlindedIncomingChannelPoint string `json:"blinded_incoming_channel_point"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AmountSat <= 0 {
		writeError(w, http.StatusBadRequest, "amount_sat must be positive")
		return
	}
	if req.ExpirySeconds < 0 {
		writeError(w, http.StatusBadRequest, "expiry_seconds must be zero or positive")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	createOpts := &lndclient.CreateInvoiceOptions{
		IsBlinded: req.Blinded,
	}
	if point := strings.ToLower(strings.TrimSpace(req.BlindedIncomingChannelPoint)); point != "" {
		createOpts.IsBlinded = true
		channels, err := s.lnd.ListChannels(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
			return
		}
		incomingChanID := uint64(0)
		for _, ch := range channels {
			if strings.ToLower(strings.TrimSpace(ch.ChannelPoint)) == point {
				incomingChanID = ch.ChannelID
				break
			}
		}
		if incomingChanID == 0 {
			writeError(w, http.StatusBadRequest, "selected blinded incoming channel not found")
			return
		}
		createOpts.IncomingChannelIDs = []uint64{incomingChanID}
	}
	if !createOpts.IsBlinded && len(createOpts.IncomingChannelIDs) == 0 {
		createOpts = nil
	}

	invoice, err := s.lnd.CreateInvoice(ctx, req.AmountSat, req.Memo, req.ExpirySeconds, createOpts)
	if err != nil {
		msg := lndDetailedErrorMessage(err)
		if msg == "" || msg == "LND error" {
			msg = "invoice failed"
		}
		writeError(w, http.StatusInternalServerError, msg)
		return
	}

	if invoice.PaymentHash != "" {
		s.recordWalletActivity(invoice.PaymentHash)
	}

	writeJSON(w, http.StatusOK, map[string]string{"payment_request": invoice.PaymentRequest})
}

func (s *Server) handleWalletDecode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentRequest string `json:"payment_request"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	paymentRequest := normalizePaymentRequest(req.PaymentRequest)
	if paymentRequest == "" {
		writeError(w, http.StatusBadRequest, "payment_request required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	decoded, err := s.lnd.DecodeInvoice(ctx, paymentRequest)
	if err != nil {
		msg := err.Error()
		lower := strings.ToLower(msg)
		if isTimeoutError(err) {
			msg = lndStatusMessage(err)
		} else if strings.Contains(lower, "invalid") || strings.Contains(lower, "payment request") {
			msg = "Invalid invoice"
		}
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"amount_sat":  decoded.AmountSat,
		"amount_msat": decoded.AmountMsat,
		"memo":        decoded.Memo,
		"destination": decoded.Destination,
		"expiry":      decoded.Expiry,
		"timestamp":   decoded.Timestamp,
		"is_blinded":  len(decoded.BlindedPaths) > 0,
		"blind_paths": len(decoded.BlindedPaths),
	})
}

func (s *Server) resolveWalletPaymentInput(ctx context.Context, rawPaymentRequest string, amountSat int64, comment string, channelPoint string, channelPoints []string) (string, []uint64, error) {
	paymentRequest := normalizePaymentRequest(rawPaymentRequest)
	if paymentRequest == "" {
		return "", nil, errors.New("payment_request required")
	}

	cleaned := strings.TrimSpace(paymentRequest)
	if strings.HasPrefix(strings.ToLower(cleaned), "lightning:") {
		cleaned = cleaned[len("lightning:"):]
	}
	if isLightningAddress(cleaned) {
		if amountSat <= 0 {
			return "", nil, errors.New("amount_sat must be positive for lightning address")
		}
		resolved, err := resolveLightningAddress(ctx, cleaned, amountSat, comment)
		if err != nil {
			return "", nil, fmt.Errorf("lightning address error: %v", err)
		}
		paymentRequest = resolved
	} else {
		paymentRequest = cleaned
	}

	selectedPoints := make([]string, 0, len(channelPoints)+1)
	if point := strings.ToLower(strings.TrimSpace(channelPoint)); point != "" {
		selectedPoints = append(selectedPoints, point)
	}
	for _, point := range channelPoints {
		trimmed := strings.ToLower(strings.TrimSpace(point))
		if trimmed != "" {
			selectedPoints = append(selectedPoints, trimmed)
		}
	}
	if len(selectedPoints) > 1 {
		seen := make(map[string]struct{}, len(selectedPoints))
		deduped := selectedPoints[:0]
		for _, point := range selectedPoints {
			if _, ok := seen[point]; ok {
				continue
			}
			seen[point] = struct{}{}
			deduped = append(deduped, point)
		}
		selectedPoints = deduped
	}

	outgoingChanIDs := make([]uint64, 0, len(selectedPoints))
	if len(selectedPoints) == 0 {
		return paymentRequest, outgoingChanIDs, nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	channels, err := s.lnd.ListChannels(resolveCtx)
	if err != nil {
		return "", nil, errors.New(lndDetailedErrorMessage(err))
	}
	channelIDsByPoint := make(map[string]uint64, len(channels))
	for _, ch := range channels {
		point := strings.ToLower(strings.TrimSpace(ch.ChannelPoint))
		if point == "" || ch.ChannelID == 0 {
			continue
		}
		channelIDsByPoint[point] = ch.ChannelID
	}
	for _, point := range selectedPoints {
		channelID := channelIDsByPoint[point]
		if channelID == 0 {
			return "", nil, errors.New("selected channel not found")
		}
		outgoingChanIDs = append(outgoingChanIDs, channelID)
	}
	return paymentRequest, outgoingChanIDs, nil
}

func (s *Server) handleWalletPayPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentRequest  string   `json:"payment_request"`
		ChannelPoint    string   `json:"channel_point"`
		ChannelPoints   []string `json:"channel_points"`
		AmountSat       int64    `json:"amount_sat"`
		Comment         string   `json:"comment"`
		MaxFeeSat       int64    `json:"max_fee_sat"`
		ConfirmPassword string   `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.MaxFeeSat < 0 {
		writeError(w, http.StatusBadRequest, "max_fee_sat must be zero or positive")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	paymentRequest, outgoingChanIDs, err := s.resolveWalletPaymentInput(r.Context(), req.PaymentRequest, req.AmountSat, req.Comment, req.ChannelPoint, req.ChannelPoints)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndWalletPaymentPreviewTimeout)
	defer cancel()

	preview, err := s.lnd.PreviewPayment(ctx, paymentRequest, outgoingChanIDs, req.MaxFeeSat, 5)
	if err != nil {
		msg := lndRPCErrorMessage(err)
		if isTimeoutError(err) {
			msg = lndStatusMessage(err)
		}
		if msg == "" || msg == "LND error" {
			msg = "Payment preview failed"
		}
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleWalletPay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentRequest  string   `json:"payment_request"`
		ChannelPoint    string   `json:"channel_point"`
		ChannelPoints   []string `json:"channel_points"`
		AmountSat       int64    `json:"amount_sat"`
		Comment         string   `json:"comment"`
		MaxFeeSat       int64    `json:"max_fee_sat"`
		ConfirmPassword string   `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.MaxFeeSat < 0 {
		writeError(w, http.StatusBadRequest, "max_fee_sat must be zero or positive")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	paymentRequest, outgoingChanIDs, err := s.resolveWalletPaymentInput(r.Context(), req.PaymentRequest, req.AmountSat, req.Comment, req.ChannelPoint, req.ChannelPoints)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndWalletPaymentTimeout)
	defer cancel()

	paymentHash := ""
	if decoded, err := s.lnd.DecodeInvoice(ctx, paymentRequest); err == nil {
		paymentHash = decoded.PaymentHash
	}

	if err := s.lnd.PayInvoice(ctx, paymentRequest, outgoingChanIDs, req.MaxFeeSat); err != nil {
		s.recordAuditEventAsync(r, "wallet.payment.failed", paymentHash, map[string]any{
			"mode":                   "standard",
			"amount_sat":             req.AmountSat,
			"max_fee_sat":            req.MaxFeeSat,
			"outgoing_channel_count": len(outgoingChanIDs),
		})
		if paymentHash != "" {
			s.recordWalletActivity(paymentHash)
		}
		msg := lndRPCErrorMessage(err)
		if isTimeoutError(err) {
			msg = "Payment timed out while LND was trying routes"
		}
		if msg == "" || msg == "LND error" {
			msg = "Payment failed"
		}
		writeError(w, http.StatusInternalServerError, msg)
		return
	}
	s.recordAuditEventAsync(r, "wallet.payment.sent", paymentHash, map[string]any{
		"mode":                   "standard",
		"amount_sat":             req.AmountSat,
		"max_fee_sat":            req.MaxFeeSat,
		"outgoing_channel_count": len(outgoingChanIDs),
	})

	if paymentHash != "" {
		s.recordWalletActivity(paymentHash)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleWalletPayValidatedRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentRequest string   `json:"payment_request"`
		ChannelPoint   string   `json:"channel_point"`
		ChannelPoints  []string `json:"channel_points"`
		RouteToken     string   `json:"route_token"`
		AmountSat      int64    `json:"amount_sat"`
		Comment        string   `json:"comment"`
		MaxFeeSat      int64    `json:"max_fee_sat"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.MaxFeeSat < 0 {
		writeError(w, http.StatusBadRequest, "max_fee_sat must be zero or positive")
		return
	}

	paymentRequest, outgoingChanIDs, err := s.resolveWalletPaymentInput(r.Context(), req.PaymentRequest, req.AmountSat, req.Comment, req.ChannelPoint, req.ChannelPoints)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndWalletPaymentTimeout)
	defer cancel()

	paymentHash := ""
	if decoded, err := s.lnd.DecodeInvoice(ctx, paymentRequest); err == nil {
		paymentHash = decoded.PaymentHash
	}

	if err := s.lnd.PayInvoiceWithValidatedRoute(ctx, paymentRequest, outgoingChanIDs, req.MaxFeeSat, 5, req.RouteToken); err != nil {
		s.recordAuditEventAsync(r, "wallet.payment.failed", paymentHash, map[string]any{
			"mode":                   "validated_route",
			"amount_sat":             req.AmountSat,
			"max_fee_sat":            req.MaxFeeSat,
			"outgoing_channel_count": len(outgoingChanIDs),
		})
		if paymentHash != "" {
			s.recordWalletActivity(paymentHash)
		}
		msg := lndRPCErrorMessage(err)
		if isTimeoutError(err) {
			msg = "Validated route payment timed out"
		}
		if msg == "" || msg == "LND error" {
			msg = "Validated route payment failed"
		}
		statusCode := http.StatusInternalServerError
		lowerMsg := strings.ToLower(msg)
		if strings.Contains(lowerMsg, "no validated route") ||
			strings.Contains(lowerMsg, "validated route token") ||
			strings.Contains(lowerMsg, "validated route destination") ||
			strings.Contains(lowerMsg, "validated route amount") ||
			strings.Contains(lowerMsg, "validated route exceeds") ||
			strings.Contains(lowerMsg, "validated route is empty") ||
			strings.Contains(lowerMsg, "validated route no longer") ||
			strings.Contains(lowerMsg, "amountless invoices") ||
			strings.Contains(lowerMsg, "blinded invoices") {
			statusCode = http.StatusBadRequest
		}
		writeError(w, statusCode, msg)
		return
	}
	s.recordAuditEventAsync(r, "wallet.payment.sent", paymentHash, map[string]any{
		"mode":                   "validated_route",
		"amount_sat":             req.AmountSat,
		"max_fee_sat":            req.MaxFeeSat,
		"outgoing_channel_count": len(outgoingChanIDs),
	})

	if paymentHash != "" {
		s.recordWalletActivity(paymentHash)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleWalletPayMPP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentRequest  string   `json:"payment_request"`
		ChannelPoint    string   `json:"channel_point"`
		ChannelPoints   []string `json:"channel_points"`
		AmountSat       int64    `json:"amount_sat"`
		Comment         string   `json:"comment"`
		MaxFeeSat       int64    `json:"max_fee_sat"`
		MaxParts        uint32   `json:"max_parts"`
		MaxShardSat     int64    `json:"max_shard_sat"`
		ConfirmPassword string   `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.MaxFeeSat < 0 {
		writeError(w, http.StatusBadRequest, "max_fee_sat must be zero or positive")
		return
	}
	if req.MaxShardSat < 0 {
		writeError(w, http.StatusBadRequest, "max_shard_sat must be zero or positive")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	paymentRequest, outgoingChanIDs, err := s.resolveWalletPaymentInput(r.Context(), req.PaymentRequest, req.AmountSat, req.Comment, req.ChannelPoint, req.ChannelPoints)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndWalletPaymentTimeout)
	defer cancel()

	paymentHash := ""
	if decoded, err := s.lnd.DecodeInvoice(ctx, paymentRequest); err == nil {
		paymentHash = decoded.PaymentHash
	}

	if err := s.lnd.PayInvoiceWithMPP(ctx, paymentRequest, outgoingChanIDs, req.MaxFeeSat, req.MaxParts, req.MaxShardSat); err != nil {
		s.recordAuditEventAsync(r, "wallet.payment.failed", paymentHash, map[string]any{
			"mode":                   "mpp",
			"amount_sat":             req.AmountSat,
			"max_fee_sat":            req.MaxFeeSat,
			"outgoing_channel_count": len(outgoingChanIDs),
			"max_parts":              req.MaxParts,
		})
		if paymentHash != "" {
			s.recordWalletActivity(paymentHash)
		}
		msg := lndRPCErrorMessage(err)
		if isTimeoutError(err) {
			msg = "MPP payment timed out while LND was trying shards"
		}
		if msg == "" || msg == "LND error" {
			msg = "MPP payment failed"
		}
		writeError(w, http.StatusInternalServerError, msg)
		return
	}
	s.recordAuditEventAsync(r, "wallet.payment.sent", paymentHash, map[string]any{
		"mode":                   "mpp",
		"amount_sat":             req.AmountSat,
		"max_fee_sat":            req.MaxFeeSat,
		"outgoing_channel_count": len(outgoingChanIDs),
		"max_parts":              req.MaxParts,
	})

	if paymentHash != "" {
		s.recordWalletActivity(paymentHash)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleWalletPaymentDetail(w http.ResponseWriter, r *http.Request) {
	paymentHash := strings.TrimSpace(chi.URLParam(r, "paymentHash"))
	if paymentHash == "" {
		writeError(w, http.StatusBadRequest, "payment_hash required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	details, err := s.lnd.PaymentDetails(ctx, paymentHash, 366*24*time.Hour)
	if err != nil {
		msg := lndRPCErrorMessage(err)
		if isTimeoutError(err) {
			msg = lndStatusMessage(err)
		}
		if msg == "" || msg == "LND error" {
			msg = err.Error()
		}
		statusCode := http.StatusInternalServerError
		if strings.Contains(strings.ToLower(msg), "not found") {
			statusCode = http.StatusNotFound
		}
		writeError(w, statusCode, msg)
		return
	}

	writeJSON(w, http.StatusOK, details)
}

func (s *Server) classifyOnchainDestination(ctx context.Context, address string) (string, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return "unknown", nil
	}

	isWalletAddress, err := s.lnd.IsWalletAddress(ctx, trimmed)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("wallet destination classification failed: %v", err)
		}
		return "unknown", err
	}
	if isWalletAddress {
		return "wallet_internal", nil
	}
	return "external", nil
}

func requiresWalletSendConfirmation(classification string) bool {
	switch strings.TrimSpace(classification) {
	case "wallet_internal":
		return false
	default:
		return true
	}
}

func (s *Server) handleWalletSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address         string   `json:"address"`
		AmountSat       int64    `json:"amount_sat"`
		SatPerVbyte     int64    `json:"sat_per_vbyte"`
		SweepAll        bool     `json:"sweep_all"`
		Outpoints       []string `json:"outpoints"`
		Label           string   `json:"label"`
		ConfirmPassword string   `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	address := strings.TrimSpace(req.Address)
	if address == "" {
		writeError(w, http.StatusBadRequest, "address required")
		return
	}
	if !req.SweepAll && req.AmountSat <= 0 {
		writeError(w, http.StatusBadRequest, "amount_sat must be positive")
		return
	}
	if req.SweepAll {
		req.AmountSat = 0
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	destinationClassification, _ := s.classifyOnchainDestination(ctx, address)
	if s.auth != nil && s.auth.Enabled() && requiresWalletSendConfirmation(destinationClassification) {
		session, ok := authSessionFromContext(r.Context())
		if !ok {
			writeErrorCode(w, http.StatusUnauthorized, "auth_required", "authentication required")
			return
		}
		if !s.auth.HasRecentReauth(session.ID, authScopeWalletSendExternal) {
			confirmPassword := strings.TrimSpace(req.ConfirmPassword)
			if confirmPassword != "" {
				if _, err := s.auth.reauth(session.ID, confirmPassword, authScopeWalletSendExternal); err != nil {
					s.recordAuditEventAsync(r, "auth.reauth.failed", authScopeWalletSendExternal, map[string]any{"reason": authAuditReason(err)})
					writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "invalid credentials")
					return
				}
				s.recordAuditEventAsync(r, "auth.reauth.succeeded", authScopeWalletSendExternal, nil)
			} else {
				writeJSON(w, http.StatusPreconditionRequired, map[string]any{
					"error":                          "password confirmation required for external on-chain sends",
					"code":                           "wallet_send_external_reauth_required",
					"destination_classification":     destinationClassification,
					"requires_password_confirmation": true,
				})
				return
			}
		}
	}

	var (
		txid    string
		sendErr error
	)
	if len(req.Outpoints) > 0 || strings.TrimSpace(req.Label) != "" {
		txid, sendErr = s.lnd.SendCoinsAdvanced(ctx, lndclient.SendCoinsParams{
			Address:     address,
			AmountSat:   req.AmountSat,
			SatPerVbyte: req.SatPerVbyte,
			SendAll:     req.SweepAll,
			Outpoints:   req.Outpoints,
			Label:       req.Label,
		})
	} else {
		txid, sendErr = s.lnd.SendCoins(ctx, address, req.AmountSat, req.SatPerVbyte, req.SweepAll)
	}
	if sendErr != nil {
		s.recordAuditEventAsync(r, "wallet.onchain_send.failed", destinationClassification, map[string]any{
			"amount_sat":         req.AmountSat,
			"sat_per_vbyte":      req.SatPerVbyte,
			"sweep_all":          req.SweepAll,
			"selected_outpoints": len(req.Outpoints),
		})
		msg := lndRPCErrorMessage(sendErr)
		if isTimeoutError(sendErr) {
			msg = lndStatusMessage(sendErr)
		}
		if msg == "" || msg == "LND error" {
			msg = "On-chain send failed"
		}
		writeError(w, http.StatusInternalServerError, msg)
		return
	}
	s.recordAuditEventAsync(r, "wallet.onchain_send.broadcast", strings.TrimSpace(txid), map[string]any{
		"destination_classification": destinationClassification,
		"amount_sat":                 req.AmountSat,
		"sat_per_vbyte":              req.SatPerVbyte,
		"sweep_all":                  req.SweepAll,
		"selected_outpoints":         len(req.Outpoints),
	})

	writeJSON(w, http.StatusOK, map[string]string{"txid": txid})
}

func (s *Server) handleWalletSendPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address     string   `json:"address"`
		AmountSat   int64    `json:"amount_sat"`
		SatPerVbyte int64    `json:"sat_per_vbyte"`
		SweepAll    bool     `json:"sweep_all"`
		Outpoints   []string `json:"outpoints"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	preview, err := s.lnd.PreviewOnchainSend(ctx, req.Address, req.AmountSat, req.SatPerVbyte, req.SweepAll, req.Outpoints)
	if err != nil {
		msg := lndRPCErrorMessage(err)
		if isTimeoutError(err) {
			msg = lndStatusMessage(err)
		}
		if msg == "" || msg == "LND error" {
			msg = "On-chain preview unavailable"
		}
		writeError(w, http.StatusInternalServerError, msg)
		return
	}

	destinationClassification, _ := s.classifyOnchainDestination(ctx, req.Address)
	writeJSON(w, http.StatusOK, map[string]any{
		"address":                        preview.Address,
		"sweep_all":                      preview.SweepAll,
		"requested_amount_sat":           preview.RequestedAmountSat,
		"recipient_amount_sat":           preview.RecipientAmountSat,
		"fee_sat":                        preview.FeeSat,
		"change_sat":                     preview.ChangeSat,
		"total_debit_sat":                preview.TotalDebitSat,
		"spendable_sat":                  preview.SpendableSat,
		"spendable_utxo_count":           preview.SpendableUtxoCount,
		"selected_input_count":           preview.SelectedInputCount,
		"selected_input_sat":             preview.SelectedInputSat,
		"estimated_vbytes":               preview.EstimatedVbytes,
		"sat_per_vbyte":                  preview.SatPerVbyte,
		"enough_funds":                   preview.EnoughFunds,
		"exact":                          preview.Exact,
		"message":                        preview.Message,
		"destination_classification":     destinationClassification,
		"requires_password_confirmation": requiresWalletSendConfirmation(destinationClassification),
	})
}

type rpcStatusError struct {
	statusCode int
	message    string
}

func (e rpcStatusError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("rpc status %d", e.statusCode)
	}
	return fmt.Sprintf("rpc status %d: %s", e.statusCode, e.message)
}

type rpcErrorDetail struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcErrorPayload struct {
	Error *rpcErrorDetail `json:"error"`
}

type bitcoinInfo struct {
	Chain                string  `json:"chain"`
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
	BestBlockHash        string  `json:"bestblockhash"`
	Pruned               bool    `json:"pruned"`
	PruneHeight          int64   `json:"pruneheight"`
	PruneTargetSize      int64   `json:"prune_target_size"`
	SizeOnDisk           int64   `json:"size_on_disk"`
}

type bitcoinNetworkInfo struct {
	Version        int                          `json:"version"`
	Subversion     string                       `json:"subversion"`
	Connections    int                          `json:"connections"`
	LocalAddresses []bitcoinNetworkLocalAddress `json:"localaddresses"`
}

type bitcoinNetworkLocalAddress struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type bitcoinRPCResponse struct {
	Result bitcoinInfo     `json:"result"`
	Error  *rpcErrorDetail `json:"error"`
}

type bitcoinNetworkRPCResponse struct {
	Result bitcoinNetworkInfo `json:"result"`
	Error  *rpcErrorDetail    `json:"error"`
}

func parseRPCError(body []byte) string {
	var payload rpcErrorPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if payload.Error == nil {
		return ""
	}
	return payload.Error.Message
}

func parseBitcoinInfo(body []byte) (bitcoinInfo, error) {
	var payload bitcoinRPCResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return bitcoinInfo{}, err
	}
	if payload.Error != nil {
		return bitcoinInfo{}, fmt.Errorf(payload.Error.Message)
	}
	return payload.Result, nil
}

func parseBitcoinNetworkInfo(body []byte) (bitcoinNetworkInfo, error) {
	var payload bitcoinNetworkRPCResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return bitcoinNetworkInfo{}, err
	}
	if payload.Error != nil {
		return bitcoinNetworkInfo{}, fmt.Errorf(payload.Error.Message)
	}
	return payload.Result, nil
}

func doBitcoinRPC(ctx context.Context, url, user, pass, method string) ([]byte, error) {
	payload := map[string]any{
		"jsonrpc": "1.0",
		"id":      "lightningos",
		"method":  method,
		"params":  []any{},
	}
	buf, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := parseRPCError(body)
		return nil, rpcStatusError{statusCode: resp.StatusCode, message: msg}
	}

	if msg := parseRPCError(body); msg != "" {
		return nil, rpcStatusError{statusCode: resp.StatusCode, message: msg}
	}
	return body, nil
}

func fetchBitcoinRPC(ctx context.Context, host, user, pass, method string) ([]byte, error) {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return doBitcoinRPC(ctx, host, user, pass, method)
	}

	body, err := doBitcoinRPC(ctx, "http://"+host, user, pass, method)
	if err == nil {
		return body, nil
	}
	var statusErr rpcStatusError
	if err != nil && errors.As(err, &statusErr) {
		return nil, err
	}

	body, httpsErr := doBitcoinRPC(ctx, "https://"+host, user, pass, method)
	if httpsErr == nil {
		return body, nil
	}
	if err != nil && httpsErr != nil {
		return nil, fmt.Errorf("rpc http failed: %v; https failed: %v", err, httpsErr)
	}
	if err != nil {
		return nil, err
	}
	return nil, httpsErr
}

func fetchBitcoinInfo(ctx context.Context, host, user, pass string) (bitcoinInfo, error) {
	body, err := fetchBitcoinRPC(ctx, host, user, pass, "getblockchaininfo")
	if err != nil {
		return bitcoinInfo{}, err
	}
	return parseBitcoinInfo(body)
}

func fetchBitcoinNetworkInfo(ctx context.Context, host, user, pass string) (bitcoinNetworkInfo, error) {
	body, err := fetchBitcoinRPC(ctx, host, user, pass, "getnetworkinfo")
	if err != nil {
		return bitcoinNetworkInfo{}, err
	}
	return parseBitcoinNetworkInfo(body)
}

func applyBitcoinInfoToStatus(status *bitcoinStatus, info bitcoinInfo) {
	if status == nil {
		return
	}
	status.RPCOk = true
	status.Chain = info.Chain
	status.Blocks = info.Blocks
	status.Headers = info.Headers
	status.VerificationProgress = info.VerificationProgress
	status.InitialBlockDownload = info.InitialBlockDownload
	status.BestBlockHash = info.BestBlockHash
	status.Pruned = info.Pruned
	status.PruneHeight = info.PruneHeight
	status.PruneTargetSize = info.PruneTargetSize
	status.SizeOnDisk = info.SizeOnDisk
}

func applyBitcoinNetworkInfoToStatus(status *bitcoinStatus, info bitcoinNetworkInfo) {
	if status == nil {
		return
	}
	status.Connections = info.Connections
	status.Version = info.Version
	status.Subversion = info.Subversion
}

func testBitcoinRPC(ctx context.Context, host, user, pass string) (bool, error) {
	_, err := fetchBitcoinInfo(ctx, host, user, pass)
	if err != nil {
		return false, err
	}
	return true, nil
}

func storeBitcoinSecrets(user, pass string) error {
	user = strings.TrimSpace(user)
	pass = strings.TrimSpace(pass)
	_ = os.Setenv("BITCOIN_RPC_USER", user)
	_ = os.Setenv("BITCOIN_RPC_PASS", pass)
	content, _ := os.ReadFile(secretsPath)
	lines := []string{}
	if len(content) > 0 {
		lines = strings.Split(string(content), "\n")
	}
	hasUser := false
	hasPass := false

	for i, line := range lines {
		if strings.HasPrefix(line, "BITCOIN_RPC_USER=") {
			lines[i] = "BITCOIN_RPC_USER=" + user
			hasUser = true
		}
		if strings.HasPrefix(line, "BITCOIN_RPC_PASS=") {
			lines[i] = "BITCOIN_RPC_PASS=" + pass
			hasPass = true
		}
	}

	if !hasUser {
		lines = append(lines, "BITCOIN_RPC_USER="+user)
	}
	if !hasPass {
		lines = append(lines, "BITCOIN_RPC_PASS="+pass)
	}

	if err := os.MkdirAll(filepath.Dir(secretsPath), 0750); err != nil {
		return err
	}
	return os.WriteFile(secretsPath, []byte(strings.Join(lines, "\n")), 0660)
}

func readBitcoinSource() string {
	envValue := strings.TrimSpace(os.Getenv("BITCOIN_SOURCE"))
	secretsRaw := ""
	if content, err := os.ReadFile(secretsPath); err == nil {
		secretsRaw = string(content)
	}
	lndConfRaw := ""
	if raw, err := os.ReadFile(lndConfPath); err == nil {
		lndConfRaw = string(raw)
	}
	return resolveBitcoinSource(envValue, secretsRaw, lndConfRaw)
}

func detectBitcoinSourceFromLNDConf() string {
	raw, err := os.ReadFile(lndConfPath)
	if err != nil {
		return ""
	}
	return parseBitcoinSourceFromLNDConf(string(raw))
}

func resolveBitcoinSource(envValue, secretsRaw, lndConfRaw string) string {
	if detected := parseBitcoinSourceFromLNDConf(lndConfRaw); detected != "" {
		return detected
	}
	if normalized := normalizeBitcoinSource(envValue); normalized != "" {
		return normalized
	}
	if normalized := parseBitcoinSourceFromSecrets(secretsRaw); normalized != "" {
		return normalized
	}
	return "remote"
}

func parseBitcoinSourceFromSecrets(raw string) string {
	if raw == "" {
		return ""
	}
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "BITCOIN_SOURCE=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "BITCOIN_SOURCE="))
		if normalized := normalizeBitcoinSource(value); normalized != "" {
			return normalized
		}
	}
	return ""
}

func normalizeBitcoinSource(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "local" || trimmed == "remote" {
		return trimmed
	}
	return ""
}

func parseBitcoinSourceFromLNDConf(raw string) string {
	if raw == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	inBitcoind := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inBitcoind = strings.EqualFold(trimmed, "[Bitcoind]")
			continue
		}
		if !inBitcoind {
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "bitcoind.rpchost=") {
			host := strings.TrimSpace(strings.TrimPrefix(trimmed, "bitcoind.rpchost="))
			if host == "" {
				continue
			}
			if isLocalRPCHost(host) {
				return "local"
			}
			return "remote"
		}
	}
	return ""
}

func isLocalRPCHost(value string) bool {
	host := extractHost(value)
	if host == "" {
		return false
	}
	lower := strings.ToLower(host)
	if lower == "localhost" {
		return true
	}
	ip := net.ParseIP(lower)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	return isHostIP(ip)
}

func extractHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
			trimmed = parsed.Host
		}
	}
	if at := strings.LastIndex(trimmed, "@"); at != -1 {
		trimmed = trimmed[at+1:]
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		return host
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		return strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
	}
	return trimmed
}

func isHostIP(ip net.IP) bool {
	for _, addr := range localInterfaceIPs() {
		if ip.Equal(addr) {
			return true
		}
	}
	return false
}

func localInterfaceIPs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	ips := []net.IP{}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				if v.IP != nil {
					ips = append(ips, v.IP)
				}
			case *net.IPAddr:
				if v.IP != nil {
					ips = append(ips, v.IP)
				}
			}
		}
	}
	return ips
}

func storeBitcoinSource(source string) error {
	trimmed := strings.ToLower(strings.TrimSpace(source))
	if trimmed == "" {
		trimmed = "remote"
	}
	if trimmed != "local" && trimmed != "remote" {
		return errors.New("invalid bitcoin source")
	}
	_ = os.Setenv("BITCOIN_SOURCE", trimmed)
	content, _ := os.ReadFile(secretsPath)
	lines := []string{}
	if len(content) > 0 {
		lines = strings.Split(string(content), "\n")
	}
	hasKey := false
	for i, line := range lines {
		if strings.HasPrefix(line, "BITCOIN_SOURCE=") {
			lines[i] = "BITCOIN_SOURCE=" + trimmed
			hasKey = true
		}
	}
	if !hasKey {
		lines = append(lines, "BITCOIN_SOURCE="+trimmed)
	}
	if err := os.MkdirAll(filepath.Dir(secretsPath), 0750); err != nil {
		return err
	}
	return os.WriteFile(secretsPath, []byte(strings.Join(lines, "\n")), 0660)
}

func normalizeLocalZMQ(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	if strings.HasPrefix(trimmed, "tcp://0.0.0.0:") {
		return "tcp://127.0.0.1:" + strings.TrimPrefix(trimmed, "tcp://0.0.0.0:")
	}
	if strings.HasPrefix(trimmed, "0.0.0.0:") {
		return "tcp://127.0.0.1:" + strings.TrimPrefix(trimmed, "0.0.0.0:")
	}
	if strings.HasPrefix(trimmed, "tcp://") {
		return trimmed
	}
	return "tcp://" + trimmed
}

func dockerContainerGateways(ctx context.Context, containerID string) []string {
	if containerID == "" {
		return []string{}
	}
	out, err := system.RunCommandWithSudo(
		ctx,
		"docker",
		"inspect",
		"-f",
		"{{range $k,$v := .NetworkSettings.Networks}}{{println $v.Gateway}}{{end}}",
		containerID,
	)
	if err != nil {
		return []string{}
	}
	gateways := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		trimmed := strings.TrimSpace(line)
		ip := net.ParseIP(trimmed)
		if ip == nil {
			continue
		}
		normalized := ip.String()
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		gateways = append(gateways, normalized)
	}
	return gateways
}

func dockerContainerCIDRs(ctx context.Context, containerID string) []string {
	if containerID == "" {
		return []string{}
	}
	out, err := system.RunCommandWithSudo(
		ctx,
		"docker",
		"inspect",
		"-f",
		"{{range $k,$v := .NetworkSettings.Networks}}{{printf \"%s/%d\\n\" $v.IPAddress $v.IPPrefixLen}}{{end}}",
		containerID,
	)
	if err != nil {
		return []string{}
	}
	cidrs := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "/") {
			continue
		}
		if _, cidr, parseErr := net.ParseCIDR(trimmed); parseErr == nil && cidr != nil {
			normalized := cidr.String()
			if normalized == "" || seen[normalized] {
				continue
			}
			seen[normalized] = true
			cidrs = append(cidrs, normalized)
		}
	}
	return cidrs
}

func updateLNDConfRPC(ctx context.Context, user, pass, host, zmqBlock, zmqTx string) error {
	remoteCfg := bitcoinRPCConfig{
		Host:     host,
		User:     user,
		Pass:     pass,
		ZMQBlock: zmqBlock,
		ZMQTx:    zmqTx,
	}
	localCfg, _, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		localCfg = bitcoinRPCConfig{
			Host:     "127.0.0.1:8332",
			ZMQBlock: "tcp://127.0.0.1:28332",
			ZMQTx:    "tcp://127.0.0.1:28333",
		}
	}
	return updateLNDConfBitcoinSource("remote", remoteCfg, localCfg)
}

func updateLNDConfBitcoinSource(active string, remoteCfg bitcoinRPCConfig, localCfg bitcoinRPCConfig) error {
	content, err := os.ReadFile(lndConfPath)
	if err != nil {
		return err
	}
	raw := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(raw, "\n")

	start := -1
	end := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if strings.EqualFold(trimmed, "[Bitcoind]") {
				start = i
				continue
			}
			if start != -1 && i > start {
				end = i
				break
			}
		}
	}

	if start == -1 {
		lines = append(lines, "[Bitcoind]")
		start = len(lines) - 1
		end = len(lines)
	}

	remoteUpdates := map[string]string{
		"bitcoind.rpchost":        remoteCfg.Host,
		"bitcoind.rpcuser":        remoteCfg.User,
		"bitcoind.rpcpass":        remoteCfg.Pass,
		"bitcoind.zmqpubrawblock": remoteCfg.ZMQBlock,
		"bitcoind.zmqpubrawtx":    remoteCfg.ZMQTx,
	}
	localUpdates := map[string]string{
		"bitcoind.rpchost":        localCfg.Host,
		"bitcoind.rpcuser":        localCfg.User,
		"bitcoind.rpcpass":        localCfg.Pass,
		"bitcoind.zmqpubrawblock": localCfg.ZMQBlock,
		"bitcoind.zmqpubrawtx":    localCfg.ZMQTx,
	}

	currentGroup := ""
	foundRemote := false
	foundLocal := false
	for i := start + 1; i < end; i++ {
		rawLine := lines[i]
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}

		marker := strings.TrimSpace(strings.TrimLeft(trimmed, "#;"))
		if strings.EqualFold(marker, "LightningOS Bitcoin Remote") {
			lines[i] = "# LightningOS Bitcoin Remote"
			currentGroup = "remote"
			foundRemote = true
			continue
		}
		if strings.EqualFold(marker, "LightningOS Bitcoin Local") {
			lines[i] = "# LightningOS Bitcoin Local"
			currentGroup = "local"
			foundLocal = true
			continue
		}

		if currentGroup == "" {
			continue
		}

		clean := strings.TrimSpace(strings.TrimLeft(trimmed, "#;"))
		parts := strings.SplitN(clean, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])

		var value string
		if currentGroup == "remote" {
			value = remoteUpdates[key]
		} else if currentGroup == "local" {
			value = localUpdates[key]
		}
		if value == "" {
			continue
		}

		prefix := ""
		if currentGroup != active {
			prefix = "# "
		}
		lines[i] = prefix + key + "=" + value
	}

	if !foundRemote || !foundLocal {
		remotePrefix := "# "
		localPrefix := "# "
		if active == "remote" {
			remotePrefix = ""
		} else if active == "local" {
			localPrefix = ""
		}
		block := []string{
			"",
			"# LightningOS Bitcoin Remote",
			remotePrefix + "bitcoind.rpchost=" + remoteCfg.Host,
			remotePrefix + "bitcoind.rpcuser=" + remoteCfg.User,
			remotePrefix + "bitcoind.rpcpass=" + remoteCfg.Pass,
			remotePrefix + "bitcoind.zmqpubrawblock=" + remoteCfg.ZMQBlock,
			remotePrefix + "bitcoind.zmqpubrawtx=" + remoteCfg.ZMQTx,
			"",
			"# LightningOS Bitcoin Local",
			localPrefix + "bitcoind.rpchost=" + localCfg.Host,
			localPrefix + "bitcoind.rpcuser=" + localCfg.User,
			localPrefix + "bitcoind.rpcpass=" + localCfg.Pass,
			localPrefix + "bitcoind.zmqpubrawblock=" + localCfg.ZMQBlock,
			localPrefix + "bitcoind.zmqpubrawtx=" + localCfg.ZMQTx,
		}
		lines = append(lines[:end], append(block, lines[end:]...)...)
	}

	return os.WriteFile(lndConfPath, []byte(strings.Join(lines, "\n")), 0660)
}

func storeWalletUnlock(password string) error {
	trimmed := strings.TrimSpace(password)
	if trimmed == "" {
		return errors.New("wallet password required")
	}
	if err := storeWalletPassword(trimmed); err != nil {
		return err
	}
	return ensureWalletUnlockConfig()
}

func (s *Server) scheduleLNDPermissionsFix(reason string) {
	if s == nil {
		return
	}
	go func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 12*time.Second)
		waitForFile(waitCtx, lndAdminMacaroonPath)
		waitCancel()
		runCtx, runCancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer runCancel()
		if _, err := system.RunCommandWithSudo(runCtx, lndFixPermsScript); err != nil {
			s.logger.Printf("lnd permissions fix failed (%s): %v", reason, err)
		}
	}()
}

func waitForFile(ctx context.Context, path string) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func storeWalletPassword(password string) error {
	if _, err := os.Stat(lndPasswordPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("password file missing: %s", lndPasswordPath)
		}
		return err
	}
	return os.WriteFile(lndPasswordPath, []byte(password), 0660)
}

func walletPasswordAvailable() bool {
	info, err := os.Stat(lndPasswordPath)
	if err != nil || info.Size() == 0 {
		return false
	}
	content, err := os.ReadFile(lndPasswordPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(content)) != ""
}

func ensureWalletUnlockConfig() error {
	if err := os.MkdirAll(filepath.Dir(lndConfPath), 0750); err != nil {
		return err
	}
	raw, _ := os.ReadFile(lndConfPath)
	updated := ensureUnlockLines(string(raw))
	return os.WriteFile(lndConfPath, []byte(updated), 0660)
}

func ensureUnlockLines(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	start := -1
	end := len(lines)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if strings.EqualFold(trimmed, "[Application Options]") {
				start = i
				continue
			}
			if start != -1 && i > start {
				end = i
				break
			}
		}
	}

	if start == -1 {
		lines = append(lines, "[Application Options]")
		start = len(lines) - 1
		end = len(lines)
	}

	hasPass := false
	hasAllow := false
	for i := start + 1; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "wallet-unlock-password-file=") {
			lines[i] = "wallet-unlock-password-file=" + lndPasswordPath
			hasPass = true
		}
		if strings.HasPrefix(trimmed, "wallet-unlock-allow-create=") {
			lines[i] = "wallet-unlock-allow-create=true"
			hasAllow = true
		}
	}

	extra := []string{}
	if !hasPass {
		extra = append(extra, "wallet-unlock-password-file="+lndPasswordPath)
	}
	if !hasAllow {
		extra = append(extra, "wallet-unlock-allow-create=true")
	}
	if len(extra) > 0 {
		lines = append(lines[:end], append(extra, lines[end:]...)...)
	}

	return strings.Join(lines, "\n")
}
