package server

import (
	"errors"
	"fmt"
	"lightningos-light/internal/logsafe"
	"net"
	"slices"
	"strconv"
	"strings"
)

type lndConfigEntry struct{ section, sectionLabel, key, value, line string }

func lndConfigEntries(raw string) []lndConfigEntry {
	var entries []lndConfigEntry
	section := ""
	sectionLabel := ""
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sectionLabel = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			section = strings.ToLower(sectionLabel)
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if ok {
			entries = append(entries, lndConfigEntry{section: section, sectionLabel: sectionLabel, key: strings.ToLower(strings.TrimSpace(key)), value: strings.TrimSpace(value), line: line})
		}
	}
	return entries
}

func lndPolicyKey(key string) string {
	return strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(key))
}

func lndPolicyValues(raw string) map[string][]string {
	values := make(map[string][]string)
	for _, entry := range lndConfigEntries(raw) {
		key := lndPolicyKey(entry.key)
		value := entry.value
		if strings.HasPrefix(value, "\"") {
			if decoded, err := strconv.Unquote(value); err == nil {
				value = decoded
			}
		}
		values[key] = append(values[key], value)
	}
	return values
}

func lndProtectedPath(key string) bool {
	return strings.HasSuffix(key, "path") || strings.HasSuffix(key, "file") || strings.HasSuffix(key, "dir") || key == "walletunlockallowcreate"
}

// Protect local RPC/auth and filesystem/startup boundaries without rebuilding
// an existing config or overwriting operator-managed paths and other options.
func validateLNDConfigSecurity(previous, updated string) error {
	if logsafe.ContainsPrivateMaterial(updated) {
		return errors.New("wallet seed or private key material must not be stored in LND configuration")
	}
	if err := validateLNDNetworkCombination(updated); err != nil {
		return err
	}
	before, after := lndPolicyValues(previous), lndPolicyValues(updated)
	for _, key := range []string{"nomacaroons", "noseedbackup", "debughtlc", "unsafereplay"} {
		for _, value := range after[key] {
			enabled, err := strconv.ParseBool(value)
			if err != nil || enabled {
				return fmt.Errorf("LND option %s must remain disabled", key)
			}
		}
	}
	for key, values := range after {
		if lndProtectedPath(key) && !slices.Equal(values, before[key]) {
			return errors.New("storage, credential and startup paths must be preserved; use the dedicated setup workflow for path changes")
		}
	}
	for key, values := range before {
		if lndProtectedPath(key) && !slices.Equal(values, after[key]) {
			return errors.New("storage, credential and startup paths must be preserved; use the dedicated setup workflow for path changes")
		}
	}
	for _, value := range after["rpclisten"] {
		if !lndLoopbackListener(value) && !(strings.HasPrefix(value, "unix:") && slices.Equal(after["rpclisten"], before["rpclisten"])) {
			return errors.New("LND RPC listeners must remain loopback-only")
		}
	}
	for _, value := range after["restlisten"] {
		if !lndLoopbackListener(value) && !slices.Equal(after["restlisten"], before["restlisten"]) {
			return errors.New("new LND REST listeners must remain loopback-only")
		}
	}
	for _, value := range after["noresttls"] {
		disabled, err := strconv.ParseBool(value)
		if err != nil || disabled && !slices.Equal(after["noresttls"], before["noresttls"]) {
			return errors.New("LND REST TLS cannot be disabled through the config editor")
		}
		if disabled {
			for _, listener := range after["restlisten"] {
				if !lndLoopbackListener(listener) {
					return errors.New("unencrypted LND REST must be loopback-only")
				}
			}
		}
	}
	return nil
}

func lndLoopbackListener(value string) bool {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"tcp://", "tcp4://", "tcp6://"} {
		value = strings.TrimPrefix(value, prefix)
	}
	host := value
	if parsed, _, err := net.SplitHostPort(value); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
