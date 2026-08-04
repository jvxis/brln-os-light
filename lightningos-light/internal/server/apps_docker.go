package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

func ensureDocker(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err == nil {
		if _, infoErr := system.RunCommandWithSudo(ctx, "docker", "info"); infoErr == nil {
			if err := ensureCompose(ctx); err != nil {
				return err
			}
			return nil
		}
		if _, startErr := system.RunCommandWithSudo(ctx, "systemctl", "enable", "--now", "docker"); startErr == nil || isDockerActive(ctx) {
			if err := ensureCompose(ctx); err != nil {
				return err
			}
			return nil
		}
	}
	if err := installDocker(ctx); err != nil {
		return err
	}
	return ensureCompose(ctx)
}

func installDocker(ctx context.Context) error {
	if _, err := runApt(ctx, "update"); err != nil {
		return err
	}
	out, err := runApt(ctx, "install", "-y", "docker.io")
	if err != nil {
		return fmt.Errorf("docker install failed: %s", strings.TrimSpace(out))
	}
	if _, err := system.RunCommandWithSudo(ctx, "systemctl", "enable", "--now", "docker"); err != nil {
		if isDockerActive(ctx) {
			return nil
		}
		return fmt.Errorf("failed to start docker: %w", err)
	}
	return nil
}

func isDockerActive(ctx context.Context) bool {
	out, err := system.RunCommandWithSudo(ctx, "systemctl", "is-active", "docker")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "active"
}

func ensureCompose(ctx context.Context) error {
	if _, _, err := resolveCompose(ctx); err == nil {
		return nil
	}
	_, err := runApt(ctx, "install", "-y", "docker-compose-plugin")
	if err != nil && strings.Contains(err.Error(), "passwordless sudo") {
		return err
	}
	_, err = runApt(ctx, "install", "-y", "docker-compose")
	if err != nil && strings.Contains(err.Error(), "passwordless sudo") {
		return err
	}
	if err := installComposePluginBinary(ctx); err != nil {
		if strings.Contains(err.Error(), "passwordless sudo") {
			return err
		}
	}
	if _, _, err := resolveCompose(ctx); err != nil {
		return err
	}
	return nil
}

func runApt(ctx context.Context, args ...string) (string, error) {
	var out string
	for attempt := 0; attempt < 10; attempt++ {
		var err error
		out, err = runAptOnce(ctx, args...)
		if err == nil {
			return out, nil
		}
		if strings.Contains(out, "password is required") {
			return out, errors.New("docker install needs passwordless sudo for lightningos (re-run install.sh or add /etc/sudoers.d/lightningos)")
		}
		if strings.Contains(out, "Could not get lock") || strings.Contains(out, "dpkg frontend lock") || strings.Contains(out, "dpkg/lock") {
			time.Sleep(3 * time.Second)
			continue
		}
		return out, fmt.Errorf("apt-get failed: %s", strings.TrimSpace(out))
	}
	return out, errors.New("apt-get blocked by dpkg lock")
}

func runAptOnce(ctx context.Context, args ...string) (string, error) {
	aptPath := "/usr/bin/apt-get"
	systemdArgs := append([]string{"--wait", "--pipe", "--collect", aptPath}, args...)
	out, err := system.RunCommandWithSudo(ctx, "systemd-run", systemdArgs...)
	if err == nil {
		return out, nil
	}
	if strings.Contains(out, "password is required") {
		return out, err
	}
	fallbackOut, fallbackErr := system.RunCommandWithSudo(ctx, "apt-get", args...)
	if fallbackErr == nil {
		return fallbackOut, nil
	}
	if strings.TrimSpace(fallbackOut) == "" {
		return out, err
	}
	return fallbackOut, fallbackErr
}

func composeBaseArgs(appRoot string, composePath string) []string {
	envPath := filepath.Join(appRoot, ".env")
	args := []string{}
	if fileExists(envPath) {
		args = append(args, "--env-file", envPath)
	}
	args = append(args, "--project-directory", appRoot, "-f", composePath)
	return args
}

func runCompose(ctx context.Context, appRoot string, composePath string, args ...string) error {
	cmd, baseArgs, err := resolveCompose(ctx)
	if err != nil {
		return err
	}
	fullArgs := append(baseArgs, composeBaseArgs(appRoot, composePath)...)
	fullArgs = append(fullArgs, args...)
	out, err := system.RunCommandWithSudo(ctx, cmd, fullArgs...)
	if err != nil {
		return fmt.Errorf("docker compose command failed: %s", composeCommandErrorDetail(out, err))
	}
	return nil
}

const (
	composeErrorMaxLines = 24
	composeErrorMaxChars = 6000
)

