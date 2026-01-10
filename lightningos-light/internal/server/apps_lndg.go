package server

import (
  "context"
  "crypto/sha256"
  "encoding/hex"
  "errors"
  "fmt"
  "os"
  "path/filepath"
  "strings"
  "time"

  "lightningos-light/internal/system"
)

type lndgPaths struct {
  Root string
  DataDir string
  PgDir string
  LogPath string
  ComposePath string
  EnvPath string
  DockerfilePath string
  EntrypointPath string
  AdminPasswordPath string
  DbPasswordPath string
  BuildHashPath string
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
  logPath := filepath.Join(dataDir, "lndg-controller.log")
  return lndgPaths{
    Root: root,
    DataDir: dataDir,
    PgDir: pgDir,
    LogPath: logPath,
    ComposePath: filepath.Join(root, "docker-compose.yaml"),
    EnvPath: filepath.Join(root, ".env"),
    DockerfilePath: filepath.Join(root, "Dockerfile"),
    EntrypointPath: filepath.Join(root, "entrypoint.sh"),
    AdminPasswordPath: filepath.Join(dataDir, "lndg-admin.txt"),
    DbPasswordPath: filepath.Join(dataDir, "lndg-db-password.txt"),
    BuildHashPath: filepath.Join(root, ".build_hash"),
  }
}

func (s *Server) installLndg(ctx context.Context) error {
  if err := ensureDocker(ctx); err != nil {
    return err
  }
  if err := ensureLndgGrpcAccess(ctx); err != nil {
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
  if err := ensureLndgLogFile(paths.LogPath); err != nil {
    return err
  }

  currentHash := lndgBuildHash()
  if _, err := ensureFileWithChange(paths.DockerfilePath, lndgDockerfile); err != nil {
    return err
  }
  if _, err := ensureFileWithChange(paths.EntrypointPath, lndgEntrypoint); err != nil {
    return err
  }
  if _, err := ensureFileWithChange(paths.ComposePath, lndgComposeContents(paths)); err != nil {
    return err
  }

  if err := ensureLndgEnv(ctx, paths); err != nil {
    return err
  }
  buildKey := lndgBuildKey(paths, currentHash)

  if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "lndg-db"); err != nil {
    return err
  }
  if err := syncLndgDbPassword(ctx, paths); err != nil {
    return err
  }
  if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "--build", "lndg"); err != nil {
    return err
  }
  _ = writeFile(paths.BuildHashPath, buildKey+"\n", 0640)
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
  if err := ensureLndgGrpcAccess(ctx); err != nil {
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
  if err := ensureLndgLogFile(paths.LogPath); err != nil {
    return err
  }
  needsBuild := false
  currentHash := lndgBuildHash()
  if changed, err := ensureFileWithChange(paths.DockerfilePath, lndgDockerfile); err != nil {
    return err
  } else if changed {
    needsBuild = true
  }
  if changed, err := ensureFileWithChange(paths.EntrypointPath, lndgEntrypoint); err != nil {
    return err
  } else if changed {
    needsBuild = true
  }
  if _, err := ensureFileWithChange(paths.ComposePath, lndgComposeContents(paths)); err != nil {
    return err
  }
  if err := ensureLndgEnv(ctx, paths); err != nil {
    return err
  }
  buildKey := lndgBuildKey(paths, currentHash)
  if readSecretFile(paths.BuildHashPath) != buildKey {
    needsBuild = true
  }
  if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "lndg-db"); err != nil {
    return err
  }
  if err := syncLndgDbPassword(ctx, paths); err != nil {
    return err
  }
  if needsBuild {
    if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d", "--build", "lndg"); err != nil {
      return err
    }
    _ = writeFile(paths.BuildHashPath, buildKey+"\n", 0640)
    return nil
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
    build:
      context: .
      args:
        LNDG_GIT_REF: ${LNDG_GIT_REF}
        LNDG_GIT_SHA: ${LNDG_GIT_SHA}
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
      LNDG_ALLOWED_HOSTS: ${LNDG_ALLOWED_HOSTS}
      LNDG_CSRF_TRUSTED_ORIGINS: ${LNDG_CSRF_TRUSTED_ORIGINS}
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "8889:8889"
    volumes:
      - /data/lnd:/root/.lnd:ro
      - %s:/app/data:rw
      - %s:/var/log/lndg-controller.log:rw
