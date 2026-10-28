package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"lightningos-light/internal/logsafe"
)

const lndSecretPlaceholder = "__LOS_KEEP_SECRET__"

type lndSensitiveOption struct {
	ID         string `json:"id"`
	Section    string `json:"section"`
	Key        string `json:"key"`
	Configured bool   `json:"configured"`
}

type lndSecretUpdate struct {
	Action string  `json:"action"`
	Value  *string `json:"value,omitempty"`
}

var commonLNDSecretOptions = []lndConfigEntry{
	{section: "bitcoind", sectionLabel: "Bitcoind", key: "bitcoind.rpcuser"},
	{section: "bitcoind", sectionLabel: "Bitcoind", key: "bitcoind.rpcpass"},
	{section: "btcd", sectionLabel: "Btcd", key: "btcd.rpcuser"},
	{section: "btcd", sectionLabel: "Btcd", key: "btcd.rpcpass"},
}

type lndConfigSnapshot struct {
	raw      []byte
	public   string
	revision string
	options  []lndSensitiveOption
}

func (s *Server) readLNDConfigSnapshot() (lndConfigSnapshot, error) {
	lndConfigMutationMu.Lock()
	defer lndConfigMutationMu.Unlock()
	raw, err := s.lndConfigOperations().read()
	if err != nil {
		return lndConfigSnapshot{}, err
	}
	if logsafe.ContainsPrivateMaterial(string(raw)) {
		return lndConfigSnapshot{}, errors.New("configuration contains unsupported private material; remove it locally before using the editor")
	}
	revision, err := s.lndConfigRevision(raw)
	if err != nil {
		return lndConfigSnapshot{}, err
	}
	public, options, _ := redactLNDConfig(string(raw))
	return lndConfigSnapshot{raw: raw, public: public, revision: revision, options: options}, nil
}

func lndOptionID(entry lndConfigEntry) string { return entry.section + "/" + entry.key }

func lndOptionIsSecret(entry lndConfigEntry) bool {
	key := lndPolicyKey(entry.key)
	if lndProtectedPath(key) || key == "nomacaroons" {
		return false
	}
	for _, part := range []string{"rpcuser", "rpcpass", "rpcauth", "password", "passwd", "token", "secret", "credential", "macaroon", "privatekey", "xprv", "mnemonic", "apikey", "authkey"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	u, err := url.Parse(strings.Trim(entry.value, "\""))
	return err == nil && u.User != nil || logsafe.Redact(entry.value) != entry.value
}

func protectedComment(index int) string { return fmt.Sprintf("# [LOS protected comment %d]", index) }

func redactLNDConfig(raw string) (string, []lndSensitiveOption, map[string]string) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	section := ""
	seen := make(map[string]bool)
	options := []lndSensitiveOption{}
	comments := make(map[string]string)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			if logsafe.Redact(line) != line {
				marker := protectedComment(index)
				comments[marker] = line
				lines[index] = marker
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		entry := lndConfigEntry{section: section, key: strings.ToLower(strings.TrimSpace(key)), value: strings.TrimSpace(value), line: line}
		if !lndOptionIsSecret(entry) {
			continue
		}
		lines[index] = strings.TrimSpace(key) + "=" + lndSecretPlaceholder
		id := lndOptionID(entry)
		if !seen[id] {
			options = append(options, lndSensitiveOption{ID: id, Section: section, Key: entry.key, Configured: true})
			seen[id] = true
		}
	}
	for _, entry := range commonLNDSecretOptions {
		id := lndOptionID(entry)
		if !seen[id] {
			options = append(options, lndSensitiveOption{ID: id, Section: entry.section, Key: entry.key})
		}
	}
	return strings.Join(lines, "\n"), options, comments
}

// The revision is opaque and keyed: exposing a plain digest of a file that
// contains passwords would give a credential-guessing oracle. A Manager restart
// rotates this in-memory key and forces a stale advanced editor to reload.
func (s *Server) lndConfigRevision(raw []byte) (string, error) {
	s.lndConfigRevisionOnce.Do(func() { _, s.lndConfigRevisionErr = rand.Read(s.lndConfigRevisionKey[:]) })
	if s.lndConfigRevisionErr != nil {
		return "", errors.New("config revision unavailable")
	}
	mac := hmac.New(sha256.New, s.lndConfigRevisionKey[:])
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func appendLNDConfigLines(raw, section string, extra []string) string {
	if len(extra) == 0 {
		return raw
	}
	lines := strings.Split(raw, "\n")
	start, end := -1, len(lines)
	if section == "" {
		start = 0
	}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
		if name == strings.ToLower(section) {
			start = index + 1
			continue
		}
		if start >= 0 {
			end = index
			break
		}
	}
	if start < 0 {
		lines = append(lines, "["+section+"]")
		end = len(lines)
	}
	result := append([]string{}, lines[:end]...)
	result = append(result, extra...)
	result = append(result, lines[end:]...)
	return strings.Join(result, "\n")
}

