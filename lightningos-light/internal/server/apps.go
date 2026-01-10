package server

import (
  "context"
  "crypto/rand"
  "encoding/base64"
  "encoding/json"
  "errors"
  "fmt"
  "io"
  "net/http"
  "os"
  "os/exec"
  "path/filepath"
  "runtime"
  "strings"
  "time"

  "github.com/go-chi/chi/v5"

  "lightningos-light/internal/system"
)

const (
  appsRoot = "/var/lib/lightningos/apps"
  appsDataRoot = "/var/lib/lightningos/apps-data"
)

type appDefinition struct {
  ID string
  Name string
  Description string
  Port int
}

type appInfo struct {
  ID string `json:"id"`
  Name string `json:"name"`
  Description string `json:"description"`
  Installed bool `json:"installed"`
  Status string `json:"status"`
  Port int `json:"port"`
  AdminPasswordPath string `json:"admin_password_path,omitempty"`
}

type lndgPaths struct {
  Root string
  DataDir string
  PgDir string
  ComposePath string
  EnvPath string
  DockerfilePath string
  EntrypointPath string
  AdminPasswordPath string
  DbPasswordPath string
}

func lndgDefinition() appDefinition {
  return appDefinition{
    ID: "lndg",
    Name: "LNDg",
    Description: "Advanced analytics, automation, and insights for your LND node.",
    Port: 8889,
  }
}

func lndgAppPaths() lndgPaths {
  root := filepath.Join(appsRoot, "lndg")
  dataDir := filepath.Join(appsDataRoot, "lndg", "data")
  pgDir := filepath.Join(appsDataRoot, "lndg", "pgdata")
  return lndgPaths{
    Root: root,
    DataDir: dataDir,
    PgDir: pgDir,
    ComposePath: filepath.Join(root, "docker-compose.yaml"),
    EnvPath: filepath.Join(root, ".env"),
    DockerfilePath: filepath.Join(root, "Dockerfile"),
    EntrypointPath: filepath.Join(root, "entrypoint.sh"),
    AdminPasswordPath: filepath.Join(dataDir, "lndg-admin.txt"),
    DbPasswordPath: filepath.Join(dataDir, "lndg-db-password.txt"),
  }
}

func (s *Server) handleAppsList(w http.ResponseWriter, r *http.Request) {
  defs := []appDefinition{
    lndgDefinition(),
  }
  resp := make([]appInfo, 0, len(defs))
  for _, def := range defs {
    info := s.getAppInfo(r.Context(), def)
    resp = append(resp, info)
  }
  writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAppInstall(w http.ResponseWriter, r *http.Request) {
  appID := chi.URLParam(r, "id")
  if appID == "" {
    writeError(w, http.StatusBadRequest, "missing app id")
    return
  }
  switch appID {
  case "lndg":
    if err := s.installLndg(r.Context()); err != nil {
      writeError(w, http.StatusInternalServerError, err.Error())
      return
    }
  default:
    writeError(w, http.StatusNotFound, "app not found")
    return
  }
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAppUninstall(w http.ResponseWriter, r *http.Request) {
  appID := chi.URLParam(r, "id")
  if appID == "" {
    writeError(w, http.StatusBadRequest, "missing app id")
    return
  }
  switch appID {
  case "lndg":
    if err := s.uninstallLndg(r.Context()); err != nil {
      writeError(w, http.StatusInternalServerError, err.Error())
      return
    }
  default:
    writeError(w, http.StatusNotFound, "app not found")
    return
  }
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request) {
  appID := chi.URLParam(r, "id")
  if appID == "" {
    writeError(w, http.StatusBadRequest, "missing app id")
    return
  }
  switch appID {
  case "lndg":
    if err := s.startLndg(r.Context()); err != nil {
      writeError(w, http.StatusInternalServerError, err.Error())
      return
    }
  default:
    writeError(w, http.StatusNotFound, "app not found")
    return
  }
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request) {
  appID := chi.URLParam(r, "id")
  if appID == "" {
    writeError(w, http.StatusBadRequest, "missing app id")
    return
  }
  switch appID {
  case "lndg":
    if err := s.stopLndg(r.Context()); err != nil {
      writeError(w, http.StatusInternalServerError, err.Error())
      return
    }
  default:
    writeError(w, http.StatusNotFound, "app not found")
    return
  }
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) getAppInfo(ctx context.Context, def appDefinition) appInfo {
  info := appInfo{
    ID: def.ID,
    Name: def.Name,
    Description: def.Description,
    Installed: false,
    Status: "not_installed",
    Port: def.Port,
  }
  if def.ID != "lndg" {
    return info
  }
  paths := lndgAppPaths()
  if fileExists(paths.ComposePath) {
    info.Installed = true
    info.AdminPasswordPath = paths.AdminPasswordPath
    status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "lndg")
    if err != nil {
      info.Status = "unknown"
    } else {
      info.Status = status
    }
  }
  return info
}

func (s *Server) installLndg(ctx context.Context) error {
  if err := ensureDocker(ctx); err != nil {
    return err
  }
  paths := lndgAppPaths()
  if err := os.MkdirAll(paths.Root, 0750); err != nil {
    return fmt.Errorf("failed to create app directory: %w", err)
  }
  if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
    return fmt.Errorf("failed to create app data directory: %w", err)
  }
  if err := os.MkdirAll(paths.PgDir, 0750); err != nil {
    return fmt.Errorf("failed to create app db directory: %w", err)
  }

  if err := ensureFile(paths.DockerfilePath, lndgDockerfile); err != nil {
    return err
  }
  if err := ensureFile(paths.EntrypointPath, lndgEntrypoint); err != nil {
    return err
  }
  if err := ensureFile(paths.ComposePath, lndgComposeContents(paths)); err != nil {
    return err
  }

  if err := ensureLndgEnv(paths); err != nil {
    return err
  }

  if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "lndg-db"); err != nil {
    return err
  }
  if err := syncLndgDbPassword(ctx, paths); err != nil {
    return err
  }
  if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "--build", "lndg"); err != nil {
    return err
  }
  return nil
}