`, paths.PgDir, paths.DataDir, paths.LogPath)
}

func ensureLndgEnv(ctx context.Context, paths lndgPaths) error {
  allowedHosts, csrfOrigins := defaultLndgHosts(ctx)
  allowedHostsValue := strings.Join(allowedHosts, ",")
  csrfOriginsValue := strings.Join(csrfOrigins, ",")
  if fileExists(paths.EnvPath) {
    existingRef := readEnvValue(paths.EnvPath, "LNDG_GIT_REF")
    existingSha := readEnvValue(paths.EnvPath, "LNDG_GIT_SHA")
    gitRef, gitSha := resolveLndgGit(ctx, existingRef, existingSha)
    if existingRef == "" || gitRef != existingRef {
      if err := setEnvValue(paths.EnvPath, "LNDG_GIT_REF", gitRef); err != nil {
        return err
      }
    }
    if gitSha != "" && gitSha != existingSha {
      if err := setEnvValue(paths.EnvPath, "LNDG_GIT_SHA", gitSha); err != nil {
        return err
      }
    }
    if readEnvValue(paths.EnvPath, "LNDG_CUSTOM_COOKIES") == "" {
      if err := appendEnvLine(paths.EnvPath, "LNDG_CUSTOM_COOKIES", "1"); err != nil {
        return err
      }
    }
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
    if allowedHostsValue != "" {
      existing := splitEnvList(readEnvValue(paths.EnvPath, "LNDG_ALLOWED_HOSTS"))
      merged := mergeUnique(existing, allowedHosts)
      mergedValue := strings.Join(merged, ",")
      if mergedValue != "" && mergedValue != strings.Join(existing, ",") {
        if err := setEnvValue(paths.EnvPath, "LNDG_ALLOWED_HOSTS", mergedValue); err != nil {
          return err
        }
      }
    }
    if csrfOriginsValue != "" {
      existing := splitEnvList(readEnvValue(paths.EnvPath, "LNDG_CSRF_TRUSTED_ORIGINS"))
      merged := mergeUnique(existing, csrfOrigins)
      mergedValue := strings.Join(merged, ",")
      if mergedValue != "" && mergedValue != strings.Join(existing, ",") {
        if err := setEnvValue(paths.EnvPath, "LNDG_CSRF_TRUSTED_ORIGINS", mergedValue); err != nil {
          return err
        }
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
  gitRef, gitSha := resolveLndgGit(ctx, "", "")
  env := strings.Join([]string{
    "LNDG_ADMIN_USER=lndg-admin",
    "LNDG_ADMIN_PASSWORD=" + adminPassword,
    "LNDG_DB_PASSWORD=" + dbPassword,
    "LNDG_NETWORK=mainnet",
    "LNDG_RPC_SERVER=host.docker.internal:10009",
    "LNDG_LND_DIR=/root/.lnd",
    "LNDG_GIT_REF=" + gitRef,
    "LNDG_GIT_SHA=" + gitSha,
    "LNDG_CUSTOM_COOKIES=1",
    "LNDG_ALLOWED_HOSTS=" + allowedHostsValue,
    "LNDG_CSRF_TRUSTED_ORIGINS=" + csrfOriginsValue,
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

func ensureLndgGrpcAccess(ctx context.Context) error {
  gatewayIP, err := dockerGatewayIP(ctx)
  if err != nil {
    return err
  }
  content, err := os.ReadFile(lndConfPath)
  if err != nil {
    return fmt.Errorf("failed to read lnd.conf: %w", err)
  }
  lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
  lines, changed := updateLndGrpcOptions(lines, gatewayIP)
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
    for i, token := range fields {
      if token == "inet" && i+1 < len(fields) {
        ip := strings.Split(fields[i+1], "/")[0]
        if ip != "" {
          return ip, nil
        }
      }
    }
  }
  return "", errors.New("unable to determine docker bridge gateway IP")
}

func updateLndGrpcOptions(lines []string, gateway string) ([]string, bool) {
  firstSection := len(lines)
  for i, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
      firstSection = i
      break
    }
  }
  top := lines[:firstSection]
  rest := lines[firstSection:]

  rpclistenOrder := []string{}
  rpclistenSet := map[string]bool{}
  filteredTop := []string{}
  for _, line := range top {
    trimmed := strings.TrimSpace(line)
    if trimmed == "" || strings.HasPrefix(trimmed, "#") {
      filteredTop = append(filteredTop, line)
      continue
    }
    if strings.HasPrefix(trimmed, "tlsextraip=") || strings.HasPrefix(trimmed, "tlsextradomain=") {
      continue
    }
    if strings.HasPrefix(trimmed, "rpclisten=") {
      value := strings.TrimSpace(strings.TrimPrefix(trimmed, "rpclisten="))
      if value != "" && !rpclistenSet[value] {
        rpclistenSet[value] = true
        rpclistenOrder = append(rpclistenOrder, value)
      }
      continue
    }
    filteredTop = append(filteredTop, line)
  }

  filteredRest := []string{}
  for _, line := range rest {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, "tlsextraip=") || strings.HasPrefix(trimmed, "tlsextradomain=") || strings.HasPrefix(trimmed, "rpclisten=") {
      continue
    }
    filteredRest = append(filteredRest, line)
  }

  desiredOrder := []string{"127.0.0.1:10009", gateway + ":10009"}
  for _, value := range desiredOrder {
    if !rpclistenSet[value] {
      rpclistenSet[value] = true
      rpclistenOrder = append([]string{value}, rpclistenOrder...)
    }
  }

  updated := append([]string{}, filteredTop...)
  updated = append(updated, fmt.Sprintf("tlsextraip=%s", gateway))
  updated = append(updated, "tlsextradomain=host.docker.internal")

  added := map[string]bool{}
  for _, value := range desiredOrder {
    if !added[value] {
      updated = append(updated, "rpclisten="+value)
      added[value] = true
    }
  }
  for _, value := range rpclistenOrder {
    if !added[value] {
      updated = append(updated, "rpclisten="+value)
      added[value] = true
    }
  }

  updated = append(updated, filteredRest...)

  changed := len(updated) != len(lines)
  if !changed {
    for i := range updated {
      if updated[i] != lines[i] {
        changed = true
        break
      }
    }
  }
  return updated, changed
}

func lndgBuildHash() string {
  sum := sha256.Sum256([]byte(lndgDockerfile + "\n" + lndgEntrypoint))
  return hex.EncodeToString(sum[:])
}

func lndgBuildKey(paths lndgPaths, base string) string {
  gitSha := readEnvValue(paths.EnvPath, "LNDG_GIT_SHA")
  if gitSha == "" {
    gitSha = "unknown"
  }
  return base + ":" + gitSha
}

func ensureLndgLogFile(path string) error {
  info, err := os.Stat(path)
  if err == nil {
    if info.IsDir() {
      entries, readErr := os.ReadDir(path)
      if readErr != nil {
        return fmt.Errorf("failed to inspect %s: %w", path, readErr)
      }
      if len(entries) == 0 {
        if err := os.Remove(path); err != nil {
          return fmt.Errorf("failed to remove %s: %w", path, err)
        }
      } else {
        backup := path + ".bak-" + time.Now().Format("20060102150405")
        if err := os.Rename(path, backup); err != nil {
          return fmt.Errorf("failed to move %s to %s: %w", path, backup, err)
        }
      }
    } else {
      return nil
    }
  } else if !os.IsNotExist(err) {
    return fmt.Errorf("failed to stat %s: %w", path, err)
  }
  file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
  if err != nil {
    return fmt.Errorf("failed to create %s: %w", path, err)
  }
  if err := file.Close(); err != nil {
    return fmt.Errorf("failed to close %s: %w", path, err)
  }
  return nil
}

func defaultLndgHosts(ctx context.Context) ([]string, []string) {
  hosts := []string{"localhost", "127.0.0.1", "host.docker.internal"}
  for _, ip := range detectHostIPs(ctx) {
    if !stringInSlice(ip, hosts) {
      hosts = append(hosts, ip)
    }
  }
  origins := []string{}
  for _, host := range hosts {
    for _, scheme := range []string{"http", "https"} {
      origin := fmt.Sprintf("%s://%s", scheme, host)
      if !stringInSlice(origin, origins) {
        origins = append(origins, origin)
      }
      originWithPort := fmt.Sprintf("%s://%s:8889", scheme, host)
      if !stringInSlice(originWithPort, origins) {
        origins = append(origins, originWithPort)
      }
    }
  }
  return hosts, origins
}

func detectHostIPs(ctx context.Context) []string {
  out, err := system.RunCommand(ctx, "ip", "-4", "-o", "addr", "show", "scope", "global")
  if err != nil {
    out, _ = system.RunCommand(ctx, "hostname", "-I")
  }
  ips := []string{}
  for _, line := range strings.Split(out, "\n") {
    if line == "" {
      continue
    }
    tokens := strings.Fields(line)
    for i, token := range tokens {
      if token == "inet" && i+1 < len(tokens) {
        ip := strings.Split(tokens[i+1], "/")[0]
        if ip != "" && !stringInSlice(ip, ips) {
          ips = append(ips, ip)
        }
      }
    }
    if strings.Contains(line, ".") && strings.Contains(line, "/") && strings.Count(line, ":") == 0 && strings.Count(line, " ") > 0 {
      for _, token := range tokens {
        if strings.Count(token, ".") == 3 && strings.Contains(token, "/") {
          ip := strings.Split(token, "/")[0]
          if ip != "" && !stringInSlice(ip, ips) {
            ips = append(ips, ip)
          }
        }
      }
    }
    if !strings.Contains(line, "inet") && strings.Count(line, ".") == 3 && !strings.Contains(line, "/") {
      if !stringInSlice(line, ips) {
        ips = append(ips, line)
      }
    }
  }
  return ips
}

func resolveLndgGit(ctx context.Context, existingRef string, existingSha string) (string, string) {
  ref := existingRef
  if ref == "" {
    ref = "master"
  }
  sha := lndgRemoteHead(ctx, ref)
  if sha == "" {
    sha = existingSha
  }
  if sha == "" {
    sha = "unknown"
  }
  return ref, sha
}

func lndgRemoteHead(ctx context.Context, ref string) string {
  if ref == "" {
    return ""
  }
  remoteRef := ref
  if !strings.HasPrefix(ref, "refs/") {
    remoteRef = "refs/heads/" + ref
  }
  out, err := system.RunCommand(ctx, "git", "ls-remote", "https://github.com/cryptosharks131/lndg", remoteRef)
  if err != nil {
    return ""
  }
  fields := strings.Fields(out)
  if len(fields) == 0 {
    return ""
  }
  return fields[0]
}

const lndgDockerfile = `FROM python:3.11-slim
ENV PYTHONUNBUFFERED=1
RUN apt-get update && apt-get install -y git gcc libpq-dev postgresql-client && rm -rf /var/lib/apt/lists/*
ARG LNDG_GIT_REF=master
ARG LNDG_GIT_SHA=unknown
RUN echo "LNDG_GIT_REF=$LNDG_GIT_REF LNDG_GIT_SHA=$LNDG_GIT_SHA"
RUN git clone --depth 1 --branch "$LNDG_GIT_REF" https://github.com/cryptosharks131/lndg /app
WORKDIR /app
RUN if [ -n "$LNDG_GIT_SHA" ] && [ "$LNDG_GIT_SHA" != "unknown" ]; then \
      git fetch --depth 1 origin "$LNDG_GIT_SHA" && git checkout "$LNDG_GIT_SHA"; \
    fi
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

strip_keys = (
  "ALLOWED_HOSTS =",
  "CSRF_TRUSTED_ORIGINS =",
  "CSRF_COOKIE_SECURE =",
  "SESSION_COOKIE_SECURE =",
  "CSRF_COOKIE_DOMAIN =",
  "SESSION_COOKIE_DOMAIN =",
  "CSRF_COOKIE_SAMESITE =",
  "SESSION_COOKIE_SAMESITE =",
  "CSRF_COOKIE_NAME =",
  "SESSION_COOKIE_NAME =",
  "CSRF_USE_SESSIONS =",
  "CSRF_FAILURE_VIEW =",
)
raw = [line for line in raw if not any(line.strip().startswith(key) for key in strip_keys)]

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

allowed_hosts = [h.strip() for h in os.environ.get("LNDG_ALLOWED_HOSTS", "").split(",") if h.strip()]
csrf_trusted = [o.strip() for o in os.environ.get("LNDG_CSRF_TRUSTED_ORIGINS", "").split(",") if o.strip()]

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
if allowed_hosts:
  raw += ["", "ALLOWED_HOSTS = " + repr(allowed_hosts)]
if csrf_trusted:
  raw += ["CSRF_TRUSTED_ORIGINS = " + repr(csrf_trusted)]
  if any(origin.startswith("http://") for origin in csrf_trusted):
    raw += ["CSRF_COOKIE_SECURE = False", "SESSION_COOKIE_SECURE = False"]
  raw += ["CSRF_COOKIE_DOMAIN = None", "SESSION_COOKIE_DOMAIN = None"]
  raw += ["CSRF_COOKIE_SAMESITE = 'Lax'", "SESSION_COOKIE_SAMESITE = 'Lax'"]
if os.environ.get("LNDG_CUSTOM_COOKIES") == "0":
  raw += ["CSRF_COOKIE_NAME = 'csrftoken'", "SESSION_COOKIE_NAME = 'sessionid'"]
else:
  raw += ["CSRF_COOKIE_NAME = 'lndg_csrftoken'", "SESSION_COOKIE_NAME = 'lndg_sessionid'"]
raw += ["CSRF_USE_SESSIONS = True"]

debug_path = "/app/lndg/csrf_debug.py"
debug_contents = """from django.views.csrf import csrf_failure as default_csrf_failure

def csrf_failure(request, reason=""):
    origin = request.META.get("HTTP_ORIGIN")
    referer = request.META.get("HTTP_REFERER")
    host = request.get_host()
    cookies = ",".join(request.COOKIES.keys())
    print(f"CSRF failure: reason={reason} origin={origin} referer={referer} host={host} cookies={cookies}", flush=True)
    return default_csrf_failure(request, reason)
"""
with open(debug_path, "w", encoding="utf-8") as f:
  f.write(debug_contents)
raw += ["CSRF_FAILURE_VIEW = 'lndg.csrf_debug.csrf_failure'"]
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

LOG_FILE=/var/log/lndg-controller.log
touch "$LOG_FILE"
exec sh -c "python controller.py runserver 0.0.0.0:8889 2>&1 | tee -a \"$LOG_FILE\""
`