// Omission means keep. Only an explicit protected update may replace/remove a
// secret; placeholders are never written as credentials. Non-secret lines and
// unknown operator options remain authoritative in the advanced editor.
func restoreLNDConfigSecrets(previous, submitted string, updates map[string]lndSecretUpdate) (string, error) {
	_, _, comments := redactLNDConfig(previous)
	old := make(map[string][]lndConfigEntry)
	order := []string{}
	for _, entry := range lndConfigEntries(previous) {
		if lndOptionIsSecret(entry) {
			id := lndOptionID(entry)
			if len(old[id]) == 0 {
				order = append(order, id)
			}
			old[id] = append(old[id], entry)
		}
	}
	for _, entry := range commonLNDSecretOptions {
		id := lndOptionID(entry)
		if _, provided := updates[id]; provided && len(old[id]) == 0 {
			old[id] = []lndConfigEntry{entry}
			order = append(order, id)
		}
	}
	lines := strings.Split(strings.ReplaceAll(submitted, "\r\n", "\n"), "\n")
	filtered := []string{}
	section := ""
	commentSeen := make(map[string]bool)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if original, ok := comments[trimmed]; ok {
			if commentSeen[trimmed] {
				return "", errors.New("protected comment duplicated")
			}
			commentSeen[trimmed] = true
			filtered = append(filtered, original)
			continue
		}
		if strings.Contains(line, lndSecretPlaceholder) && (strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";")) {
			return "", errors.New("invalid protected placeholder")
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
		}
		if !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, ";") {
			key, value, ok := strings.Cut(trimmed, "=")
			entry := lndConfigEntry{section: section, key: strings.ToLower(strings.TrimSpace(key)), value: strings.TrimSpace(value)}
			// URL-valued options lose their userinfo when redacted. Match the
			// stored option ID as well as its submitted name/value classification.
			if ok && (lndOptionIsSecret(entry) || len(old[lndOptionID(entry)]) > 0) {
				if entry.value != lndSecretPlaceholder {
					return "", errors.New("sensitive values require explicit keep, replace or clear updates")
				}
				if len(old[lndOptionID(entry)]) == 0 {
					return "", errors.New("protected placeholder has no stored value")
				}
				continue
			}
		}
		if strings.Contains(line, lndSecretPlaceholder) || strings.Contains(line, "[LOS protected comment") {
			return "", errors.New("invalid protected placeholder")
		}
		filtered = append(filtered, line)
	}
	for id := range updates {
		if len(old[id]) == 0 {
			return "", errors.New("unknown sensitive option")
		}
	}
	result := strings.Join(filtered, "\n")
	for _, id := range order {
		entries := old[id]
		update, provided := updates[id]
		if !provided || update.Action == "keep" {
			if update.Value != nil {
				return "", errors.New("keep must not include a sensitive value")
			}
			extra := []string{}
			for _, entry := range entries {
				if entry.line != "" {
					extra = append(extra, entry.line)
				}
			}
			result = appendLNDConfigLines(result, entries[0].sectionLabel, extra)
			continue
		}
		switch update.Action {
		case "clear":
			if update.Value != nil {
				return "", errors.New("clear must not include a sensitive value")
			}
		case "replace":
			if update.Value == nil || len(*update.Value) == 0 || len(*update.Value) > 4096 || strings.ContainsAny(*update.Value, "\r\n\x00") || *update.Value == lndSecretPlaceholder {
				return "", errors.New("invalid sensitive replacement value")
			}
			key := lndPolicyKey(entries[0].key)
			for _, blocked := range []string{"mnemonic", "privatekey", "xprv", "seedwords"} {
				if strings.Contains(key, blocked) {
					return "", errors.New("key or seed material cannot be supplied through the config API")
				}
			}
			// go-flags decodes double-quoted INI strings with strconv.Unquote.
			result = appendLNDConfigLines(result, entries[0].sectionLabel, []string{entries[0].key + "=" + strconv.Quote(*update.Value)})
		default:
			return "", errors.New("sensitive update must be keep, replace or clear")
		}
	}
	return result, nil
}