func (s *Server) uninstallLndg(ctx context.Context) error {
  paths := lndgAppPaths()
  if fileExists(paths.ComposePath) {
    _ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
  }
  if err := os.RemoveAll(paths.Root); err != nil {
    return fmt.Errorf("failed to remove app files: %w", err)
  }
  return nil
}

func (s *Server) startLndg(ctx context.Context) error {
  paths := lndgAppPaths()
  if err := os.MkdirAll(paths.Root, 0750); err != nil {
    return fmt.Errorf("failed to create app directory: %w", err)
  }
  if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
    return fmt.Errorf("failed to create app data directory: %w", err)
  }
  if err := os.MkdirAll(paths.PgDir, 0750); err != nil {
    return fmt.Errorf("failed to create app db directory: %w", err)
  }
  if err := ensureFile(paths.DockerfilePath, lndgDockerfile); err != nil {
    return err
  }
  if err := ensureFile(paths.EntrypointPath, lndgEntrypoint); err != nil {
    return err
  }
  if err := ensureFile(paths.ComposePath, lndgComposeContents(paths)); err != nil {
    return err
  }
  if err := ensureLndgEnv(paths); err != nil {
    return err
  }
  if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "lndg-db"); err != nil {
    return err
  }
  if err := syncLndgDbPassword(ctx, paths); err != nil {
    return err
  }
  return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "lndg")
}

