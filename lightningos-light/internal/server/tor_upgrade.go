package server

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

const (
	torUpgradeUnitName   = "lightningos-tor-upgrade"
	torUpgradeScriptPath = "/usr/local/sbin/lightningos-check-tor-update"
)

var torVersionPattern = regexp.MustCompile(`([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)`)

//go:embed assets/check-tor-update.sh
var embeddedTorUpgradeScript string

type torUpgradeStatusResponse struct {
	Version                 string `json:"version,omitempty"`
	InstalledPackageVersion string `json:"installed_package_version,omitempty"`
	CandidateVersion        string `json:"candidate_version,omitempty"`
	CandidatePackageVersion string `json:"candidate_package_version,omitempty"`
	RepositoryOfficial      bool   `json:"repository_official"`
	ServiceUnit             string `json:"service_unit,omitempty"`
	ServiceStatus           string `json:"service_status"`
	ServiceActive           bool   `json:"service_active"`
	SocksReady              bool   `json:"socks_ready"`
	ControlReady            bool   `json:"control_ready"`
	UpdateAvailable         bool   `json:"update_available"`
	CanUpdate               bool   `json:"can_update"`
	Running                 bool   `json:"running"`
	CheckedAt               string `json:"checked_at"`
	Error                   string `json:"error,omitempty"`
}

