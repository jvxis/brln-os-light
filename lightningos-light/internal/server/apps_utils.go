package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
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