func (s *Server) stopLndg(ctx context.Context) error {
  paths := lndgAppPaths()
  if !fileExists(paths.ComposePath) {
    return errors.New("LNDg is not installed")
  }
  return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func lndgComposeContents(paths lndgPaths) string {
  return fmt.Sprintf(`services:
  lndg-db:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_USER: lndg
      POSTGRES_PASSWORD: ${LNDG_DB_PASSWORD}
      POSTGRES_DB: lndg
    volumes:
      - %s:/var/lib/postgresql/data

  lndg:
    build: .
    restart: unless-stopped
    depends_on:
      - lndg-db
    env_file:
      - ./.env
    environment:
      LNDG_DB_PASSWORD: ${LNDG_DB_PASSWORD}
      LNDG_ADMIN_PASSWORD: ${LNDG_ADMIN_PASSWORD}
      LNDG_ADMIN_USER: ${LNDG_ADMIN_USER}
      LNDG_NETWORK: ${LNDG_NETWORK}
      LNDG_RPC_SERVER: ${LNDG_RPC_SERVER}
      LNDG_LND_DIR: ${LNDG_LND_DIR}
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "8889:8889"
    volumes:
      - /data/lnd:/root/.lnd:ro
      - %s:/app/data:rw
      - %s:/var/log/lndg-controller.log:rw
`, paths.PgDir, paths.DataDir, filepath.Join(paths.DataDir, "lndg-controller.log"))
}

func ensureLndgEnv(paths lndgPaths) error {
  if fileExists(paths.EnvPath) {
    if !fileExists(paths.AdminPasswordPath) {
      adminPassword := readEnvValue(paths.EnvPath, "LNDG_ADMIN_PASSWORD")
      if adminPassword == "" {
        adminPassword = readSecretFile(paths.AdminPasswordPath)
        if adminPassword != "" {
          if err := appendEnvLine(paths.EnvPath, "LNDG_ADMIN_PASSWORD", adminPassword); err != nil {
            return err
          }
        }
      }
      if adminPassword == "" {
        return errors.New("LNDG_ADMIN_PASSWORD missing from .env")
      }
      if err := writeFile(paths.AdminPasswordPath, adminPassword+"\n", 0600); err != nil {
        return err
      }
    }
    if !fileExists(paths.DbPasswordPath) {
      dbPassword := readEnvValue(paths.EnvPath, "LNDG_DB_PASSWORD")
      if dbPassword == "" {
        dbPassword = readSecretFile(paths.DbPasswordPath)
        if dbPassword != "" {
          if err := appendEnvLine(paths.EnvPath, "LNDG_DB_PASSWORD", dbPassword); err != nil {
            return err
          }
        }
      }
      if dbPassword == "" {
        return errors.New("LNDG_DB_PASSWORD missing from .env")
      }
      if err := writeFile(paths.DbPasswordPath, dbPassword+"\n", 0600); err != nil {
        return err
      }
    }
    return nil
  }
  adminPassword := readSecretFile(paths.AdminPasswordPath)
  if adminPassword == "" {
    var err error
    adminPassword, err = randomToken(20)
    if err != nil {
      return err
    }
  }
  dbPassword := readSecretFile(paths.DbPasswordPath)
  if dbPassword == "" {
    var err error
    dbPassword, err = randomToken(24)
    if err != nil {
      return err
    }
  }
  env := strings.Join([]string{
    "LNDG_ADMIN_USER=lndg-admin",
    "LNDG_ADMIN_PASSWORD=" + adminPassword,
    "LNDG_DB_PASSWORD=" + dbPassword,
    "LNDG_NETWORK=mainnet",
    "LNDG_RPC_SERVER=host.docker.internal:10009",
    "LNDG_LND_DIR=/root/.lnd",
    "",
  }, "\n")
  if err := writeFile(paths.EnvPath, env, 0600); err != nil {
    return err
  }
  if !fileExists(paths.AdminPasswordPath) {
    if err := writeFile(paths.AdminPasswordPath, adminPassword+"\n", 0600); err != nil {
      return err
    }
  }
  if !fileExists(paths.DbPasswordPath) {
    if err := writeFile(paths.DbPasswordPath, dbPassword+"\n", 0600); err != nil {
      return err
    }
  }
  return nil
}

func ensureDocker(ctx context.Context) error {
  if _, err := exec.LookPath("docker"); err == nil {
    if _, infoErr := system.RunCommandWithSudo(ctx, "docker", "info"); infoErr == nil {
      if err := ensureCompose(ctx); err != nil {
        return err
      }
      return nil
    }
    if _, startErr := system.RunCommandWithSudo(ctx, "systemctl", "enable", "--now", "docker"); startErr == nil || isDockerActive(ctx) {
      if err := ensureCompose(ctx); err != nil {
        return err
      }
      return nil
    }
  }
  if err := installDocker(ctx); err != nil {
    return err
  }
  return ensureCompose(ctx)
}

func installDocker(ctx context.Context) error {
  if _, err := runApt(ctx, "update"); err != nil {
    return err
  }
  out, err := runApt(ctx, "install", "-y", "docker.io", "docker-compose-plugin")
  if err != nil {
    if strings.Contains(out, "Unable to locate package docker-compose-plugin") {
      out, err = runApt(ctx, "install", "-y", "docker.io", "docker-compose")
    }
    if err != nil {
      return fmt.Errorf("docker install failed: %s", strings.TrimSpace(out))
    }
  }
  if _, err := system.RunCommandWithSudo(ctx, "systemctl", "enable", "--now", "docker"); err != nil {
    if isDockerActive(ctx) {
      return nil
    }
    return fmt.Errorf("failed to start docker: %w", err)
  }
  return nil
}

func isDockerActive(ctx context.Context) bool {
  out, err := system.RunCommandWithSudo(ctx, "systemctl", "is-active", "docker")
  if err != nil {
    return false
  }
  return strings.TrimSpace(out) == "active"
}

func ensureCompose(ctx context.Context) error {
  if _, _, err := resolveCompose(ctx); err == nil {
    return nil
  }
  _, err := runApt(ctx, "install", "-y", "docker-compose-plugin")
  if err != nil && strings.Contains(err.Error(), "passwordless sudo") {
    return err
  }
  _, err = runApt(ctx, "install", "-y", "docker-compose")
  if err != nil && strings.Contains(err.Error(), "passwordless sudo") {
    return err
  }
  if err := installComposePluginBinary(ctx); err != nil {
    if strings.Contains(err.Error(), "passwordless sudo") {
      return err
    }
  }
  if _, _, err := resolveCompose(ctx); err != nil {
    return err
  }
  return nil
}

func runApt(ctx context.Context, args ...string) (string, error) {
  var out string
  for attempt := 0; attempt < 10; attempt++ {
    var err error
    out, err = runAptOnce(ctx, args...)
    if err == nil {
      return out, nil
    }
    if strings.Contains(out, "password is required") {
      return out, errors.New("docker install needs passwordless sudo for lightningos (re-run install.sh or add /etc/sudoers.d/lightningos)")
    }
    if strings.Contains(out, "Could not get lock") || strings.Contains(out, "dpkg frontend lock") || strings.Contains(out, "dpkg/lock") {
      time.Sleep(3 * time.Second)
      continue
    }
    return out, fmt.Errorf("apt-get failed: %s", strings.TrimSpace(out))
  }
  return out, errors.New("apt-get blocked by dpkg lock")
}

func runAptOnce(ctx context.Context, args ...string) (string, error) {
  aptPath := "/usr/bin/apt-get"
  systemdArgs := append([]string{"--wait", "--pipe", "--collect", aptPath}, args...)
  out, err := system.RunCommandWithSudo(ctx, "systemd-run", systemdArgs...)
  if err == nil {
    return out, nil
  }
  if strings.Contains(out, "password is required") {
    return out, err
  }
  fallbackOut, fallbackErr := system.RunCommandWithSudo(ctx, "apt-get", args...)
  if fallbackErr == nil {
    return fallbackOut, nil
  }
  if strings.TrimSpace(fallbackOut) == "" {
    return out, err
  }
  return fallbackOut, fallbackErr
}

func composeBaseArgs(appRoot string, composePath string) []string {
  envPath := filepath.Join(appRoot, ".env")
  args := []string{}
  if fileExists(envPath) {
    args = append(args, "--env-file", envPath)
  }
  args = append(args, "--project-directory", appRoot, "-f", composePath)
  return args
}

func runCompose(ctx context.Context, appRoot string, composePath string, args ...string) error {
  cmd, baseArgs, err := resolveCompose(ctx)
  if err != nil {
    return err
  }
  fullArgs := append(baseArgs, composeBaseArgs(appRoot, composePath)...)
  fullArgs = append(fullArgs, args...)
  if _, err := system.RunCommandWithSudo(ctx, cmd, fullArgs...); err != nil {
    return err
  }
  return nil
}

func getComposeStatus(ctx context.Context, appRoot string, composePath string, service string) (string, error) {
  cmd, baseArgs, err := resolveCompose(ctx)
  if err != nil {
    return "unknown", err
  }
  fullArgs := append(baseArgs, composeBaseArgs(appRoot, composePath)...)
  fullArgs = append(fullArgs, "ps", "--services", "--filter", "status=running")
  out, err := system.RunCommandWithSudo(ctx, cmd, fullArgs...)
  if err != nil {
    return "unknown", err
  }
  for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
    if strings.TrimSpace(line) == service {
      return "running", nil
    }
  }
  return "stopped", nil
}

