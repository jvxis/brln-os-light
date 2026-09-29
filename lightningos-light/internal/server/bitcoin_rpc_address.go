package server

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

func normalizeBitcoinRPCHostPort(value string, defaultPort int) (string, int) {
	if defaultPort <= 0 {
		defaultPort = 8332
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "127.0.0.1", defaultPort
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "tcp://") {
		trimmed = strings.TrimSpace(trimmed[len("tcp://"):])
	} else if strings.Contains(trimmed, "://") {
		if parsed, err := url.Parse(repairDuplicateBitcoinRPCURLPort(trimmed)); err == nil && parsed.Host != "" {
			trimmed = parsed.Host
		}
	}

	if host, port, ok := parseBitcoinHostPort(trimmed); ok {
		return host, port
	}

	parts := strings.Split(trimmed, ":")
	if len(parts) > 2 && !strings.HasPrefix(trimmed, "[") {
		for _, part := range parts[1:] {
			if port, ok := validBitcoinPort(part); ok {
				return parts[0], port
			}
		}
	}

	return strings.Trim(trimmed, "[]"), defaultPort
}

// Older configurations may contain the RPC port twice. Go 1.26 rejects that
// authority, so repair only an identical, valid repeated port before parsing.
// Keep URL validation enabled and leave paths, queries and IPv6 literals intact.
func repairDuplicateBitcoinRPCURLPort(value string) string {
	scheme, rest, ok := strings.Cut(value, "://")
	if !ok {
		return value
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority := rest[:end]
	lastColon := strings.LastIndexByte(authority, ':')
	if lastColon < 0 {
		return value
	}
	host, port, err := net.SplitHostPort(authority[:lastColon])
	if err != nil || host == "" || port != authority[lastColon+1:] {
		return value
	}
	if _, valid := validBitcoinPort(port); !valid {
		return value
	}
	return scheme + "://" + net.JoinHostPort(host, port) + rest[end:]
}

func bitcoinRPCHostPort(value string, defaultPort int) string {
	host, port := normalizeBitcoinRPCHostPort(value, defaultPort)
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func parseBitcoinHostPort(value string) (string, int, bool) {
	if host, portValue, err := net.SplitHostPort(value); err == nil {
		if port, ok := validBitcoinPort(portValue); ok {
			return strings.Trim(host, "[]"), port, true
		}
	}

	if strings.Count(value, ":") != 1 {
		return "", 0, false
	}
	parts := strings.SplitN(value, ":", 2)
	if strings.TrimSpace(parts[0]) == "" {
		return "", 0, false
	}
	if port, ok := validBitcoinPort(parts[1]); ok {
		return strings.TrimSpace(parts[0]), port, true
	}
	return "", 0, false
}

func validBitcoinPort(value string) (int, bool) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}
