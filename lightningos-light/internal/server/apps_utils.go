package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func ensureFile(path string, content string) error {
	if fileExists(path) {
		current, err := os.ReadFile(path)
		if err == nil && string(current) == content {
			return nil
		}
	}
	return writeFile(path, content, 0640)
}

func ensureFileWithChange(path string, content string) (bool, error) {
	if fileExists(path) {
		current, err := os.ReadFile(path)
		if err == nil && string(current) == content {
			return false, nil
		}
	}
	if err := writeFile(path, content, 0640); err != nil {
		return false, err
	}
	return true, nil
}

// reconcileInstalledCatalogFile replaces only an existing regular
// manager-owned declaration file. It never creates a missing installation and
// refuses links or directories before writing the current closed catalog
// content. The privileged broker validates the result before any lifecycle
// command is executed.
func reconcileInstalledCatalogFile(path string, content string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("failed to inspect installed catalog file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("installed catalog file is unsafe: %s", path)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read installed catalog file %s: %w", path, err)
	}
	if string(current) == content {
		return nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".lightningos-catalog-*")
	if err != nil {
		return fmt.Errorf("failed to create catalog replacement for %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		temporary.Close()
		return fmt.Errorf("failed to secure catalog replacement for %s: %w", path, err)
	}
	if _, err := temporary.WriteString(content); err != nil {
		temporary.Close()
		return fmt.Errorf("failed to write catalog replacement for %s: %w", path, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("failed to sync catalog replacement for %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("failed to close catalog replacement for %s: %w", path, err)
	}
	// Windows does not replace an existing destination with os.Rename. Removing
	// the directory entry remains safe here: it never follows a link, and the
	// production Linux path uses the atomic replacement below.
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to replace installed catalog file %s: %w", path, err)
		}
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("failed to reconcile installed catalog file %s: %w", path, err)
	}
	return nil
}

func writeFile(path string, content string, mode os.FileMode) error {
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func readEnvValue(path string, key string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	return ""
}

func readSecretFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

func appendEnvLine(path string, key string, value string) error {
	if value == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to update %s: %w", path, err)
	}
	defer file.Close()
	if _, err := file.WriteString(fmt.Sprintf("%s=%s\n", key, value)); err != nil {
		return fmt.Errorf("failed to update %s: %w", path, err)
	}
	return nil
}

func stringInSlice(value string, items []string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func splitEnvList(value string) []string {
	if value == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	items := []string{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func mergeUnique(base []string, extra []string) []string {
	merged := append([]string{}, base...)
	for _, value := range extra {
		if !stringInSlice(value, merged) {
			merged = append(merged, value)
		}
	}
	return merged
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func linuxPathHasSafeChars(value string) bool {
	for _, char := range value {
		if char >= 'a' && char <= 'z' {
			continue
		}
		if char >= 'A' && char <= 'Z' {
			continue
		}
		if char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '/', '.', '_', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func setEnvValue(path string, key string, value string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	lines := []string{}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, key+"=") {
			continue
		}
		lines = append(lines, line)
	}
	lines = append(lines, fmt.Sprintf("%s=%s", key, value))
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		return fmt.Errorf("failed to update %s: %w", path, err)
	}
	return nil
}
