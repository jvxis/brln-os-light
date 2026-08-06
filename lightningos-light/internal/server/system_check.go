package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/system"
)

type systemCheckTone string

const (
	systemCheckOK     systemCheckTone = "ok"
	systemCheckWarn   systemCheckTone = "warn"
	systemCheckDanger systemCheckTone = "danger"
	systemCheckMuted  systemCheckTone = "muted"
)

type systemCheckResponse struct {
	Status    string             `json:"status"`
	CheckedAt string             `json:"checked_at"`
	Groups    []systemCheckGroup `json:"groups"`
}

type systemCheckGroup struct {
	ID      string            `json:"id"`
	Label   string            `json:"label"`
	Status  systemCheckTone   `json:"status"`
	Summary string            `json:"summary"`
	Items   []systemCheckItem `json:"items"`
}

type systemCheckItem struct {
	ID         string          `json:"id"`
	Label      string          `json:"label"`
	Status     systemCheckTone `json:"status"`
	Detail     string          `json:"detail,omitempty"`
	Value      any             `json:"value,omitempty"`
	Diagnostic string          `json:"diagnostic,omitempty"`
	LogSource  string          `json:"log_source,omitempty"`
	LogTail    []string        `json:"log_tail,omitempty"`
}

const systemCheckLogLines = 12

