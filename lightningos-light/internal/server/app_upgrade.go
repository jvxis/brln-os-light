package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lightningos-light/internal/privileged"
	"lightningos-light/internal/system"
)

const (
	appUpgradeUnitName  = "lightningos-app-upgrade"
	appReleaseCachePath = "/var/lib/lightningos/app-release.json"
	appReleaseCacheTTL  = 24 * time.Hour
)

var (
	appVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z\.-]*)?$`)
	appCommitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

//go:embed assets/upgrade-app.sh
var embeddedAppUpgradeScript string

type appReleaseInfo struct {
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	Channel     string `json:"channel"`
	ReleasePage string `json:"release_page"`
	CheckedAt   string `json:"checked_at"`
	Commit      string `json:"commit"`
	Repository  string `json:"repository,omitempty"`
}

type appReleaseCache struct {
	CheckedAt string         `json:"checked_at"`
	Info      appReleaseInfo `json:"info"`
	Catalog   string         `json:"catalog,omitempty"`
}

type appUpgradeStatusResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	LatestTag       string `json:"latest_tag"`
	LatestChannel   string `json:"latest_channel"`
	ReleasePage     string `json:"release_page"`
	CheckedAt       string `json:"checked_at"`
	UpdateAvailable bool   `json:"update_available"`
	Running         bool   `json:"running"`
	Error           string `json:"error,omitempty"`
}

type appUpgradeStartRequest struct {
	TargetVersion string `json:"target_version"`
}

type appGHRelease struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Immutable  bool   `json:"immutable"`
	HtmlURL    string `json:"html_url"`
}

type appGHCommit struct {
	SHA string `json:"sha"`
}

func (s *Server) startAppUpgradeChecker() {
	go func() {
		refresh := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := getAppReleaseInfo(ctx, true, currentAppVersion(s.cfg.UI.StaticDir)); err != nil && s.logger != nil {
				s.logger.Printf("app upgrade check failed: %v", err)
			}
		}

		refresh()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			refresh()
		}
	}()
}

func (s *Server) handleAppUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	currentVersion := currentAppVersion(s.cfg.UI.StaticDir)
	currentComparable := normalizeAppVersion(currentVersion)
	currentDisplay := currentComparable
	if currentDisplay == "" {
		currentDisplay = strings.TrimSpace(currentVersion)
	}
	running := appUpgradeRunning(ctx)
	info, err := getAppReleaseInfo(ctx, force, currentVersion)

	resp := appUpgradeStatusResponse{
		CurrentVersion: currentDisplay,
		LatestVersion:  info.Version,
		LatestTag:      info.Tag,
		LatestChannel:  info.Channel,
		ReleasePage:    info.ReleasePage,
		CheckedAt:      info.CheckedAt,
		Running:        running,
	}
	if err != nil {
		resp.Error = err.Error()
	}

	if info.Version != "" {
		if currentComparable == "" {
			resp.UpdateAvailable = true
		} else if isSemverNewer(currentComparable, info.Version) {
			resp.UpdateAvailable = true
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAppUpgradeStart(w http.ResponseWriter, r *http.Request) {
	var req appUpgradeStartRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if appUpgradeRunning(ctx) {
		writeError(w, http.StatusConflict, "upgrade already running")
		return
	}

	info, err := getAppReleaseInfo(ctx, true, currentAppVersion(s.cfg.UI.StaticDir))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to resolve latest release: %v", err))
		return
	}
	if info.Version == "" || info.Tag == "" || info.Commit == "" {
		writeError(w, http.StatusBadGateway, "latest release metadata is incomplete")
		return
	}

	requested := normalizeAppVersion(req.TargetVersion)
	if requested != "" && requested != info.Version {
		writeError(w, http.StatusBadRequest, "target_version must match latest release")
		return
	}

	currentVersion := normalizeAppVersion(currentAppVersion(s.cfg.UI.StaticDir))
	if currentVersion != "" && !isSemverNewer(currentVersion, info.Version) {
		writeError(w, http.StatusConflict, "no newer version available")
		return
	}

	if handled, unit, err := system.StartLightningOSUpgradeWithBroker(ctx, info.Version, info.Tag, info.Commit, embeddedAppUpgradeScript, false); handled {
		if err != nil {
			if s.logger != nil {
				s.logger.Printf("app upgrade start failed: %v", err)
			}
			writeAppUpgradeStartError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"unit":           unit,
			"target_version": info.Version,
		})
		return
	}

	writeAppUpgradeStartError(w, privileged.ErrBrokerUnavailable)
}

func writeAppUpgradeStartError(w http.ResponseWriter, err error) {
	if errors.Is(err, privileged.ErrBrokerUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "Privileged upgrade service is unavailable. The upgrade did not start. Recover the broker using docs/UPGRADE_RECOVERY.md, then retry; reinstalling LOS is not required.")
		return
	}
	writeError(w, http.StatusInternalServerError, "Failed to start app upgrade. Check the lightningos-manager service journal; the upgrade log may be empty because the upgrade did not start.")
}

func currentAppVersion(staticDir string) string {
	raw, err := os.ReadFile(filepath.Join(staticDir, "version.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func appUpgradeRunning(ctx context.Context) bool {
	if legacyTransitionUnitRunning(ctx) {
		return true
	}
	return transientSystemdUnitRunning(ctx, appUpgradeUnitName)
}

func transientSystemdUnitRunning(ctx context.Context, unit string) bool {
	out, err := system.RunCommand(ctx, "systemctl", transientSystemdUnitListArgs(unit)...)
	if err != nil {
		return false
	}
	return transientSystemdUnitListed(out, unit)
}

func transientSystemdUnitListArgs(unit string) []string {
	return []string{
		"list-units",
		"--type=service",
		"--state=active,activating,reloading,deactivating",
		"--no-legend",
		"--no-pager",
		"--plain",
		unit + ".service",
	}
}

func transientSystemdUnitListed(output, unit string) bool {
	wanted := unit + ".service"
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == wanted {
			return true
		}
	}
	return false
}

func normalizeAppVersion(value string) string {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return ""
	}
	if normalized[0] == 'v' || normalized[0] == 'V' {
		normalized = normalized[1:]
	}
	if normalized == "" {
		return ""
	}
	normalized = strings.ReplaceAll(normalized, "_", "-")
	parts := strings.Fields(normalized)
	normalized = strings.Join(parts, "-")
	for strings.Contains(normalized, "--") {
		normalized = strings.ReplaceAll(normalized, "--", "-")
	}
	normalized = strings.Trim(normalized, "-")
	normalized = strings.ToLower(normalized)
	if appVersionPattern.MatchString(normalized) {
		return normalized
	}
	return ""
}

func getAppReleaseInfo(ctx context.Context, force bool, currentVersion string) (appReleaseInfo, error) {
	catalog := appCatalogForVersion(currentVersion)
	cached, checkedAt, ok := readAppReleaseCache(catalog)
	if ok && !force && time.Since(checkedAt) < appReleaseCacheTTL {
		return cached, nil
	}

	info, err := fetchAppReleaseForVersion(ctx, currentVersion)
	if err != nil {
		if ok {
			return cached, err
		}
		return appReleaseInfo{}, err
	}

	_ = writeAppReleaseCache(info, catalog)
	return info, nil
}

func appReleaseTagMatchesVersion(tag, version string) bool {
	candidate := strings.TrimSpace(tag)
	candidate = strings.TrimPrefix(strings.TrimPrefix(candidate, "v"), "V")
	if !appVersionPattern.MatchString(strings.ToLower(candidate)) {
		return false
	}
	return strings.EqualFold(candidate, version)
}

func readAppReleaseCache(catalog string) (appReleaseInfo, time.Time, bool) {
	data, err := os.ReadFile(appReleaseCachePath)
	if err != nil {
		return appReleaseInfo{}, time.Time{}, false
	}
	var cache appReleaseCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return appReleaseInfo{}, time.Time{}, false
	}
	if cache.Catalog != catalog || !appCatalogAllowsRelease(cache.Info.Repository, cache.Info.Version) {
		return appReleaseInfo{}, time.Time{}, false
	}
	checkedAt, err := time.Parse(time.RFC3339, cache.CheckedAt)
	if err != nil {
		return appReleaseInfo{}, time.Time{}, false
	}
	cache.Info.CheckedAt = cache.CheckedAt
	return cache.Info, checkedAt, true
}

func writeAppReleaseCache(info appReleaseInfo, catalog string) error {
	if info.CheckedAt == "" {
		info.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	}
	cache := appReleaseCache{
		CheckedAt: info.CheckedAt,
		Info:      info,
		Catalog:   catalog,
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(appReleaseCachePath), 0750); err != nil {
		return err
	}
	return os.WriteFile(appReleaseCachePath, data, 0640)
}
