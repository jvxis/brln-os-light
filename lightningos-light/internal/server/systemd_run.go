package server

import (
  "context"

  "lightningos-light/internal/system"
)

func runSystemd(ctx context.Context, args ...string) (string, error) {
  base := []string{"--quiet", "--wait", "--pipe", "--collect"}
  full := append(base, args...)
  return system.RunCommandWithSudo(ctx, "systemd-run", full...)
}
