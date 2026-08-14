package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

const (
	appUpgradeUnitName  = "lightningos-app-upgrade"
	appReleaseCachePath = "/var/lib/lightningos/app-release.json"
	appReleaseCacheTTL  = 24 * time.Hour
	appReleaseAPIURL    = "https://api.github.com/repos/jvxis/brln-os-light/releases?per_page=10"
	appCommitAPIBaseURL = "https://api.github.com/repos/jvxis/brln-os-light/commits/"
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
}

type appReleaseCache struct {
	CheckedAt string         `json:"checked_at"`
	Info      appReleaseInfo `json:"info"`
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
			if _, err := getAppReleaseInfo(ctx, true); err != nil && s.logger != nil {
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
	info, err := getAppReleaseInfo(ctx, force)

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

	info, err := getAppReleaseInfo(ctx, true)
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
			writeError(w, http.StatusInternalServerError, "failed to start app upgrade (check logs)")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"unit":           unit,
			"target_version": info.Version,
		})
		return
	}

	writeError(w, http.StatusServiceUnavailable, "privileged broker is required for LightningOS upgrades")
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
	out, _ := system.RunCommand(ctx, "systemctl", "is-active", appUpgradeUnitName)
	state := strings.TrimSpace(out)
	return state == "active" || state == "activating"
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

func getAppReleaseInfo(ctx context.Context, force bool) (appReleaseInfo, error) {
	cached, checkedAt, ok := readAppReleaseCache()
	if ok && !force && time.Since(checkedAt) < appReleaseCacheTTL {
		return cached, nil
	}

	info, err := fetchLatestAppRelease(ctx)
	if err != nil {
		if ok {
			return cached, err
		}
		return appReleaseInfo{}, err
	}

	_ = writeAppReleaseCache(info)
	return info, nil
}

func fetchLatestAppRelease(ctx context.Context) (appReleaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appReleaseAPIURL, nil)
	if err != nil {
		return appReleaseInfo{}, err
	}
	req.Header.Set("User-Agent", "lightningos-light")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return appReleaseInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return appReleaseInfo{}, fmt.Errorf("github api returned %s", resp.Status)
	}

	var releases []appGHRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return appReleaseInfo{}, err
	}

	for _, rel := range releases {
		if rel.Draft {
			continue
		}
		version := normalizeAppVersion(rel.TagName)
		if version == "" {
			version = normalizeAppVersion(rel.Name)
		}
		if version == "" {
			continue
		}
		channel := "stable"
		if rel.Prerelease || strings.Contains(strings.ToLower(version), "beta") || strings.Contains(strings.ToLower(version), "rc") {
			channel = "beta"
		}
		tag := strings.TrimSpace(rel.TagName)
		if tag == "" {
			tag = version
		}
		if !appReleaseTagMatchesVersion(tag, version) {
			continue
		}
		commit, err := fetchAppReleaseCommit(ctx, tag)
		if err != nil {
			return appReleaseInfo{}, fmt.Errorf("failed to resolve release commit: %w", err)
		}
		return appReleaseInfo{
			Version:     version,
			Tag:         tag,
			Channel:     channel,
			ReleasePage: rel.HtmlURL,
			CheckedAt:   time.Now().UTC().Format(time.RFC3339),
			Commit:      commit,
		}, nil
	}

	return appReleaseInfo{}, errors.New("no suitable app release found")
}

func appReleaseTagMatchesVersion(tag, version string) bool {
	candidate := strings.TrimSpace(tag)
	candidate = strings.TrimPrefix(strings.TrimPrefix(candidate, "v"), "V")
	if !appVersionPattern.MatchString(strings.ToLower(candidate)) {
		return false
	}
	return strings.EqualFold(candidate, version)
}

func fetchAppReleaseCommit(ctx context.Context, tag string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appCommitAPIBaseURL+url.PathEscape(tag), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "lightningos-light")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github commit api returned %s", resp.Status)
	}
	var commit appGHCommit
	if err := json.NewDecoder(resp.Body).Decode(&commit); err != nil {
		return "", err
	}
	commit.SHA = strings.ToLower(strings.TrimSpace(commit.SHA))
	if !appCommitPattern.MatchString(commit.SHA) {
		return "", errors.New("github commit api returned an invalid commit")
	}
	return commit.SHA, nil
}

func readAppReleaseCache() (appReleaseInfo, time.Time, bool) {
	data, err := os.ReadFile(appReleaseCachePath)
	if err != nil {
		return appReleaseInfo{}, time.Time{}, false
	}
	var cache appReleaseCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return appReleaseInfo{}, time.Time{}, false
	}
	checkedAt, err := time.Parse(time.RFC3339, cache.CheckedAt)
	if err != nil {
		return appReleaseInfo{}, time.Time{}, false
	}
	cache.Info.CheckedAt = cache.CheckedAt
	return cache.Info, checkedAt, true
}

func writeAppReleaseCache(info appReleaseInfo) error {
	if info.CheckedAt == "" {
		info.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	}
	cache := appReleaseCache{
		CheckedAt: info.CheckedAt,
		Info:      info,
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
