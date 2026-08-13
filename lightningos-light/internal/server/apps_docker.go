package server

import (
	"context"
	"errors"
	"strings"

	"lightningos-light/internal/system"
)

// ensureDockerForCatalogApp deliberately has no legacy fallback. Every
// catalog application must provision both the Docker packages and daemon
// through the typed privileged broker before it can reach its lifecycle.
func ensureDockerForCatalogApp(ctx context.Context) error {
	return ensureDockerForCatalogAppEnforce(ctx)
}

func ensureDockerForCatalogAppEnforce(ctx context.Context) error {
	if handled, err := system.EnsurePackageFeatureWithBroker(ctx, "docker_runtime"); !handled {
		return errors.New("Docker package provisioning requires privileged broker enforce mode")
	} else if err != nil {
		return err
	}
	if handled, err := system.EnsureDockerRuntimeWithBroker(ctx); !handled {
		return errors.New("Docker runtime provisioning requires privileged broker enforce mode")
	} else {
		return err
	}
}

const (
	composeErrorMaxLines = 24
	composeErrorMaxChars = 6000
)

// composeCommandErrorDetail keeps the actionable tail of a failed broker-side
// Compose command while applying the same secret redaction used by diagnostics.
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

// These small parsers remain useful to broker-facing tests and reject any
// warning text that might otherwise become a Docker identifier.
func composeStartsDetached(args []string) bool {
	if len(args) == 0 || args[0] != "up" {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "-d" || arg == "--detach" {
			return true
		}
	}
	return false
}

func isDockerContainerID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

func parseComposeContainerID(out string) string {
	id := ""
	for _, line := range strings.Split(out, "\n") {
		candidate := strings.TrimSpace(line)
		if isDockerContainerID(candidate) {
			id = candidate
		}
	}
	return id
}
