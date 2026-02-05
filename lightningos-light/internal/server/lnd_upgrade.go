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
  "strconv"
  "strings"
  "time"

  "lightningos-light/internal/system"
)

const (
  lndUpgradeUnitName = "lightningos-lnd-upgrade"
  lndUpgradeScriptPath = "/usr/local/sbin/lightningos-upgrade-lnd"
  lndReleaseCachePath = "/var/lib/lightningos/lnd-release.json"
  lndReleaseCacheTTL = 6 * time.Hour
  lndReleaseAPIURL = "https://api.github.com/repos/lightningnetwork/lnd/releases?per_page=5"
  lndDownloadTemplate = "https://github.com/lightningnetwork/lnd/releases/download/v%s/lnd-linux-amd64-v%s.tar.gz"
)

var lndVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z\.-]*)?$`)
var lndVersionExtract = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z\.-]*)?`)

//go:embed assets/upgrade-lnd.sh
var embeddedUpgradeScript string

type lndReleaseInfo struct {
  Version string `json:"version"`
  DownloadURL string `json:"download_url"`
  Channel string `json:"channel"`
  ReleasePage string `json:"release_page"`
  CheckedAt string `json:"checked_at"`
}

type lndReleaseCache struct {
  CheckedAt string `json:"checked_at"`
  Info lndReleaseInfo `json:"info"`
}

type lndUpgradeStatusResponse struct {
  CurrentVersion string `json:"current_version"`
  CurrentSource string `json:"current_source"`
  LatestVersion string `json:"latest_version"`
  LatestURL string `json:"latest_url"`
  LatestChannel string `json:"latest_channel"`
  ReleasePage string `json:"release_page"`
  CheckedAt string `json:"checked_at"`
  UpdateAvailable bool `json:"update_available"`
  Running bool `json:"running"`
  Error string `json:"error,omitempty"`
}

type lndUpgradeRequest struct {
  TargetVersion string `json:"target_version"`
  DownloadURL string `json:"download_url"`
}

type ghRelease struct {
  TagName string `json:"tag_name"`
  Prerelease bool `json:"prerelease"`
  HtmlURL string `json:"html_url"`
  PublishedAt string `json:"published_at"`
  Assets []ghAsset `json:"assets"`
}

type ghAsset struct {
  Name string `json:"name"`
  BrowserDownloadURL string `json:"browser_download_url"`
}

