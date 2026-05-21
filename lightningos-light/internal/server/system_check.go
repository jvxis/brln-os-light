package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
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
	ID     string          `json:"id"`
	Label  string          `json:"label"`
	Status systemCheckTone `json:"status"`
	Detail string          `json:"detail,omitempty"`
	Value  any             `json:"value,omitempty"`
}

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

	return newSystemCheckGroup("app", "App", items)
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
		rpcTone = systemCheckDanger
		rpcDetail = lndStatusMessage(err)
		if isTimeoutError(err) && s.lndWarmupActive() {
			rpcTone = systemCheckWarn
			rpcDetail = "warming up after restart"
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

	return newSystemCheckGroup("lnd", "LND", items)
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

	return newSystemCheckGroup("bitcoin", "Bitcoin", items)
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

	return newSystemCheckGroup("postgres", "Postgres", items)
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

	return newSystemCheckGroup("tor", "Tor", items)
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
