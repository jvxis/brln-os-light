package server

import (
	"context"
	"fmt"
	"strings"

	"lightningos-light/internal/system"
)

func runSystemd(ctx context.Context, args ...string) (string, error) {
	base := []string{"--quiet", "--wait", "--pipe", "--collect"}
	full := append(base, args...)
	out, err := system.RunCommandWithSudo(ctx, "systemd-run", full...)
	if err == nil {
		return out, nil
	}
	// systemd-run reports failures from the transient unit on stdout/stderr.
	// Preserve that diagnostic so app installation errors identify the missing
	// user, command, or permission instead of exposing only "exit status 1".
	if detail := strings.TrimSpace(out); detail != "" {
		return out, fmt.Errorf("%w: %s", err, detail)
	}
	return out, err
}