func composeContainerID(ctx context.Context, appRoot string, composePath string, service string) (string, error) {
  cmd, baseArgs, err := resolveCompose(ctx)
  if err != nil {
    return "", err
  }
  fullArgs := append(baseArgs, composeBaseArgs(appRoot, composePath)...)
  fullArgs = append(fullArgs, "ps", "-q", service)
  out, err := system.RunCommandWithSudo(ctx, cmd, fullArgs...)
  if err != nil {
    return "", err
  }
  return strings.TrimSpace(out), nil
}

func syncLndgDbPassword(ctx context.Context, paths lndgPaths) error {
  password := readEnvValue(paths.EnvPath, "LNDG_DB_PASSWORD")
  if password == "" {
    password = readSecretFile(paths.DbPasswordPath)
  }
  if password == "" {
    return errors.New("LNDG_DB_PASSWORD missing")
  }
  containerID, err := composeContainerID(ctx, paths.Root, paths.ComposePath, "lndg-db")
  if err != nil {
    return err
  }
  if containerID == "" {
    return errors.New("lndg-db container not running")
  }
  escaped := strings.ReplaceAll(password, "'", "''")
  cmd := fmt.Sprintf("export PGPASSWORD=\"$POSTGRES_PASSWORD\"; PGUSER=\"${POSTGRES_USER:-postgres}\"; psql -U \"$PGUSER\" -h 127.0.0.1 -d postgres -v ON_ERROR_STOP=1 -c \"ALTER USER lndg WITH PASSWORD '%s';\" || psql -U \"$PGUSER\" -h 127.0.0.1 -d postgres -v ON_ERROR_STOP=1 -c \"CREATE USER lndg WITH PASSWORD '%s';\"", escaped, escaped)
  var lastErr error
  var lastOut string
  for attempt := 0; attempt < 10; attempt++ {
    out, err := system.RunCommandWithSudo(ctx, "docker", "exec", "-i", containerID, "sh", "-c", cmd)
    if err == nil {
      return nil
    }
    lastErr = err
    lastOut = strings.TrimSpace(out)
    time.Sleep(2 * time.Second)
  }
  if lastErr != nil {
    if lastOut != "" {
      return fmt.Errorf("failed to sync lndg db password: %s", lastOut)
    }
    return fmt.Errorf("failed to sync lndg db password: %w", lastErr)
  }
  return nil
}