func (s *Server) handleLNDUpgradeStatus(w http.ResponseWriter, r *http.Request) {
  force := r.URL.Query().Get("force") == "1"
  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()

  currentVersion, currentSource := currentLndVersion(ctx)
  running := lndUpgradeRunning(ctx)
  info, err := getLndReleaseInfo(ctx, force)

  resp := lndUpgradeStatusResponse{
    CurrentVersion: currentVersion,
    CurrentSource: currentSource,
    LatestVersion: info.Version,
    LatestURL: info.DownloadURL,
    LatestChannel: info.Channel,
    ReleasePage: info.ReleasePage,
    CheckedAt: info.CheckedAt,
    Running: running,
  }

  if err != nil {
    resp.Error = err.Error()
  }

  if currentVersion != "" && info.Version != "" {
    if isSemverNewer(currentVersion, info.Version) {
      resp.UpdateAvailable = true
    }
  }

  writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLNDUpgradeStart(w http.ResponseWriter, r *http.Request) {
  var req lndUpgradeRequest
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  version := strings.TrimSpace(req.TargetVersion)
  version = strings.TrimPrefix(version, "v")
  if version == "" || !lndVersionPattern.MatchString(version) {
    writeError(w, http.StatusBadRequest, "invalid target_version")
    return
  }

  if _, err := os.Stat(lndUpgradeScriptPath); err != nil {
    // allow lazy install on demand
  }

  ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
  defer cancel()

  if lndUpgradeRunning(ctx) {
    writeError(w, http.StatusConflict, "upgrade already running")
    return
  }

  if err := ensureLndUpgradeScript(ctx); err != nil {
    if s.logger != nil {
      s.logger.Printf("failed to install lnd upgrade script: %v", err)
    }
    writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to prepare upgrade script: %v", err))
    return
  }

  downloadURL := strings.TrimSpace(req.DownloadURL)
  if downloadURL == "" {
    downloadURL = fmt.Sprintf(lndDownloadTemplate, version, version)
  } else if !isAllowedLndDownloadURL(downloadURL, version) {
    writeError(w, http.StatusBadRequest, "invalid download_url")
    return
  }

  args := []string{
    "--unit", lndUpgradeUnitName,
    "--collect",
    "--quiet",
    lndUpgradeScriptPath,
    "--version", version,
    "--url", downloadURL,
  }

  if out, err := system.RunCommandWithSudo(ctx, "systemd-run", args...); err != nil {
    if s.logger != nil {
      s.logger.Printf("lnd upgrade start failed: %v (%s)", err, strings.TrimSpace(out))
    }
    writeError(w, http.StatusInternalServerError, "failed to start upgrade (check logs)")
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{
    "ok": true,
    "unit": lndUpgradeUnitName,
  })
}

func currentLndVersion(ctx context.Context) (string, string) {
  candidates := []string{"/usr/local/bin/lnd", "lnd", "/usr/local/bin/lncli", "lncli"}
  for _, bin := range candidates {
    out, err := system.RunCommand(ctx, bin, "--version")
    if err != nil && out == "" {
      continue
    }
    version := parseLndVersion(out)
    if version != "" {
      return version, bin + " --version"
    }
  }
  return "", ""
}

func parseLndVersion(output string) string {
  match := lndVersionExtract.FindString(output)
  return strings.TrimSpace(match)
}

func isSemverNewer(current string, latest string) bool {
  cur := parseSemver(current)
  next := parseSemver(latest)
  if !cur.Valid || !next.Valid {
    return false
  }
  return compareSemver(cur, next) < 0
}

type parsedSemver struct {
  Major int
  Minor int
  Patch int
  Pre []string
  Valid bool
}

func parseSemver(value string) parsedSemver {
  trimmed := strings.TrimSpace(strings.TrimPrefix(value, "v"))
  if trimmed == "" {
    return parsedSemver{}
  }
  if !lndVersionPattern.MatchString(trimmed) {
    return parsedSemver{}
  }
  main := trimmed
  pre := ""
  if idx := strings.Index(trimmed, "-"); idx != -1 {
    main = trimmed[:idx]
    pre = trimmed[idx+1:]
  }
  parts := strings.Split(main, ".")
  if len(parts) != 3 {
    return parsedSemver{}
  }
  major, majorErr := atoi(parts[0])
  minor, minorErr := atoi(parts[1])
  patch, patchErr := atoi(parts[2])
  if majorErr != nil || minorErr != nil || patchErr != nil {
    return parsedSemver{}
  }
  parsed := parsedSemver{
    Major: major,
    Minor: minor,
    Patch: patch,
    Valid: true,
  }
  if pre != "" {
    parsed.Pre = strings.Split(pre, ".")
  }
  return parsed
}

func compareSemver(a parsedSemver, b parsedSemver) int {
  if a.Major != b.Major {
    return compareInts(a.Major, b.Major)
  }
  if a.Minor != b.Minor {
    return compareInts(a.Minor, b.Minor)
  }
  if a.Patch != b.Patch {
    return compareInts(a.Patch, b.Patch)
  }
  aHasPre := len(a.Pre) > 0
  bHasPre := len(b.Pre) > 0
  if !aHasPre && !bHasPre {
    return 0
  }
  if !aHasPre && bHasPre {
    return 1
  }
  if aHasPre && !bHasPre {
    return -1
  }
  max := len(a.Pre)
  if len(b.Pre) > max {
    max = len(b.Pre)
  }
  for i := 0; i < max; i++ {
    if i >= len(a.Pre) {
      return -1
    }
    if i >= len(b.Pre) {
      return 1
    }
    cmp := compareIdentifier(a.Pre[i], b.Pre[i])
    if cmp != 0 {
      return cmp
    }
  }
  return 0
}

func compareIdentifier(a string, b string) int {
  aNum, aErr := atoi(a)
  bNum, bErr := atoi(b)
  if aErr == nil && bErr == nil {
    return compareInts(aNum, bNum)
  }
  if aErr == nil && bErr != nil {
    return -1
  }
  if aErr != nil && bErr == nil {
    return 1
  }
  if a == b {
    return 0
  }
  if a < b {
    return -1
  }
  return 1
}

func compareInts(a int, b int) int {
  if a == b {
    return 0
  }
  if a < b {
    return -1
  }
  return 1
}

func atoi(value string) (int, error) {
  return strconv.Atoi(value)
}

func ensureLndUpgradeScript(ctx context.Context) error {
  if strings.TrimSpace(embeddedUpgradeScript) == "" {
    return errors.New("embedded upgrade script is empty")
  }
  existing, err := os.ReadFile(lndUpgradeScriptPath)
  if err == nil && string(existing) == embeddedUpgradeScript {
    return nil
  }

  tmpFile, err := os.CreateTemp("", "lightningos-upgrade-lnd-*.sh")
  if err != nil {
    return err
  }
  tmpPath := tmpFile.Name()
  if _, err := tmpFile.WriteString(embeddedUpgradeScript); err != nil {
    _ = tmpFile.Close()
    _ = os.Remove(tmpPath)
    return err
  }
  if err := tmpFile.Close(); err != nil {
    _ = os.Remove(tmpPath)
    return err
  }
  defer os.Remove(tmpPath)

  installCmd := fmt.Sprintf("mkdir -p %s && install -m 0755 %s %s", filepath.Dir(lndUpgradeScriptPath), tmpPath, lndUpgradeScriptPath)
  if _, err := runSystemd(ctx, "/bin/sh", "-c", installCmd); err != nil {
    return fmt.Errorf("systemd-run failed (check sudoers for lightningos): %w", err)
  }
  return nil
}

func lndUpgradeRunning(ctx context.Context) bool {
  out, err := system.RunCommand(ctx, "systemctl", "is-active", lndUpgradeUnitName)
  if err != nil {
    out, _ = system.RunCommandWithSudo(ctx, "systemctl", "is-active", lndUpgradeUnitName)
  }
  state := strings.TrimSpace(out)
  return state == "active" || state == "activating"
}

func getLndReleaseInfo(ctx context.Context, force bool) (lndReleaseInfo, error) {
  cached, checkedAt, ok := readLndReleaseCache()
  if ok && !force && time.Since(checkedAt) < lndReleaseCacheTTL {
    return cached, nil
  }

  info, err := fetchLatestLndRelease(ctx)
  if err != nil {
    if ok {
      return cached, err
    }
    return lndReleaseInfo{}, err
  }

  _ = writeLndReleaseCache(info)
  return info, nil
}

func fetchLatestLndRelease(ctx context.Context) (lndReleaseInfo, error) {
  req, err := http.NewRequestWithContext(ctx, http.MethodGet, lndReleaseAPIURL, nil)
  if err != nil {
    return lndReleaseInfo{}, err
  }
  req.Header.Set("User-Agent", "lightningos-light")
  client := &http.Client{Timeout: 6 * time.Second}
  resp, err := client.Do(req)
  if err != nil {
    return lndReleaseInfo{}, err
  }
  defer resp.Body.Close()

  if resp.StatusCode < 200 || resp.StatusCode >= 300 {
    return lndReleaseInfo{}, fmt.Errorf("github api returned %s", resp.Status)
  }

  var releases []ghRelease
  if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
    return lndReleaseInfo{}, err
  }

  for _, rel := range releases {
    version := strings.TrimSpace(strings.TrimPrefix(rel.TagName, "v"))
    if version == "" {
      continue
    }
    if !lndVersionPattern.MatchString(version) {
      continue
    }
    assetName := fmt.Sprintf("lnd-linux-amd64-v%s.tar.gz", version)
    downloadURL := ""
    for _, asset := range rel.Assets {
      if asset.Name == assetName && asset.BrowserDownloadURL != "" {
        downloadURL = asset.BrowserDownloadURL
        break
      }
    }
    if downloadURL == "" {
      downloadURL = fmt.Sprintf(lndDownloadTemplate, version, version)
    }
    channel := "stable"
    if rel.Prerelease {
      channel = "beta"
    }
    return lndReleaseInfo{
      Version: version,
      DownloadURL: downloadURL,
      Channel: channel,
      ReleasePage: rel.HtmlURL,
      CheckedAt: time.Now().UTC().Format(time.RFC3339),
    }, nil
  }

  return lndReleaseInfo{}, errors.New("no suitable LND release found")
}

