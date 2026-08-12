package server

import (
	"context"
	"errors"
	"strings"

	"lightningos-light/internal/system"
)

// dockerGatewayIP remains a compatibility helper for the unmigrated LNbits
// and Fedimint paths. Migrated apps resolve their fixed network inside the
// privileged broker instead of calling this manager-side helper.
func dockerGatewayIP(ctx context.Context) (string, error) {
	out, err := system.RunCommandWithSudo(ctx, "docker", "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err == nil {
		ip := strings.TrimSpace(out)
		if ip != "" && ip != "<no value>" {
			return ip, nil
		}
	}
	out, err = system.RunCommandWithSudo(ctx, "ip", "-4", "addr", "show", "docker0")
	if err == nil {
		fields := strings.Fields(out)
		for index, token := range fields {
			if token == "inet" && index+1 < len(fields) {
				ip := strings.Split(fields[index+1], "/")[0]
				if ip != "" {
					return ip, nil
				}
			}
		}
	}
	return "", errors.New("unable to determine docker bridge gateway IP")
}
