package server

import (
	"strings"
	"testing"
	"time"

	sysinfo "lightningos-light/internal/system"
)

func ramSample(pct float64) resourceAlertSample {
	return resourceAlertSample{Key: "ram", Label: "RAM", Percent: pct, Detail: "x", WarnPct: 90, CritPct: 95, Confirm: 2}
}

func diskSample(pct float64) resourceAlertSample {
	return resourceAlertSample{Key: "disk:/", Label: "Disk /", Percent: pct, Detail: "x", WarnPct: 85, CritPct: 95, Confirm: 1}
}

func TestResourceAlertsConfirmEscalateRecover(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	step := func(state map[string]resourceAlertState, s resourceAlertSample) (map[string]resourceAlertState, []string) {
		now = now.Add(5 * time.Minute)
		return evaluateResourceAlerts(state, []resourceAlertSample{s}, now)
	}
	state := map[string]resourceAlertState{}

	// A single RAM peak is not an alert; two checks in a row are.
	state, lines := step(state, ramSample(93))
	if len(lines) != 0 {
		t.Fatalf("one sample above 90%% must wait for confirmation: %v", lines)
	}
	state, lines = step(state, ramSample(80))
	if len(lines) != 0 || state["ram"].Pending != 0 {
		t.Fatalf("a peak that went away resets the confirmation: %v %+v", lines, state["ram"])
	}
	state, _ = step(state, ramSample(92))
	state, lines = step(state, ramSample(93))
	if len(lines) != 1 || !strings.Contains(lines[0], "warning") {
		t.Fatalf("RAM above 90%% for two checks must alert: %v", lines)
	}
	// Same level: no repeat until the reminder.
	state, lines = step(state, ramSample(93))
	if len(lines) != 0 {
		t.Fatalf("no repeat at the same level: %v", lines)
	}
	// Escalation to critical also needs two checks.
	state, _ = step(state, ramSample(97))
	state, lines = step(state, ramSample(98))
	if len(lines) != 1 || !strings.Contains(lines[0], "critical") {
		t.Fatalf("critical after two checks above 95%%: %v", lines)
	}
	// Hysteresis: 91% is within 5 points of the critical line, stays critical
	// silently; 88% drops to warning silently; 86% is still within 5 of 90.
	for _, pct := range []float64{91, 88, 86} {
		state, lines = step(state, ramSample(pct))
		if len(lines) != 0 {
			t.Fatalf("%v%%: no message while still high: %v", pct, lines)
		}
	}
	if state["ram"].Level != resourceAlertLevelWarn {
		t.Fatalf("88%% and 86%% are warning, got %+v", state["ram"])
	}
	state, lines = step(state, ramSample(70))
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "✅ RAM back to 70%") {
		t.Fatalf("recovery message: %v", lines)
	}
	if state["ram"].Level != resourceAlertLevelOK {
		t.Fatalf("recovered: %+v", state["ram"])
	}
}

func TestResourceAlertsDiskAndReminder(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	state, lines := evaluateResourceAlerts(nil, []resourceAlertSample{diskSample(96)}, now)
	if len(lines) != 1 || !strings.Contains(lines[0], "🔴 Disk / 96%") {
		t.Fatalf("a full disk alerts on the first check, straight at critical: %v", lines)
	}
	state, lines = evaluateResourceAlerts(state, []resourceAlertSample{diskSample(96)}, now.Add(23*time.Hour))
	if len(lines) != 0 {
		t.Fatalf("no reminder before 24h: %v", lines)
	}
	state, lines = evaluateResourceAlerts(state, []resourceAlertSample{diskSample(96)}, now.Add(24*time.Hour))
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "still") {
		t.Fatalf("daily reminder while still critical: %v", lines)
	}
	// A disk that disappears from the samples is forgotten without a message.
	state, lines = evaluateResourceAlerts(state, nil, now.Add(25*time.Hour))
	if len(lines) != 0 || len(state) != 0 {
		t.Fatalf("unmounted disk: %v %+v", lines, state)
	}
}

func TestResourceAlertSamplesSkipFullByDesignMounts(t *testing.T) {
	stats := sysinfo.SystemStats{
		RAMTotalMB: 8000,
		RAMUsedMB:  7600,
		Disk: []sysinfo.DiskUsage{
			{Mount: "/", TotalGB: 200, UsedGB: 180, UsedPercent: 90},
			{Mount: "/data", TotalGB: 2000, UsedGB: 1000, UsedPercent: 50},
			{Mount: "/snap/core22/1612", TotalGB: 0.07, UsedGB: 0.07, UsedPercent: 100},
			{Mount: "/snap/lxd/29351", TotalGB: 1.2, UsedGB: 1.2, UsedPercent: 100},
			{Mount: "/boot/efi", TotalGB: 1, UsedGB: 0.9, UsedPercent: 90},
			{Mount: "/var/lib/docker/overlay2/abc/merged", TotalGB: 200, UsedGB: 180, UsedPercent: 90},
			{Mount: "/", TotalGB: 200, UsedGB: 180, UsedPercent: 90},
		},
	}
	samples := resourceAlertSamples(stats)
	var keys []string
	for _, s := range samples {
		keys = append(keys, s.Key)
	}
	if strings.Join(keys, ",") != "ram,disk:/,disk:/data" {
		t.Fatalf("watched resources: %v", keys)
	}
	if samples[0].Percent != 95 || samples[0].Confirm != 2 {
		t.Fatalf("RAM sample: %+v", samples[0])
	}
}
