package server

import (
	"net"
	"strings"
)

// updateLndRestOptions is retained only as a pure parser for recognized legacy
// configuration. All host mutation and LND reconciliation now belongs to the
// typed broker operations used by the individual applications.
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
