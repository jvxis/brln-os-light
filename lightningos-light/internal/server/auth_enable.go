package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	osuser "os/user"
	"strings"
	"time"

	"lightningos-light/internal/system"

	"gopkg.in/yaml.v3"
)

const authDefaultConfigPath = "/etc/lightningos/config.yaml"

func (s *Server) handleAuthEnableLogin(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.cfg == nil {
		writeError(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	if s.cfg.Features.LoginEnabled() {
		writeErrorCode(w, http.StatusConflict, "auth_already_enabled", "login protection is already enabled")
		return
	}

	configPath := authConfigPath(s.cfg.Path)
	if err := enableLoginInConfigFile(configPath); err != nil {
		retryErr := err
		compatCtx, compatCancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer compatCancel()
		if compatFixErr := ensureAuthEnableCompat(compatCtx, configPath); compatFixErr == nil {
			retryErr = enableLoginInConfigFile(configPath)
		} else if s.logger != nil {
			s.logger.Printf("auth enable compat fix failed: %v", compatFixErr)
		}
		if retryErr != nil {
			if s.logger != nil {
				s.logger.Printf("auth enable failed: %v", retryErr)
			}
			writeErrorCode(w, http.StatusInternalServerError, "auth_enable_failed", "failed to enable login protection")
			return
		}
	}

	enabled := true
	s.cfg.Features.EnableLogin = &enabled
	s.scheduleManagerRestart(1500 * time.Millisecond)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":                true,
		"restart_scheduled": true,
		"config_path":       configPath,
	})
}

func authConfigPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return authDefaultConfigPath
	}
	return trimmed
}

func (s *Server) scheduleManagerRestart(delay time.Duration) {
	if s == nil {
		return
	}
	if delay <= 0 {
		delay = time.Second
	}
	time.AfterFunc(delay, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := system.SystemctlRestart(ctx, "lightningos-manager"); err != nil && s.logger != nil {
			s.logger.Printf("auth enable restart failed: %v", err)
		}
	})
}

func enableLoginInConfigFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if len(root.Content) == 0 {
		return fmt.Errorf("config yaml is empty")
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return fmt.Errorf("config yaml root is not a mapping")
	}

	features := yamlEnsureMapValue(doc, "features")
	enableLogin := yamlEnsureMapValue(features, "enable_login")
	enableLogin.Kind = yaml.ScalarNode
	enableLogin.Tag = "!!bool"
	enableLogin.Value = "true"

	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}

	perm := os.FileMode(0o640)
	if info, statErr := os.Stat(path); statErr == nil {
		perm = info.Mode().Perm()
	}
	if err := os.WriteFile(path, out.Bytes(), perm); err == nil {
		return nil
	} else if !os.IsPermission(err) {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return system.WriteFileWithSudo(ctx, path, out.Bytes())
}

func ensureAuthEnableCompat(ctx context.Context, configPath string) error {
	managerUser := authEnableManagerUser()
	teePath := authLookupPath("tee", "/usr/bin/tee")
	systemctlPath := authLookupPath("systemctl", "/usr/bin/systemctl")
	systemdRunPath := authLookupPath("systemd-run", "/usr/bin/systemd-run")
	sudoersPath := "/etc/sudoers.d/lightningos-auth-enable"

	lines := []string{}
	if noRequireTTY := sudoersNoRequireTTYLine(ctx, managerUser); noRequireTTY != "" {
		lines = append(lines, noRequireTTY)
	}
	lines = append(lines,
		fmt.Sprintf("%s ALL=NOPASSWD: %s %s, %s restart lightningos-manager, %s *", managerUser, teePath, configPath, systemctlPath, systemdRunPath),
		"",
	)
	content := strings.Join(lines, "\n")

	if err := system.WriteFileWithSudo(ctx, sudoersPath, []byte(content)); err != nil {
		return err
	}

	script := fmt.Sprintf("chmod 440 %s", shSingleQuote(sudoersPath))
	if visudoPath, err := exec.LookPath("visudo"); err == nil && strings.TrimSpace(visudoPath) != "" {
		script += fmt.Sprintf(" && %s -cf %s >/dev/null", shSingleQuote(visudoPath), shSingleQuote(sudoersPath))
	}
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return err
	}
	return nil
}

func sudoersNoRequireTTYLine(ctx context.Context, managerUser string) string {
	visudoPath, err := exec.LookPath("visudo")
	if err != nil || strings.TrimSpace(visudoPath) == "" {
		return ""
	}
	tmp, err := os.CreateTemp("", "lightningos-sudoers-*.tmp")
	if err != nil {
		return ""
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(fmt.Sprintf("Defaults:%s !requiretty\n", managerUser)); err != nil {
		_ = tmp.Close()
		return ""
	}
	if err := tmp.Close(); err != nil {
		return ""
	}
	if err := exec.CommandContext(ctx, visudoPath, "-cf", tmp.Name()).Run(); err != nil {
		return ""
	}
	return fmt.Sprintf("Defaults:%s !requiretty", managerUser)
}

func authEnableManagerUser() string {
	current, err := osuser.Current()
	if err == nil {
		username := strings.TrimSpace(current.Username)
		if username != "" {
			return username
		}
	}
	return "lightningos"
}

func authLookupPath(name string, fallback string) string {
	if path, err := exec.LookPath(name); err == nil && strings.TrimSpace(path) != "" {
		return path
	}
	return fallback
}

func shSingleQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func yamlEnsureMapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		mapping.Kind = yaml.MappingNode
		mapping.Tag = "!!map"
		mapping.Content = nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		keyNode := mapping.Content[index]
		if keyNode.Value == key {
			return mapping.Content[index+1]
		}
	}

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return valueNode
}