// composeCommandErrorDetail keeps the actionable tail of a failed Compose
// command while applying the same secret redaction used by diagnostics. Docker
// builds often emit the real cause only on stdout/stderr; returning the bare
// exit status makes every app installation failure look like a sudo problem.
func composeCommandErrorDetail(output string, err error) string {
	lines := redactSystemCheckLogLines(strings.Split(output, "\n"))
	if len(lines) > composeErrorMaxLines {
		lines = lines[len(lines)-composeErrorMaxLines:]
	}
	detail := strings.TrimSpace(strings.Join(lines, "\n"))
	runes := []rune(detail)
	if len(runes) > composeErrorMaxChars {
		detail = strings.TrimSpace(string(runes[len(runes)-composeErrorMaxChars:]))
	}
	if detail != "" {
		return detail
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}

func getComposeStatus(ctx context.Context, appRoot string, composePath string, service string) (string, error) {
	cmd, baseArgs, err := resolveCompose(ctx)
	if err != nil {
		return "unknown", err
	}
	fullArgs := append(baseArgs, composeBaseArgs(appRoot, composePath)...)
	fullArgs = append(fullArgs, "ps", "--services", "--filter", "status=running")
	out, err := system.RunCommandWithSudo(ctx, cmd, fullArgs...)
	if err != nil {
		return "unknown", err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == service {
			return "running", nil
		}
	}
	return "stopped", nil
}

func composeContainerID(ctx context.Context, appRoot string, composePath string, service string) (string, error) {
	cmd, baseArgs, err := resolveCompose(ctx)
	if err != nil {
		return "", err
	}
	fullArgs := append(baseArgs, composeBaseArgs(appRoot, composePath)...)
	fullArgs = append(fullArgs, "ps", "-q", service)
	out, err := system.RunCommandWithSudo(ctx, cmd, fullArgs...)
	if err != nil {
		return "", err
	}
	return parseComposeContainerID(out), nil
}

// parseComposeContainerID ignores warnings emitted by Compose on stderr.
// RunCommandWithSudo intentionally combines stdout and stderr, so returning the
// entire output can turn a valid ID plus a warning into an invalid Docker
// container argument.
func parseComposeContainerID(out string) string {
	id := ""
	for _, line := range strings.Split(out, "\n") {
		candidate := strings.TrimSpace(line)
		if len(candidate) < 12 || len(candidate) > 64 {
			continue
		}
		valid := true
		for _, r := range candidate {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				valid = false
				break
			}
		}
		if valid {
			id = candidate
		}
	}
	return id
}

type composeRelease struct {
	TagName string `json:"tag_name"`
}

func resolveCompose(ctx context.Context) (string, []string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "compose", "version")
	if err == nil {
		return "docker", []string{"compose"}, nil
	}
	if strings.Contains(out, "password is required") || strings.Contains(err.Error(), "password is required") {
		return "", nil, errors.New("docker compose requires passwordless sudo for lightningos")
	}
	out, err = system.RunCommandWithSudo(ctx, "docker-compose", "version")
	if err == nil {
		return "docker-compose", []string{}, nil
	}
	if strings.Contains(out, "password is required") || strings.Contains(err.Error(), "password is required") {
		return "", nil, errors.New("docker-compose requires passwordless sudo for lightningos")
	}
	return "", nil, errors.New("docker compose not available (install docker-compose-plugin or docker-compose)")
}

func installComposePluginBinary(ctx context.Context) error {
	if fileExists("/usr/lib/docker/cli-plugins/docker-compose") || fileExists("/usr/local/lib/docker/cli-plugins/docker-compose") {
		return nil
	}
	tag := fetchLatestComposeTag(ctx)
	if tag == "" {
		tag = "v2.32.4"
	}
	arch := mapComposeArch(runtime.GOARCH)
	if arch == "" {
		return fmt.Errorf("unsupported architecture for docker compose: %s", runtime.GOARCH)
	}
	url := fmt.Sprintf("https://github.com/docker/compose/releases/download/%s/docker-compose-linux-%s", tag, arch)
	if _, err := exec.LookPath("curl"); err != nil {
		if _, err := runApt(ctx, "install", "-y", "curl"); err != nil {
			return err
		}
	}
	targetPath := "/usr/local/lib/docker/cli-plugins/docker-compose"
	script := fmt.Sprintf("mkdir -p /usr/local/lib/docker/cli-plugins && curl -fsSL -o %s %s && chmod 0755 %s", targetPath, url, targetPath)
	if _, err := system.RunCommandWithSudo(ctx, "systemd-run", "--wait", "--pipe", "--collect", "/bin/sh", "-c", script); err == nil {
		return nil
	}
	targetPath = "/usr/lib/docker/cli-plugins/docker-compose"
	script = fmt.Sprintf("mkdir -p /usr/lib/docker/cli-plugins && curl -fsSL -o %s %s && chmod 0755 %s", targetPath, url, targetPath)
	if _, err := system.RunCommandWithSudo(ctx, "systemd-run", "--wait", "--pipe", "--collect", "/bin/sh", "-c", script); err == nil {
		return nil
	}
	return errors.New("failed to install docker compose plugin binary")
}

func fetchLatestComposeTag(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/docker/compose/releases/latest", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var release composeRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return ""
	}
	return strings.TrimSpace(release.TagName)
}

func mapComposeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return ""
	}
}
