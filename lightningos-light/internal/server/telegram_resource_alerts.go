package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	sysinfo "lightningos-light/internal/system"
)

// Proactive RAM and disk alerts on Telegram. The system summary only shows
// the numbers when it is due; a node that runs out of RAM (LND gossip flood)
// or disk (Postgres bloat) stops long before the next summary is read.
const (
	resourceAlertCheckInterval = 5 * time.Minute
	resourceAlertReminder      = 24 * time.Hour
	// Back to normal only this many points below the threshold, so a value
	// sitting on the line does not alternate alert / recovered.
	resourceAlertHysteresisPct = 5.0

	resourceAlertRAMWarnPct  = 90.0
	resourceAlertRAMCritPct  = 95.0
	resourceAlertDiskWarnPct = 85.0
	resourceAlertDiskCritPct = 95.0
	// RAM must stay high for two checks (10 min): a short peak during a
	// compaction or a graph sync is not an alert. Disk does not swing back.
	resourceAlertRAMConfirmSamples  = 2
	resourceAlertDiskConfirmSamples = 1

	resourceAlertLevelOK   = 0
	resourceAlertLevelWarn = 1
	resourceAlertLevelCrit = 2
)

type resourceAlertSample struct {
	Key     string // "ram" or "disk:<mount>"
	Label   string
	Percent float64
	Detail  string
	WarnPct float64
	CritPct float64
	Confirm int
}

type resourceAlertState struct {
	Level        int
	PendingLevel int
	Pending      int
	LastSentAt   time.Time
}

func (s resourceAlertSample) threshold(level int) float64 {
	if level >= resourceAlertLevelCrit {
		return s.CritPct
	}
	return s.WarnPct
}

func (s resourceAlertSample) rawLevel() int {
	switch {
	case s.Percent >= s.CritPct:
		return resourceAlertLevelCrit
	case s.Percent >= s.WarnPct:
		return resourceAlertLevelWarn
	default:
		return resourceAlertLevelOK
	}
}

// evaluateResourceAlerts compares the samples with the previous state and
// returns the new state plus one line per event: a level reached (after
// Confirm samples), a reminder when still alerting after resourceAlertReminder,
// and a recovery when back below the warning threshold minus the hysteresis.
// A resource missing from the samples (unmounted disk) is forgotten silently.
func evaluateResourceAlerts(prev map[string]resourceAlertState, samples []resourceAlertSample, now time.Time) (map[string]resourceAlertState, []string) {
	next := make(map[string]resourceAlertState, len(samples))
	var lines []string
	for _, sample := range samples {
		st := prev[sample.Key]
		// Levels already reached are only left hysteresis points below their
		// line: critical at 91% stays critical, at 88% falls to warning.
		level := sample.rawLevel()
		for held := st.Level; held > level; held-- {
			if sample.Percent >= sample.threshold(held)-resourceAlertHysteresisPct {
				level = held
				break
			}
		}
		confirm := sample.Confirm
		if confirm < 1 {
			confirm = 1
		}
		switch {
		case level > st.Level:
			if st.PendingLevel == level {
				st.Pending++
			} else {
				st.PendingLevel = level
				st.Pending = 1
			}
			if st.Pending >= confirm {
				st.Level = level
				st.PendingLevel, st.Pending = 0, 0
				st.LastSentAt = now
				lines = append(lines, resourceAlertLine(sample, level))
			}
		case level < st.Level:
			st.PendingLevel, st.Pending = 0, 0
			st.Level = level
			if level == resourceAlertLevelOK {
				st.LastSentAt = time.Time{}
				lines = append(lines, fmt.Sprintf("✅ %s back to %s%% (%s)", sample.Label, formatFloat(sample.Percent, 1), sample.Detail))
			}
		default:
			st.PendingLevel, st.Pending = 0, 0
			if level > resourceAlertLevelOK && now.Sub(st.LastSentAt) >= resourceAlertReminder {
				st.LastSentAt = now
				lines = append(lines, resourceAlertLine(sample, level)+" — still")
			}
		}
		next[sample.Key] = st
	}
	return next, lines
}