func (s *Server) handleTorUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1"
	timeout := 8 * time.Second
	if force {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	var refreshErr error
	if force && !torUpgradeRunning(ctx) {
		if out, err := system.RunCommandWithSudo(ctx, "apt-get", "update"); err != nil {
			refreshErr = fmt.Errorf("failed to refresh APT metadata: %s", commandErrorDetail(out, err))
		}
	}

	resp := torUpgradeStatus(ctx)
	if refreshErr != nil {
		resp.Error = refreshErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTorUpgradeStart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if torUpgradeRunning(ctx) {
		writeError(w, http.StatusConflict, "Tor update already running")
		return
	}

	status := torUpgradeStatus(ctx)
	if status.RepositoryOfficial && !status.UpdateAvailable {
		writeError(w, http.StatusConflict, "no newer Tor version available")
		return
	}
	if !status.CanUpdate {
		writeError(w, http.StatusConflict, "Tor update is unavailable")
		return
	}

	if err := ensureTorUpgradeScript(ctx); err != nil {
		if s.logger != nil {
			s.logger.Printf("failed to install Tor update script: %v", err)
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to prepare Tor update script: %v", err))
		return
	}

	args := []string{
		"--unit", torUpgradeUnitName,
		"--collect",
		"--quiet",
		"--",
		torUpgradeScriptPath,
		"--yes",
		"--configure-repo",
		"--restart",
	}
	if out, err := system.RunCommandWithSudo(ctx, "systemd-run", args...); err != nil {
		details := commandErrorDetail(out, err)
		if s.logger != nil {
			s.logger.Printf("Tor update start failed: %s", details)
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to start Tor update: %s", details))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"unit":           torUpgradeUnitName,
		"target_version": status.CandidateVersion,
	})
}

func torUpgradeStatus(ctx context.Context) torUpgradeStatusResponse {
	resp := torUpgradeStatusResponse{
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
		Running:       torUpgradeRunning(ctx),
		ServiceStatus: "unavailable",
	}

	installedOut, installedErr := system.RunCommand(ctx, "dpkg-query", "-W", "-f=${db:Status-Abbrev}\t${Version}", "tor")
	if installedErr == nil {
		fields := strings.Fields(installedOut)
		if len(fields) >= 2 && fields[0] == "ii" {
			resp.InstalledPackageVersion = fields[len(fields)-1]
		}
	}

	versionOut, versionErr := system.RunCommand(ctx, "tor", "--version")
	if versionErr == nil {
		resp.Version = extractTorVersion(versionOut)
	}
	if resp.Version == "" {
		resp.Version = extractTorVersion(resp.InstalledPackageVersion)
	}

	// apt-cache localizes field names such as "Candidate". Keep its output
	// stable so candidate detection does not depend on the host locale.
	policyOut, policyErr := system.RunCommand(ctx, "env", "LC_ALL=C", "LANG=C", "apt-cache", "policy", "tor")
	if policyErr == nil {
		resp.CandidatePackageVersion = aptPolicyCandidate(policyOut)
		resp.CandidateVersion = extractTorVersion(resp.CandidatePackageVersion)
		resp.RepositoryOfficial = strings.Contains(policyOut, "deb.torproject.org/torproject.org")
	}

	resp.UpdateAvailable = torVersionNewer(resp.Version, resp.CandidateVersion)
	if resp.InstalledPackageVersion != "" && resp.CandidatePackageVersion != "" {
		_, compareErr := system.RunCommand(
			ctx,
			"dpkg",
			"--compare-versions",
			resp.InstalledPackageVersion,
			"lt",
			resp.CandidatePackageVersion,
		)
		resp.UpdateAvailable = compareErr == nil
	}
	resp.CanUpdate = resp.CandidatePackageVersion != "" && (resp.UpdateAvailable || !resp.RepositoryOfficial)

	resp.ServiceUnit, resp.ServiceActive = firstActiveSystemdUnit(ctx, []string{"tor@default", "tor"})
	if resp.ServiceUnit == "" {
		resp.ServiceUnit = firstExistingTorUnit(ctx)
	}
	resp.SocksReady = testTCP("127.0.0.1:9050")
	resp.ControlReady = testTCP("127.0.0.1:9051")
	switch {
	case resp.ServiceActive && resp.SocksReady && resp.ControlReady:
		resp.ServiceStatus = "healthy"
	case resp.ServiceActive:
		resp.ServiceStatus = "degraded"
	case resp.ServiceUnit != "":
		resp.ServiceStatus = "inactive"
	}

	errorsFound := make([]string, 0, 2)
	if installedErr != nil && versionErr != nil {
		errorsFound = append(errorsFound, "Tor is not installed or its version could not be read")
	}
	if policyErr != nil {
		errorsFound = append(errorsFound, "APT candidate could not be resolved")
	} else if resp.CandidatePackageVersion == "" {
		errorsFound = append(errorsFound, "APT did not provide a Tor candidate; refresh package metadata and verify the Tor repository")
	}
	resp.Error = strings.Join(errorsFound, "; ")
	return resp
}

func aptPolicyCandidate(policy string) string {
	for _, line := range strings.Split(policy, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(strings.TrimSuffix(fields[0], ":"), "candidate") {
			if fields[1] == "(none)" {
				return ""
			}
			return fields[1]
		}
	}
	return ""
}

func extractTorVersion(value string) string {
	match := torVersionPattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func torVersionNewer(current, candidate string) bool {
	currentParts, currentOK := parseTorVersion(current)
	candidateParts, candidateOK := parseTorVersion(candidate)
	if !candidateOK {
		return false
	}
	if !currentOK {
		return true
	}
	for i := range currentParts {
		if candidateParts[i] != currentParts[i] {
			return candidateParts[i] > currentParts[i]
		}
	}
	return false
}

func parseTorVersion(value string) ([4]int, bool) {
	var parts [4]int
	match := extractTorVersion(value)
	if match == "" {
		return parts, false
	}
	for i, raw := range strings.Split(match, ".") {
		part, err := strconv.Atoi(raw)
		if err != nil {
			return [4]int{}, false
		}
		parts[i] = part
	}
	return parts, true
}

func firstExistingTorUnit(ctx context.Context) string {
	for _, unit := range []string{"tor@default", "tor"} {
		out, _ := system.RunCommand(ctx, "systemctl", "list-unit-files", unit+".service", "--no-legend")
		if strings.HasPrefix(strings.TrimSpace(out), unit+".service") {
			return unit
		}
	}
	return ""
}

func torUpgradeRunning(ctx context.Context) bool {
	out, err := system.RunCommand(ctx, "systemctl", "is-active", torUpgradeUnitName)
	if err != nil {
		out, _ = system.RunCommandWithSudo(ctx, "systemctl", "is-active", torUpgradeUnitName)
	}
	state := strings.TrimSpace(out)
	return state == "active" || state == "activating"
}

func ensureTorUpgradeScript(ctx context.Context) error {
	if strings.TrimSpace(embeddedTorUpgradeScript) == "" {
		return errors.New("embedded Tor update script is empty")
	}

	existing, err := os.ReadFile(torUpgradeScriptPath)
	if err == nil && string(existing) == embeddedTorUpgradeScript {
		return nil
	}

	stageDir := "/var/lib/lightningos"
	if err := os.MkdirAll(stageDir, 0750); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(stageDir, "lightningos-check-tor-update-*.sh")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.WriteString(embeddedTorUpgradeScript); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	defer os.Remove(tmpPath)

	installCmd := fmt.Sprintf("mkdir -p %s && install -m 0755 %s %s", filepath.Dir(torUpgradeScriptPath), tmpPath, torUpgradeScriptPath)
	out, err := runSystemd(ctx, "/bin/sh", "-c", installCmd)
	if err != nil {
		return fmt.Errorf("failed to install Tor update script: %s", commandErrorDetail(out, err))
	}

	installed, err := os.ReadFile(torUpgradeScriptPath)
	if err != nil {
		return fmt.Errorf("failed to read installed Tor update script: %w", err)
	}
	if string(installed) != embeddedTorUpgradeScript {
		return errors.New("installed Tor update script content does not match embedded script")
	}
	return nil
}

func commandErrorDetail(output string, err error) string {
	detail := strings.TrimSpace(output)
	if detail != "" {
		return detail
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}