var (
	systemCheckKeyValueSecretPattern = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(password|passwd|pass|token|secret|macaroon|credential|adminpw)[a-z0-9_.-]*)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
	systemCheckURLPasswordPattern    = regexp.MustCompile(`://([^:\s/@]+):([^@\s]+)@`)
	systemCheckBasicAuthPattern      = regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9+/=._~-]+`)
)

func (s *Server) handleSystemCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	writeJSON(w, http.StatusOK, s.systemCheck(ctx))
}

func (s *Server) systemCheck(ctx context.Context) systemCheckResponse {
	builders := []func(context.Context) systemCheckGroup{
		s.systemCheckApp,
		s.systemCheckLND,
		s.systemCheckBitcoin,
		s.systemCheckPostgres,
		s.systemCheckTor,
		s.systemCheckSecurity,
		s.systemCheckSystem,
	}

	groups := make([]systemCheckGroup, len(builders))
	var wg sync.WaitGroup
	for idx, build := range builders {
		idx := idx
		build := build
		wg.Add(1)
		go func() {
			defer wg.Done()
			groupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			groups[idx] = build(groupCtx)
		}()
	}
	wg.Wait()

	return systemCheckResponse{
		Status:    systemCheckOverallStatus(groups),
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Groups:    groups,
	}
}

func (s *Server) systemCheckSecurity(ctx context.Context) systemCheckGroup {
	status := inspectManagerFirewall(ctx)
	firewallTone := systemCheckWarn
	firewallDetail := "UFW is not installed; LightningOS did not change firewall rules"
	if status.Installed && !status.StatusAvailable {
		firewallDetail = "UFW status is unavailable; existing rules were not changed"
	} else if status.StatusAvailable && !status.Active {
		firewallDetail = "UFW is inactive; the saved LAN policy is not being enforced"
	} else if status.Active {
		firewallTone = systemCheckOK
		firewallDetail = "active"
	}

	accessTone := systemCheckWarn
	accessDetail := "Manager access policy is not configured"
	if !status.ConfigValid {
		accessDetail = "Saved LAN network is missing or invalid"
	} else if !status.Active {
		accessDetail = "Configured for " + status.ConfiguredCIDR + ", but UFW is inactive"
	} else if status.BroadRulePresent {
		accessTone = systemCheckDanger
		accessDetail = "Port 8443 has a broad allow rule"
	} else if status.ManagerAccessBound {
		accessTone = systemCheckOK
		if strings.EqualFold(status.ConfiguredCIDR, "none") {
			accessDetail = "Port 8443 has no broad LAN rule"
		} else {
			accessDetail = "Port 8443 is restricted to " + status.ConfiguredCIDR
		}
	} else {
		accessTone = systemCheckDanger
		accessDetail = "UFW is active, but the expected port 8443 restriction is missing"
	}

	return newSystemCheckGroup("security", "Security", []systemCheckItem{
		{
			ID:     "ufw",
			Label:  "Host firewall",
			Status: firewallTone,
			Detail: firewallDetail,
			Value:  status.Active,
		},
		{
			ID:     "manager_access",
			Label:  "Manager LAN access",
			Status: accessTone,
			Detail: accessDetail,
			Value:  status.ConfiguredCIDR,
		},
	})
}

func (s *Server) systemCheckApp(ctx context.Context) systemCheckGroup {
	items := []systemCheckItem{
		{
			ID:     "api",
			Label:  "API",
			Status: systemCheckOK,
			Detail: "responding",
			Value:  true,
		},
	}

	managerActive := system.SystemctlIsActive(ctx, "lightningos-manager")
	items = append(items, boolSystemCheckItem(
		"manager_service",
		"lightningos-manager",
		managerActive,
		systemCheckDanger,
		"active",
		"inactive",
	))

	reportsActive := system.SystemctlIsActive(ctx, "lightningos-reports.timer")
	items = append(items, boolSystemCheckItem(
		"reports_timer",
		"lightningos-reports.timer",
		reportsActive,
		systemCheckWarn,
		"active",
		"inactive",
	))

	terminalEnabled := strings.TrimSpace(os.Getenv("TERMINAL_ENABLED")) == "1"
	terminalTone := systemCheckMuted
	terminalDetail := "disabled"
	if terminalEnabled {
		if system.SystemctlIsActive(ctx, "lightningos-terminal") {
			terminalTone = systemCheckOK
			terminalDetail = "active"
		} else {
			terminalTone = systemCheckWarn
			terminalDetail = "enabled but inactive"
		}
	}
	items = append(items, systemCheckItem{
		ID:     "terminal_service",
		Label:  "lightningos-terminal",
		Status: terminalTone,
		Detail: terminalDetail,
		Value:  terminalEnabled,
	})

	group := newSystemCheckGroup("app", "App", items)
	return s.withSystemCheckDiagnostics(ctx, group)
}

func (s *Server) systemCheckLND(ctx context.Context) systemCheckGroup {
	status, err := s.lndStatus(ctx, false)
	items := []systemCheckItem{
		boolSystemCheckItem(
			"service",
			"Service",
			status.ServiceActive,
			systemCheckDanger,
			"active",
			"inactive",
		),
	}

	rpcTone := systemCheckOK
	rpcDetail := "fresh"
	if err != nil {
		endpointReachable := false
		if status.ServiceActive && s.cfg != nil {
			endpointReachable = testTCP(s.cfg.LND.GRPCHost)
		}
		issue := classifyLNDHealthError(err, s.lndWarmupActive(), status.ServiceActive, endpointReachable)
		rpcDetail = issue.Message
		if issue.Level == "WARN" {
			rpcTone = systemCheckWarn
		} else {
			rpcTone = systemCheckDanger
		}
	} else if !status.InfoKnown {
		rpcTone = systemCheckWarn
		rpcDetail = "GetInfo not available"
	} else if status.InfoStale {
		rpcTone = systemCheckWarn
		rpcDetail = fmt.Sprintf("stale for %ds", status.InfoAgeSeconds)
	}
	items = append(items, systemCheckItem{
		ID:     "rpc",
		Label:  "RPC",
		Status: rpcTone,
		Detail: rpcDetail,
		Value:  err == nil && status.InfoKnown && !status.InfoStale,
	})

	walletTone := systemCheckMuted
	walletDetail := "unknown"
	if status.WalletState != "" {
		walletDetail = status.WalletState
		walletTone = systemCheckWarn
		if status.WalletState == "unlocked" {
			walletTone = systemCheckOK
		}
		if status.WalletState == "locked" {
			walletTone = systemCheckDanger
		}
	}
	items = append(items, systemCheckItem{
		ID:     "wallet",
		Label:  "Wallet",
		Status: walletTone,
		Detail: walletDetail,
		Value:  status.WalletState,
	})

	items = append(items,
		boolSystemCheckItem("synced_to_chain", "Chain sync", status.SyncedToChain, systemCheckWarn, "synced", "not synced"),
		boolSystemCheckItem("synced_to_graph", "Graph sync", status.SyncedToGraph, systemCheckWarn, "synced", "not synced"),
		systemCheckItem{
			ID:     "channels",
			Label:  "Channels",
			Status: systemCheckMuted,
			Detail: fmt.Sprintf("%d active / %d inactive", status.Channels.Active, status.Channels.Inactive),
			Value:  status.Channels.Active,
		},
		systemCheckItem{
			ID:     "db_backend",
			Label:  "DB backend",
			Status: systemCheckMuted,
			Detail: nonEmpty(status.DBBackend, "unknown"),
			Value:  status.DBBackend,
		},
	)

	group := newSystemCheckGroup("lnd", "LND", items)
	return s.withSystemCheckDiagnostics(ctx, group)
}

func (s *Server) systemCheckBitcoin(ctx context.Context) systemCheckGroup {
	source := readBitcoinSource()
	status, err := s.bitcoinActiveStatusCached(ctx)
	items := []systemCheckItem{
		{
			ID:     "source",
			Label:  "Source",
			Status: systemCheckMuted,
			Detail: source,
			Value:  source,
		},
	}

	if err != nil {
		items = append(items, systemCheckItem{
			ID:     "active_check",
			Label:  "Active check",
			Status: systemCheckWarn,
			Detail: err.Error(),
		})
	}

	rpcTone := systemCheckDanger
	rpcDetail := "unreachable"
	if status.RPCOk {
		rpcTone = systemCheckOK
		rpcDetail = "reachable"
		if status.RPCStale {
			rpcTone = systemCheckWarn
			rpcDetail = fmt.Sprintf("stale for %ds", status.RPCLastOKAgeSeconds)
		}
	}
	items = append(items, systemCheckItem{
		ID:     "rpc",
		Label:  "RPC",
		Status: rpcTone,
		Detail: rpcDetail,
		Value:  status.RPCOk,
	})

	items = append(items,
		boolSystemCheckItem("zmq_rawblock", "ZMQ rawblock", status.ZMQRawBlockOk, systemCheckWarn, "reachable", "unreachable"),
		boolSystemCheckItem("zmq_rawtx", "ZMQ rawtx", status.ZMQRawTxOk, systemCheckWarn, "reachable", "unreachable"),
	)

	syncTone := systemCheckMuted
	syncDetail := "unknown"
	if status.VerificationProgress > 0 || status.Blocks > 0 || status.Headers > 0 {
		syncTone = systemCheckOK
		syncDetail = fmt.Sprintf("%.2f%% | %d/%d blocks", status.VerificationProgress*100, status.Blocks, status.Headers)
		if status.InitialBlockDownload || (status.Headers > 0 && status.Blocks < status.Headers) {
			syncTone = systemCheckWarn
		}
	}
	items = append(items, systemCheckItem{
		ID:     "sync",
		Label:  "Sync",
		Status: syncTone,
		Detail: syncDetail,
		Value:  status.VerificationProgress,
	})

	if source == "local" {
		localStatus, localErr := s.bitcoinLocalStatusCached(ctx)
		localTone := systemCheckMuted
		localDetail := "external"
		localValue := localStatus.Status
		if localErr != nil {
			localTone = systemCheckWarn
			localDetail = localErr.Error()
		} else if localStatus.Source == "app" {
			localDetail = localStatus.Status
			localTone = systemCheckDanger
			if localStatus.Status == "running" {
				localTone = systemCheckOK
			}
		} else if localStatus.Source == "external" {
			localTone = systemCheckMuted
			localDetail = "external systemd/local RPC"
		}
		items = append(items, systemCheckItem{
			ID:     "bitcoind_runtime",
			Label:  "bitcoind",
			Status: localTone,
			Detail: localDetail,
			Value:  localValue,
		})
	}

	group := newSystemCheckGroup("bitcoin", "Bitcoin", items)
	return s.withSystemCheckDiagnostics(ctx, group)
}

func (s *Server) systemCheckPostgres(ctx context.Context) systemCheckGroup {
	status := s.postgresStatus(ctx)
	items := []systemCheckItem{
		boolSystemCheckItem("service", "Service", status.ServiceActive, systemCheckDanger, "active", "inactive"),
	}
	if status.Version != "" {
		items = append(items, systemCheckItem{
			ID:     "version",
			Label:  "Version",
			Status: systemCheckMuted,
			Detail: status.Version,
			Value:  status.Version,
		})
	}

	if len(status.Databases) == 0 {
		items = append(items, systemCheckItem{
			ID:     "databases",
			Label:  "Databases",
			Status: systemCheckMuted,
			Detail: "no configured DSNs found",
			Value:  0,
		})
	} else {
		for _, db := range status.Databases {
			tone := systemCheckDanger
			detail := "unavailable"
			if db.Available {
				tone = systemCheckOK
				detail = fmt.Sprintf("%d MB | %d connection(s)", db.SizeMB, db.Connections)
			}
			items = append(items, systemCheckItem{
				ID:     "database_" + stableID(db.Source, db.Name),
				Label:  nonEmpty(db.Name, "database"),
				Status: tone,
				Detail: detail,
				Value:  db.Available,
			})
		}
	}

	group := newSystemCheckGroup("postgres", "Postgres", items)
	return s.withSystemCheckDiagnostics(ctx, group)
}

func (s *Server) systemCheckTor(ctx context.Context) systemCheckGroup {
	unit, active := firstActiveSystemdUnit(ctx, []string{"tor@default", "tor"})
	unitDetail := unit
	if unitDetail == "" {
		unitDetail = "no active unit"
	}
	items := []systemCheckItem{
		boolSystemCheckItem("service", "Service", active, systemCheckWarn, unitDetail, unitDetail),
		boolSystemCheckItem("socks_port", "SOCKS 9050", testTCP("127.0.0.1:9050"), systemCheckWarn, "listening", "not listening"),
		boolSystemCheckItem("control_port", "Control 9051", testTCP("127.0.0.1:9051"), systemCheckWarn, "listening", "not listening"),
	}

	upgrade := torUpgradeStatus(ctx)
	versionTone := systemCheckOK
	versionDetail := upgrade.Version
	versionDiagnostic := ""
	if versionDetail == "" {
		versionTone = systemCheckMuted
		versionDetail = "unavailable"
	} else if !upgrade.RepositoryOfficial {
		versionTone = systemCheckWarn
		versionDiagnostic = "Official Tor Project repository is not configured; the available candidate may be stale."
	} else if upgrade.UpdateAvailable {
		versionTone = systemCheckWarn
		versionDetail = fmt.Sprintf("%s (latest %s)", upgrade.Version, upgrade.CandidateVersion)
		versionDiagnostic = "A newer Tor package is available from the official repository."
	} else if upgrade.CandidateVersion != "" {
		versionDetail = fmt.Sprintf("%s (current)", upgrade.Version)
	}
	items = append(items, systemCheckItem{
		ID:         "version",
		Label:      "Version",
		Status:     versionTone,
		Detail:     versionDetail,
		Diagnostic: versionDiagnostic,
		Value:      upgrade.Version,
	})

	svc, errMsg := s.torPeerCheckerService()
	if svc == nil {
		items = append(items, systemCheckItem{
			ID:     "peer_checker",
			Label:  "Peer checker",
			Status: systemCheckMuted,
			Detail: nonEmpty(errMsg, "unavailable"),
		})
	} else {
		snapshot := svc.Snapshot()
		tone := systemCheckMuted
		if snapshot.Enabled {
			tone = systemCheckWarn
			if snapshot.Status == "ok" {
				tone = systemCheckOK
			}
			if snapshot.Status == "checking" {
				tone = systemCheckMuted
			}
		}
		items = append(items, systemCheckItem{
			ID:     "peer_checker",
			Label:  "Peer checker",
			Status: tone,
			Detail: snapshot.Status,
			Value:  snapshot.Enabled,
		})
	}

	group := newSystemCheckGroup("tor", "Tor", items)
	return s.withSystemCheckDiagnostics(ctx, group)
}

func (s *Server) systemCheckSystem(ctx context.Context) systemCheckGroup {
	stats, err := system.GetSystemStats(ctx)
	if err != nil {
		return newSystemCheckGroup("system", "System", []systemCheckItem{{
			ID:     "stats",
			Label:  "Stats",
			Status: systemCheckWarn,
			Detail: err.Error(),
		}})
	}

	cpuPercent := stats.CPUPercentAvg30s
	if cpuPercent == 0 {
		cpuPercent = stats.CPUPercent
	}
	ramPercent := 0.0
	if stats.RAMTotalMB > 0 {
		ramPercent = (float64(stats.RAMUsedMB) / float64(stats.RAMTotalMB)) * 100
	}
	maxDiskPercent := 0.0
	maxDiskMount := ""
	for _, disk := range stats.Disk {
		if disk.UsedPercent > maxDiskPercent {
			maxDiskPercent = disk.UsedPercent
			maxDiskMount = disk.Mount
		}
	}

	items := []systemCheckItem{
		{
			ID:     "uptime",
			Label:  "Uptime",
			Status: systemCheckOK,
			Detail: fmt.Sprintf("%dh", stats.UptimeSec/3600),
			Value:  stats.UptimeSec,
		},
		{
			ID:     "cpu",
			Label:  "CPU",
			Status: thresholdTone(cpuPercent, 75, 90),
			Detail: formatPercentDetail(cpuPercent),
			Value:  cpuPercent,
		},
		{
			ID:     "ram",
			Label:  "RAM",
			Status: thresholdTone(ramPercent, 80, 92),
			Detail: fmt.Sprintf("%d/%d MB (%s)", stats.RAMUsedMB, stats.RAMTotalMB, formatPercentDetail(ramPercent)),
			Value:  ramPercent,
		},
	}

	if stats.TemperatureC > 0 {
		items = append(items, systemCheckItem{
			ID:     "temperature",
			Label:  "Temperature",
			Status: thresholdTone(stats.TemperatureC, 65, 78),
			Detail: fmt.Sprintf("%.1f C", stats.TemperatureC),
			Value:  stats.TemperatureC,
		})
	}
	if maxDiskMount != "" {
		items = append(items, systemCheckItem{
			ID:     "disk_usage",
			Label:  "Disk usage",
			Status: thresholdTone(maxDiskPercent, 80, 92),
			Detail: fmt.Sprintf("%s | %s", maxDiskMount, formatPercentDetail(maxDiskPercent)),
			Value:  maxDiskPercent,
		})
	} else {
		items = append(items, systemCheckItem{
			ID:     "disk_usage",
			Label:  "Disk usage",
			Status: systemCheckMuted,
			Detail: "unavailable",
		})
	}

	return newSystemCheckGroup("system", "System", items)
}

func newSystemCheckGroup(id string, label string, items []systemCheckItem) systemCheckGroup {
	status := systemCheckGroupStatus(items)
	return systemCheckGroup{
		ID:      id,
		Label:   label,
		Status:  status,
		Summary: systemCheckSummary(items),
		Items:   items,
	}
}

func boolSystemCheckItem(id string, label string, ok bool, failureTone systemCheckTone, okDetail string, failDetail string) systemCheckItem {
	tone := systemCheckOK
	detail := okDetail
	if !ok {
		tone = failureTone
		detail = failDetail
	}
	return systemCheckItem{
		ID:     id,
		Label:  label,
		Status: tone,
		Detail: detail,
		Value:  ok,
	}
}

func systemCheckGroupStatus(items []systemCheckItem) systemCheckTone {
	statuses := make([]systemCheckTone, 0, len(items))
	for _, item := range items {
		statuses = append(statuses, item.Status)
	}
	return worstSystemCheckTone(statuses...)
}

func worstSystemCheckTone(statuses ...systemCheckTone) systemCheckTone {
	worst := systemCheckMuted
	for _, status := range statuses {
		if systemCheckToneSeverity(status) > systemCheckToneSeverity(worst) {
			worst = status
		}
	}
	return worst
}

func systemCheckToneSeverity(status systemCheckTone) int {
	switch status {
	case systemCheckDanger:
		return 3
	case systemCheckWarn:
		return 2
	case systemCheckOK:
		return 1
	default:
		return 0
	}
}

func systemCheckOverallStatus(groups []systemCheckGroup) string {
	statuses := make([]systemCheckTone, 0, len(groups))
	for _, group := range groups {
		statuses = append(statuses, group.Status)
	}
	switch worstSystemCheckTone(statuses...) {
	case systemCheckDanger:
		return "ERR"
	case systemCheckWarn:
		return "WARN"
	default:
		return "OK"
	}
}

func systemCheckSummary(items []systemCheckItem) string {
	warnCount := 0
	dangerCount := 0
	okCount := 0
	for _, item := range items {
		switch item.Status {
		case systemCheckDanger:
			dangerCount++
		case systemCheckWarn:
			warnCount++
		case systemCheckOK:
			okCount++
		}
	}
	if dangerCount > 0 {
		return fmt.Sprintf("%d failing check(s)", dangerCount)
	}
	if warnCount > 0 {
		return fmt.Sprintf("%d warning check(s)", warnCount)
	}
	if okCount > 0 {
		return "All checks passed"
	}
	return "No active checks"
}

func firstActiveSystemdUnit(ctx context.Context, units []string) (string, bool) {
	for _, unit := range units {
		if system.SystemctlIsActive(ctx, unit) {
			return unit, true
		}
	}
	return "", false
}

func thresholdTone(value float64, warnAt float64, dangerAt float64) systemCheckTone {
	if value >= dangerAt {
		return systemCheckDanger
	}
	if value >= warnAt {
		return systemCheckWarn
	}
	return systemCheckOK
}

func formatPercentDetail(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64) + "%"
}

func nonEmpty(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func stableID(parts ...string) string {
	joined := strings.ToLower(strings.Join(parts, "_"))
	joined = strings.Trim(joined, "_")
	if joined == "" {
		return "unknown"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range joined {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func (s *Server) withSystemCheckDiagnostics(ctx context.Context, group systemCheckGroup) systemCheckGroup {
	cache := map[string]systemCheckLogSnapshot{}
	for idx := range group.Items {
		if !systemCheckNeedsDiagnostics(group.Items[idx].Status) {
			continue
		}
		source := s.systemCheckLogSource(ctx, group.ID, group.Items[idx])
		if source.kind == "" {
			continue
		}
		key := source.kind + ":" + source.name
		snapshot, ok := cache[key]
		if !ok {
			snapshot = s.readSystemCheckLogSnapshot(ctx, source)
			cache[key] = snapshot
		}
		if snapshot.Source != "" {
			group.Items[idx].LogSource = snapshot.Source
		}
		if len(snapshot.Lines) > 0 {
			group.Items[idx].LogTail = append([]string(nil), snapshot.Lines...)
		}
		if snapshot.Err != "" {
			group.Items[idx].Diagnostic = "Log snapshot unavailable: " + snapshot.Err
		}
	}
	return group
}

func systemCheckNeedsDiagnostics(status systemCheckTone) bool {
	return status == systemCheckWarn || status == systemCheckDanger
}

type systemCheckLogSource struct {
	kind string
	name string
}

type systemCheckLogSnapshot struct {
	Source string
	Lines  []string
	Err    string
}

func (s *Server) systemCheckLogSource(ctx context.Context, groupID string, item systemCheckItem) systemCheckLogSource {
	switch groupID {
	case "app":
		switch item.ID {
		case "manager_service":
			return systemCheckLogSource{kind: "journal", name: "lightningos-manager"}
		case "reports_timer":
			return systemCheckLogSource{kind: "journal", name: "lightningos-reports"}
		case "terminal_service":
			return systemCheckLogSource{kind: "journal", name: "lightningos-terminal"}
		}
	case "lnd":
		return systemCheckLogSource{kind: "journal", name: "lnd"}
	case "bitcoin":
		return systemCheckLogSource{kind: "bitcoin", name: "bitcoin"}
	case "postgres":
		return systemCheckLogSource{kind: "journal", name: "postgresql"}
	case "tor":
		if service := firstSystemdLogUnit(ctx, []string{"tor@default", "tor"}); service != "" {
			return systemCheckLogSource{kind: "journal", name: service}
		}
	}
	return systemCheckLogSource{}
}

func (s *Server) readSystemCheckLogSnapshot(ctx context.Context, source systemCheckLogSource) systemCheckLogSnapshot {
	logCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	var (
		lines     []string
		sourceID  string
		readError error
	)
	switch source.kind {
	case "bitcoin":
		lines, sourceID, readError = s.readBitcoinLocalLogLines(logCtx, systemCheckLogLines, "")
	case "journal":
		lines, readError = system.JournalTailSince(logCtx, source.name, systemCheckLogLines, "")
		sourceID = "systemd:" + source.name
	default:
		return systemCheckLogSnapshot{}
	}
	if readError != nil {
		return systemCheckLogSnapshot{
			Source: sourceID,
			Err:    readError.Error(),
		}
	}
	return systemCheckLogSnapshot{
		Source: sourceID,
		Lines:  redactSystemCheckLogLines(lines),
	}
}

func firstSystemdLogUnit(ctx context.Context, units []string) string {
	if unit, ok := firstActiveSystemdUnit(ctx, units); ok {
		return unit
	}
	for _, unit := range units {
		if systemdUnitLoaded(ctx, unit) {
			return unit
		}
	}
	return ""
}

func redactSystemCheckLogLines(lines []string) []string {
	redacted := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = systemCheckURLPasswordPattern.ReplaceAllString(line, `://$1:[redacted]@`)
		line = systemCheckKeyValueSecretPattern.ReplaceAllString(line, `$1$3[redacted]`)
		line = systemCheckBasicAuthPattern.ReplaceAllString(line, `Basic [redacted]`)
		redacted = append(redacted, line)
	}
	return redacted
}