func resolveCompose(ctx context.Context) (string, []string, error) {
  out, err := system.RunCommandWithSudo(ctx, "docker", "compose", "version")
  if err == nil {
    return "docker", []string{"compose"}, nil
  }
  if strings.Contains(out, "password is required") || strings.Contains(err.Error(), "password is required") {
    return "", nil, errors.New("docker compose requires passwordless sudo for lightningos")
  }
  out, err = system.RunCommandWithSudo(ctx, "docker-compose", "version")
  if err == nil {
    return "docker-compose", []string{}, nil
  }
  if strings.Contains(out, "password is required") || strings.Contains(err.Error(), "password is required") {
    return "", nil, errors.New("docker-compose requires passwordless sudo for lightningos")
  }
  return "", nil, errors.New("docker compose not available (install docker-compose-plugin or docker-compose)")
}

func ensureFile(path string, content string) error {
  if fileExists(path) {
    current, err := os.ReadFile(path)
    if err == nil && string(current) == content {
      return nil
    }
  }
  return writeFile(path, content, 0640)
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

type composeRelease struct {
  TagName string `json:"tag_name"`
}

func installComposePluginBinary(ctx context.Context) error {
  if fileExists("/usr/lib/docker/cli-plugins/docker-compose") || fileExists("/usr/local/lib/docker/cli-plugins/docker-compose") {
    return nil
  }
  tag := fetchLatestComposeTag(ctx)
  if tag == "" {
    tag = "v2.32.4"
  }
  arch := mapComposeArch(runtime.GOARCH)
  if arch == "" {
    return fmt.Errorf("unsupported architecture for docker compose: %s", runtime.GOARCH)
  }
  url := fmt.Sprintf("https://github.com/docker/compose/releases/download/%s/docker-compose-linux-%s", tag, arch)
  if _, err := exec.LookPath("curl"); err != nil {
    if _, err := runApt(ctx, "install", "-y", "curl"); err != nil {
      return err
    }
  }
  targetPath := "/usr/local/lib/docker/cli-plugins/docker-compose"
  script := fmt.Sprintf("mkdir -p /usr/local/lib/docker/cli-plugins && curl -fsSL -o %s %s && chmod 0755 %s", targetPath, url, targetPath)
  if _, err := system.RunCommandWithSudo(ctx, "systemd-run", "--wait", "--pipe", "--collect", "/bin/sh", "-c", script); err == nil {
    return nil
  }
  targetPath = "/usr/lib/docker/cli-plugins/docker-compose"
  script = fmt.Sprintf("mkdir -p /usr/lib/docker/cli-plugins && curl -fsSL -o %s %s && chmod 0755 %s", targetPath, url, targetPath)
  if _, err := system.RunCommandWithSudo(ctx, "systemd-run", "--wait", "--pipe", "--collect", "/bin/sh", "-c", script); err == nil {
    return nil
  }
  return errors.New("failed to install docker compose plugin binary")
}

func fetchLatestComposeTag(ctx context.Context) string {
  req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/docker/compose/releases/latest", nil)
  if err != nil {
    return ""
  }
  resp, err := http.DefaultClient.Do(req)
  if err != nil {
    return ""
  }
  defer resp.Body.Close()
  if resp.StatusCode < 200 || resp.StatusCode >= 300 {
    return ""
  }
  body, err := io.ReadAll(resp.Body)
  if err != nil {
    return ""
  }
  var release composeRelease
  if err := json.Unmarshal(body, &release); err != nil {
    return ""
  }
  return strings.TrimSpace(release.TagName)
}

func mapComposeArch(goarch string) string {
  switch goarch {
  case "amd64":
    return "x86_64"
  case "arm64":
    return "aarch64"
  default:
    return ""
  }
}

const lndgDockerfile = `FROM python:3.11-slim
ENV PYTHONUNBUFFERED=1
RUN apt-get update && apt-get install -y git gcc libpq-dev postgresql-client && rm -rf /var/lib/apt/lists/*
RUN git clone https://github.com/cryptosharks131/lndg /app
WORKDIR /app
RUN git checkout "master"
RUN pip install -r requirements.txt
RUN pip install supervisor whitenoise psycopg2-binary
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
`

const lndgEntrypoint = `#!/bin/sh
set -e

DATA_DIR=/app/data
SETTINGS_FILE=/app/lndg/settings.py
ADMIN_FILE="$DATA_DIR/lndg-admin.txt"

: "${LNDG_LND_DIR:=/root/.lnd}"
: "${LNDG_NETWORK:=mainnet}"
: "${LNDG_RPC_SERVER:=host.docker.internal:10009}"
: "${LNDG_ADMIN_USER:=lndg-admin}"

mkdir -p "$DATA_DIR"

  if [ ! -f "$SETTINGS_FILE" ]; then
  python initialize.py -d -net "$LNDG_NETWORK" -rpc "$LNDG_RPC_SERVER" -dir "$LNDG_LND_DIR" -wn -f
fi

python - <<'PY'
import os

path = "/app/lndg/settings.py"
raw = open(path, "r", encoding="utf-8").read().splitlines()
start = None
depth = 0
end = None

for i, line in enumerate(raw):
  if start is None and line.strip().startswith("DATABASES"):
    start = i
  if start is not None:
    depth += line.count("{") - line.count("}")
    if depth == 0 and i > start:
      end = i
      break

if start is None or end is None:
  raise SystemExit("Unable to locate DATABASES block")

db_password = os.environ.get("LNDG_DB_PASSWORD", "")
if not db_password:
  raise SystemExit("LNDG_DB_PASSWORD is required")

replacement = [
  "DATABASES = {",
  "    'default': {",
  "        'ENGINE': 'django.db.backends.postgresql_psycopg2',",
  "        'NAME': 'lndg',",
  "        'USER': 'lndg',",
  "        'PASSWORD': '" + db_password + "',",
  "        'HOST': 'lndg-db',",
  "        'PORT': '5432',",
  "    }",
  "}",
]

raw = raw[:start] + replacement + raw[end+1:]
with open(path, "w", encoding="utf-8") as f:
  f.write("\n".join(raw))
PY

until pg_isready -h lndg-db -U lndg > /dev/null 2>&1; do
  sleep 2
done

python manage.py migrate
python manage.py collectstatic --noinput

python - <<'PY'
import os
import sys

sys.path.insert(0, "/app")
os.environ.setdefault("DJANGO_SETTINGS_MODULE", "lndg.settings")

import django  # noqa: E402
django.setup()

from django.contrib.auth import get_user_model  # noqa: E402

username = os.environ.get("LNDG_ADMIN_USER", "lndg-admin")
password = os.environ.get("LNDG_ADMIN_PASSWORD", "")
if not password:
  raise SystemExit("LNDG_ADMIN_PASSWORD is required")

User = get_user_model()
user, _ = User.objects.get_or_create(username=username, defaults={"email": "admin@lndg.local"})
user.set_password(password)
user.is_staff = True
user.is_superuser = True
user.save()
PY

if [ ! -f "$ADMIN_FILE" ]; then
  printf "%s\n" "$LNDG_ADMIN_PASSWORD" > "$ADMIN_FILE"
fi

exec python controller.py runserver 0.0.0.0:8889
`