func readLndReleaseCache() (lndReleaseInfo, time.Time, bool) {
  data, err := os.ReadFile(lndReleaseCachePath)
  if err != nil {
    return lndReleaseInfo{}, time.Time{}, false
  }
  var cache lndReleaseCache
  if err := json.Unmarshal(data, &cache); err != nil {
    return lndReleaseInfo{}, time.Time{}, false
  }
  checkedAt, err := time.Parse(time.RFC3339, cache.CheckedAt)
  if err != nil {
    return lndReleaseInfo{}, time.Time{}, false
  }
  cache.Info.CheckedAt = cache.CheckedAt
  return cache.Info, checkedAt, true
}

func writeLndReleaseCache(info lndReleaseInfo) error {
  if info.CheckedAt == "" {
    info.CheckedAt = time.Now().UTC().Format(time.RFC3339)
  }
  cache := lndReleaseCache{
    CheckedAt: info.CheckedAt,
    Info: info,
  }
  data, err := json.Marshal(cache)
  if err != nil {
    return err
  }
  if err := os.MkdirAll(filepath.Dir(lndReleaseCachePath), 0750); err != nil {
    return err
  }
  return os.WriteFile(lndReleaseCachePath, data, 0640)
}

func isAllowedLndDownloadURL(value string, version string) bool {
  parsed, err := url.Parse(value)
  if err != nil {
    return false
  }
  if parsed.Scheme != "https" {
    return false
  }
  host := strings.ToLower(parsed.Host)
  if host != "github.com" {
    return false
  }
  if !strings.Contains(parsed.Path, "/lightningnetwork/lnd/releases/download/") {
    return false
  }
  expectedFile := fmt.Sprintf("lnd-linux-amd64-v%s.tar.gz", version)
  if !strings.HasSuffix(parsed.Path, expectedFile) {
    return false
  }
  if !strings.Contains(parsed.Path, "/v"+version+"/") {
    return false
  }
  return true
}
