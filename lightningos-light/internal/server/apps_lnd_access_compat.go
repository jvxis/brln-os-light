package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"lightningos-light/internal/system"
)

// dockerGatewayIP remains a compatibility helper for the unmigrated Fedimint
// and BTCPay LND-access paths. Migrated apps resolve their fixed network inside the
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

func ensureLnbitsRestAccess(ctx context.Context) error {
	bridgeIP, err := dockerGatewayIP(ctx)
	if err != nil || bridgeIP == "" {
		return errors.New("unable to determine docker gateway IPs")
	}
	content, err := os.ReadFile(lndConfPath)
	if err != nil {
		return fmt.Errorf("failed to read lnd.conf: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	lines, changed := updateLndRestOptions(lines, []string{bridgeIP})
	if !changed {
		return nil
	}
	if err := os.WriteFile(lndConfPath, []byte(strings.Join(lines, "\n")+"\n"), 0640); err != nil {
		return fmt.Errorf("failed to update lnd.conf: %w", err)
	}
	_, _ = system.RunCommandWithSudo(ctx, "rm", "-f", "/data/lnd/tls.cert", "/data/lnd/tls.key")
	if _, err := system.RunCommandWithSudo(ctx, "systemctl", "restart", "lnd"); err != nil {
		return fmt.Errorf("failed to restart lnd: %w", err)
	}
	return nil
}

func updateLndRestOptions(lines []string, gateways []string) ([]string, bool) {
	uniqueGateways := []string{}
	for _, gateway := range gateways {
		gateway = strings.TrimSpace(gateway)
		if gateway == "" || stringInSlice(gateway, uniqueGateways) {
			continue
		}
		uniqueGateways = append(uniqueGateways, gateway)
	}

	cleaned := append([]string{}, lines...)
	insertIdx := -1
	for i, line := range lines {
		if !strings.EqualFold(strings.TrimSpace(line), "[Application Options]") {
			continue
		}
		insertIdx = i + 1
		managedEnd := insertIdx
		preserved := []string{}
		for managedEnd < len(lines) {
			trimmed := strings.TrimSpace(lines[managedEnd])
			if !isLndRestManagedLine(trimmed) {
				break
			}
			if !isLndRestAppManagedLine(trimmed) {
				preserved = append(preserved, lines[managedEnd])
			}
			managedEnd++
		}
		cleaned = append([]string{}, lines[:insertIdx]...)
		cleaned = append(cleaned, preserved...)
		cleaned = append(cleaned, lines[managedEnd:]...)
		break
	}
	if insertIdx == -1 {
		cleaned = append(cleaned, "[Application Options]")
		insertIdx = len(cleaned)
	}

	restSet := map[string]bool{}
	tlsExtraIPSet := map[string]bool{}
	tlsExtraDomainSet := map[string]bool{}
	for _, line := range cleaned {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "restlisten="):
			restSet[strings.TrimSpace(strings.TrimPrefix(trimmed, "restlisten="))] = true
		case strings.HasPrefix(trimmed, "tlsextraip="):
			tlsExtraIPSet[strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextraip="))] = true
		case strings.HasPrefix(trimmed, "tlsextradomain="):
			tlsExtraDomainSet[strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextradomain="))] = true
		}
	}

	block := []string{}
	for _, gateway := range uniqueGateways {
		if !tlsExtraIPSet[gateway] {
			block = append(block, "tlsextraip="+gateway)
		}
	}
	if !tlsExtraDomainSet["host.docker.internal"] {
		block = append(block, "tlsextradomain=host.docker.internal")
	}
	hasWildcard := restSet["0.0.0.0:8080"] || restSet["[::]:8080"] || restSet[":8080"] || restSet["*:8080"]
	if !hasWildcard {
		for _, gateway := range uniqueGateways {
			if !restSet[gateway+":8080"] {
				block = append(block, "restlisten="+gateway+":8080")
			}
		}
	}
	updated := append([]string{}, cleaned[:insertIdx]...)
	updated = append(updated, block...)
	updated = append(updated, cleaned[insertIdx:]...)
	if len(updated) != len(lines) {
		return updated, true
	}
	for i := range updated {
		if updated[i] != lines[i] {
			return updated, true
		}
	}
	return updated, false
}

func isLndRestManagedLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "restlisten=") || strings.HasPrefix(trimmed, "tlsextraip=") || strings.HasPrefix(trimmed, "tlsextradomain=")
}

func isLndRestAppManagedLine(trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, "tlsextradomain="):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextradomain=")) == "host.docker.internal"
	case strings.HasPrefix(trimmed, "tlsextraip="):
		return isLikelyDockerGatewayIP(strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsextraip=")))
	case strings.HasPrefix(trimmed, "restlisten="):
		return isLikelyDockerGatewayAddr(strings.TrimSpace(strings.TrimPrefix(trimmed, "restlisten=")))
	default:
		return false
	}
}

func isLikelyDockerGatewayAddr(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port != "8080" {
		return false
	}
	return host == "127.0.0.1" || isLikelyDockerGatewayIP(host)
}

func isLikelyDockerGatewayIP(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return false
	}
	ip4 := ip.To4()
	private := ip4[0] == 10 || (ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) || (ip4[0] == 192 && ip4[1] == 168)
	return private && ip4[3] == 1
}