func resourceAlertLine(sample resourceAlertSample, level int) string {
	icon, name := "🟠", "warning"
	if level >= resourceAlertLevelCrit {
		icon, name = "🔴", "critical"
	}
	return fmt.Sprintf("%s %s %s%% (%s) — %s ≥%s%%", icon, sample.Label, formatFloat(sample.Percent, 1), sample.Detail, name, formatFloat(sample.threshold(level), 0))
}

func resourceAlertSamples(stats sysinfo.SystemStats) []resourceAlertSample {
	var samples []resourceAlertSample
	if stats.RAMTotalMB > 0 {
		samples = append(samples, resourceAlertSample{
			Key:     "ram",
			Label:   "RAM",
			Percent: float64(stats.RAMUsedMB) / float64(stats.RAMTotalMB) * 100,
			Detail:  fmt.Sprintf("%s / %s MB", formatInt(stats.RAMUsedMB), formatInt(stats.RAMTotalMB)),
			WarnPct: resourceAlertRAMWarnPct,
			CritPct: resourceAlertRAMCritPct,
			Confirm: resourceAlertRAMConfirmSamples,
		})
	}
	seen := map[string]bool{}
	for _, disk := range stats.Disk {
		mount := strings.TrimSpace(disk.Mount)
		if !resourceAlertWatchMount(mount) || disk.TotalGB < 1 || seen[mount] {
			continue
		}
		seen[mount] = true
		samples = append(samples, resourceAlertSample{
			Key:     "disk:" + mount,
			Label:   "Disk " + mount,
			Percent: disk.UsedPercent,
			Detail:  fmt.Sprintf("%s / %s GB", formatFloat(disk.UsedGB, 1), formatFloat(disk.TotalGB, 1)),
			WarnPct: resourceAlertDiskWarnPct,
			CritPct: resourceAlertDiskCritPct,
			Confirm: resourceAlertDiskConfirmSamples,
		})
	}
	return samples
}

// resourceAlertWatchMount skips mounts that are full by design (snap
// squashfs images are always 100%), boot partitions and Docker overlays,
// which repeat the usage of the filesystem below them.
func resourceAlertWatchMount(mount string) bool {
	if mount == "" || !strings.HasPrefix(mount, "/") {
		return false
	}
	for _, prefix := range []string{"/snap", "/boot", "/run", "/dev", "/sys", "/proc", "/var/lib/docker", "/var/snap"} {
		if mount == prefix || strings.HasPrefix(mount, prefix+"/") {
			return false
		}
	}
	return true
}

func (s *Server) runTelegramResourceAlertLoop() {
	ticker := time.NewTicker(resourceAlertCheckInterval)
	defer ticker.Stop()
	// In memory: after a restart a condition that is still on is reported
	// once more, which is what an operator wants after a reboot anyway.
	state := map[string]resourceAlertState{}
	for {
		<-ticker.C
		if s == nil || s.notifier == nil {
			continue
		}
		cfg := readTelegramBackupConfig()
		if !cfg.configured() {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		settings, err := loadTelegramNotificationSettings(ctx, s.db)
		cancel()
		if err != nil {
			continue
		}
		if !settings.ResourceAlertsEnabled {
			state = map[string]resourceAlertState{}
			continue
		}

		statsCtx, statsCancel := context.WithTimeout(context.Background(), 10*time.Second)
		stats, err := sysinfo.GetSystemStats(statsCtx)
		statsCancel()
		if err != nil {
			continue
		}
		next, lines := evaluateResourceAlerts(state, resourceAlertSamples(stats), time.Now())
		if len(lines) == 0 {
			state = next
			continue
		}

		message := []string{"🚨 LightningOS — Resource alert"}
		aliasCtx, aliasCancel := context.WithTimeout(context.Background(), 5*time.Second)
		alias := strings.TrimSpace(getNodeAlias(aliasCtx, s.lnd))
		aliasCancel()
		if alias != "" {
			message = append(message, fmt.Sprintf("🔖 Node: %s", alias))
		}
		message = append(message, lines...)

		sendCtx, sendCancel := context.WithTimeout(context.Background(), 20*time.Second)
		sendErr := sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID, strings.Join(message, "\n"))
		sendCancel()
		if sendErr != nil {
			// Keep the old state: the same event is sent on the next check.
			if s.logger != nil {
				s.logger.Printf("notifications: telegram resource alert send failed: %v", sendErr)
			}
			continue
		}
		state = next
	}
}
