package appmanifest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	LNDgID            = "lndg"
	LNDgRelease       = "1.11.0"
	LNDgSourceCommit  = "0fe400029240fc59431b56b6ce47e24b764396b1"
	LNDgSourceSHA256  = "390789734f729608cdc54f3b356e26f98e6184570f4677ba4df273980eea5df4"
	LNDgSourceURL     = "https://codeload.github.com/cryptosharks131/lndg/tar.gz/" + LNDgSourceCommit
	LNDgSourceDir     = "lndg-" + LNDgSourceCommit
	LNDgBaseImage     = "python:3.12-slim@sha256:229a2c5bfa27522db7815ea81f9bed70af17ccb9de9fc7ad142b1877b5830d36"
	LNDgImage         = "lightningos/lndg:" + LNDgRelease
	LNDgSupervisor    = "4.3.0"
	LNDgWhitenoise    = "6.12.0"
	LNDgPsycopgBinary = "2.9.12"

	LNDgProject         = "lndg"
	LNDgComposeFile     = "docker-compose.yaml"
	LNDgEnvFile         = ".env"
	LNDgEntrypointFile  = "entrypoint.sh"
	LNDgPrimaryService  = "lndg"
	LNDgDatabaseService = "lndg-db"
	LNDgPort            = 8889
	LNDgStopTimeout     = 30
	LNDgLNDDir          = "lnd"
	LNDgTLSCertFile     = "tls.cert"
	LNDgMacaroonFile    = "lndg.macaroon"
	LNDgChannelDBFile   = "channel.db"
	LNDgChannelDBPath   = "/data/lnd/data/graph/mainnet/channel.db"
	LNDgContainerUID    = 1000
	LNDgContainerGID    = 1000

	// LNDgPostgresImage is the Docker Official Image release selected for the
	// LNDg database. The multi-architecture index digest prevents a mutable
	// postgres:16 tag from changing underneath an install. Trixie preserves
	// collation compatibility with volumes created by the former mutable tag.
	LNDgPostgresImage = "postgres:16.14-trixie@sha256:33f923b05f64ca54ac4401c01126a6b92afe839a0aa0a52bc5aeb5cc958e5f20"

	LNDgImageApp      AppImageVariant = "app"
	LNDgImagePostgres AppImageVariant = "postgres"
)

const LNDgEntrypoint = `#!/bin/sh
set -eu

DATA_DIR=/app/data
SETTINGS_FILE=/app/lndg/settings.py
ADMIN_FILE="$DATA_DIR/lndg-admin.txt"
SQLITE_FILE="$DATA_DIR/db.sqlite3"
MIGRATION_FIXTURE="$DATA_DIR/.lndg-sqlite-migration.json"
MIGRATION_MARKER="$DATA_DIR/.lndg-postgres-migrated"
SQLITE_SETTINGS_FILE=/app/lndg/sqlite_migration_settings.py

: "${LNDG_LND_DIR:=/root/.lnd}"
: "${LNDG_NETWORK:=mainnet}"
: "${LNDG_RPC_SERVER:=host.docker.internal:10009}"
: "${LNDG_TLS_PATH:=/etc/lnd/tls.cert}"
: "${LNDG_MACAROON_PATH:=/etc/lnd/lndg.macaroon}"
: "${LNDG_DATABASE_PATH:=/etc/lnd/channel.db}"
: "${LNDG_ADMIN_USER:=lndg-admin}"
: "${LNDG_ADMIN_PASSWORD:?LNDG_ADMIN_PASSWORD is required}"
: "${LNDG_DB_PASSWORD:?LNDG_DB_PASSWORD is required}"

mkdir -p "$DATA_DIR"

legacy_sqlite=false
if [ -s "$SQLITE_FILE" ]; then
  legacy_sqlite=true
fi

if [ ! -f "$SETTINGS_FILE" ]; then
  python initialize.py -d -net "$LNDG_NETWORK" -rpc "$LNDG_RPC_SERVER" -dir "$LNDG_LND_DIR" -tls "$LNDG_TLS_PATH" -mcrn "$LNDG_MACAROON_PATH" -lnddb "$LNDG_DATABASE_PATH" -u "$LNDG_ADMIN_USER" --adminpw="$LNDG_ADMIN_PASSWORD" -wn -f
fi

until pg_isready -h lndg-db -U lndg > /dev/null 2>&1; do
  sleep 2
done

if [ "$legacy_sqlite" = true ] && [ ! -f "$MIGRATION_MARKER" ]; then
  postgres_schema=$(PGPASSWORD="$LNDG_DB_PASSWORD" psql -h lndg-db -U lndg -d lndg -Atc "select to_regclass('public.django_migrations');")
  if [ ! -f "$MIGRATION_FIXTURE" ]; then
    if [ -n "$postgres_schema" ]; then
      echo "Refusing automatic SQLite import into an initialized PostgreSQL schema" >&2
      exit 1
    fi
    SETTINGS_FILE="$SETTINGS_FILE" SQLITE_SETTINGS_FILE="$SQLITE_SETTINGS_FILE" SQLITE_FILE="$SQLITE_FILE" python - <<'PY'
import os

source = os.environ["SETTINGS_FILE"]
target = os.environ["SQLITE_SETTINGS_FILE"]
sqlite_file = os.environ["SQLITE_FILE"]
raw = open(source, "r", encoding="utf-8").read().splitlines()
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
  raise SystemExit("Unable to locate DATABASES block for SQLite migration")
replacement = [
  "DATABASES = {",
  "    'default': {",
  "        'ENGINE': 'django.db.backends.sqlite3',",
  "        'NAME': " + repr(sqlite_file) + ",",
  "    }",
  "}",
]
raw = raw[:start] + replacement + raw[end+1:]
with open(target, "w", encoding="utf-8") as f:
  f.write("\n".join(raw) + "\n")
PY
    umask 077
    DJANGO_SETTINGS_MODULE=lndg.sqlite_migration_settings python manage.py dumpdata --natural-foreign --natural-primary \
      --exclude contenttypes --exclude auth.permission --exclude admin.logentry --exclude sessions \
      > "$MIGRATION_FIXTURE.tmp"
    mv "$MIGRATION_FIXTURE.tmp" "$MIGRATION_FIXTURE"
    rm -f "$SQLITE_SETTINGS_FILE"
  fi
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

allowed_hosts = [h.strip() for h in os.environ.get("LNDG_ALLOWED_HOSTS", "").split(",") if h.strip()]
csrf_trusted = [o.strip() for o in os.environ.get("LNDG_CSRF_TRUSTED_ORIGINS", "").split(",") if o.strip()]
if not csrf_trusted and allowed_hosts:
  for host in allowed_hosts:
    for scheme in ("http", "https"):
      csrf_trusted.append(f"{scheme}://{host}")
      csrf_trusted.append(f"{scheme}://{host}:8889")

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
filtered = []
for line in raw:
  stripped = line.strip()
  if (
    stripped.startswith("ALLOWED_HOSTS")
    or stripped.startswith("CSRF_TRUSTED_ORIGINS")
    or stripped.startswith("CSRF_COOKIE_SECURE")
    or stripped.startswith("SESSION_COOKIE_SECURE")
    or stripped.startswith("CSRF_COOKIE_SAMESITE")
    or stripped.startswith("SESSION_COOKIE_SAMESITE")
    or stripped.startswith("CSRF_COOKIE_DOMAIN")
    or stripped.startswith("SESSION_COOKIE_DOMAIN")
    or stripped.startswith("CSRF_COOKIE_NAME")
    or stripped.startswith("SESSION_COOKIE_NAME")
  ):
    continue
  filtered.append(line)
raw = filtered
if allowed_hosts:
  raw += ["", "ALLOWED_HOSTS = " + repr(allowed_hosts)]
if csrf_trusted:
  raw += ["CSRF_TRUSTED_ORIGINS = " + repr(csrf_trusted)]
raw += [
  "CSRF_COOKIE_SECURE = False",
  "SESSION_COOKIE_SECURE = False",
  "CSRF_COOKIE_DOMAIN = None",
  "SESSION_COOKIE_DOMAIN = None",
  "CSRF_COOKIE_SAMESITE = 'Lax'",
  "SESSION_COOKIE_SAMESITE = 'Lax'",
]
with open(path, "w", encoding="utf-8") as f:
  f.write("\n".join(raw))
PY

python manage.py migrate
if [ -f "$MIGRATION_FIXTURE" ] && [ ! -f "$MIGRATION_MARKER" ]; then
  python manage.py loaddata "$MIGRATION_FIXTURE"
  umask 077
  : > "$MIGRATION_MARKER"
  rm -f "$MIGRATION_FIXTURE"
fi
rm -f "$SQLITE_SETTINGS_FILE"
python manage.py collectstatic --noinput

python - <<'PY'
import os
import sys

sys.path.insert(0, "/app")
os.environ.setdefault("DJANGO_SETTINGS_MODULE", "lndg.settings")

import django
django.setup()

from django.contrib.auth import get_user_model

username = os.environ.get("LNDG_ADMIN_USER", "lndg-admin")
password = os.environ.get("LNDG_ADMIN_PASSWORD", "")
if not password:
  raise SystemExit("LNDG_ADMIN_PASSWORD is required")

User = get_user_model()
user, created = User.objects.get_or_create(username=username, defaults={"email": "admin@lndg.local"})
updated = False
if created:
  user.set_password(password)
  updated = True
if not user.is_staff:
  user.is_staff = True
  updated = True
if not user.is_superuser:
  user.is_superuser = True
  updated = True
if not user.has_usable_password():
  user.set_password(password)
  updated = True
if updated:
  user.save()
PY

if [ ! -f "$ADMIN_FILE" ]; then
  printf "%s\n" "$LNDG_ADMIN_PASSWORD" > "$ADMIN_FILE"
fi

LOG_FILE=/var/log/lndg-controller.log
touch "$LOG_FILE"
exec sh -c "python controller.py runserver 0.0.0.0:8889 2>&1 | tee -a \"$LOG_FILE\""
`

type LNDgComposePaths struct {
	DataDir        string
	PgDir          string
	LogPath        string
	LndDir         string
	ChannelDBPath  string
	EntrypointPath string
}

type LNDgRuntime struct {
	AdminPassword string
	DBPassword    string
	AllowedHosts  []string
}

func LNDgImageForVariant(variant AppImageVariant) (string, error) {
	switch variant {
	case LNDgImageApp:
		return LNDgImage, nil
	case LNDgImagePostgres:
		return LNDgPostgresImage, nil
	default:
		return "", errors.New("lndg image variant is not allowed")
	}
}

func LNDgImageVariants() []AppImageVariant {
	return []AppImageVariant{LNDgImageApp, LNDgImagePostgres}
}

func ValidateLNDgRuntime(runtime LNDgRuntime) error {
	if err := validateLNDgToken(runtime.AdminPassword, 20); err != nil {
		return errors.New("lndg admin credential is invalid")
	}
	if err := validateLNDgToken(runtime.DBPassword, 24); err != nil {
		return errors.New("lndg database credential is invalid")
	}
	if len(runtime.AllowedHosts) < 3 || len(runtime.AllowedHosts) > 16 {
		return errors.New("lndg allowed hosts are invalid")
	}
	base := []string{"localhost", "127.0.0.1", "host.docker.internal"}
	for index, host := range base {
		if runtime.AllowedHosts[index] != host {
			return errors.New("lndg allowed hosts are invalid")
		}
	}
	dynamic := append([]string(nil), runtime.AllowedHosts[len(base):]...)
	if !sort.StringsAreSorted(dynamic) {
		return errors.New("lndg allowed hosts are not canonical")
	}
	seen := make(map[string]struct{}, len(runtime.AllowedHosts))
	for index, host := range runtime.AllowedHosts {
		if host == "" || strings.ContainsAny(host, "\r\n\x00, =") {
			return errors.New("lndg allowed hosts are invalid")
		}
		if index >= len(base) {
			ip := net.ParseIP(host)
			if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() {
				return errors.New("lndg allowed host is not an IPv4 address")
			}
		}
		if _, exists := seen[host]; exists {
			return errors.New("lndg allowed hosts contain duplicates")
		}
		seen[host] = struct{}{}
	}
	return nil
}

func LNDgRuntimeEnv(runtime LNDgRuntime) (string, error) {
	runtime.AllowedHosts = canonicalLNDgHosts(runtime.AllowedHosts)
	if err := ValidateLNDgRuntime(runtime); err != nil {
		return "", err
	}
	origins := lndgOrigins(runtime.AllowedHosts)
	return strings.Join([]string{
		"LNDG_ADMIN_USER=lndg-admin",
		"LNDG_ADMIN_PASSWORD=" + runtime.AdminPassword,
		"LNDG_DB_PASSWORD=" + runtime.DBPassword,
		"LNDG_NETWORK=mainnet",
		"LNDG_RPC_SERVER=host.docker.internal:10009",
		"LNDG_LND_DIR=/root/.lnd",
		"LNDG_GIT_REF=" + LNDgRelease,
		"LNDG_GIT_SHA=" + LNDgSourceCommit,
		"LNDG_ALLOWED_HOSTS=" + strings.Join(runtime.AllowedHosts, ","),
		"LNDG_CSRF_TRUSTED_ORIGINS=" + strings.Join(origins, ","),
		"",
	}, "\n"), nil
}

func ParseLNDgRuntimeEnv(raw []byte) (LNDgRuntime, error) {
	var runtime LNDgRuntime
	if len(raw) == 0 || len(raw) > 8192 || raw[len(raw)-1] != '\n' || strings.ContainsAny(string(raw), "\r\x00") {
		return runtime, errors.New("invalid lndg environment encoding")
	}
	values := make(map[string]string)
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines[:len(lines)-1] {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return runtime, errors.New("invalid lndg environment entry")
		}
		if _, exists := values[key]; exists {
			return runtime, errors.New("duplicate lndg environment entry")
		}
		values[key] = value
	}
	allowed := map[string]string{
		"LNDG_ADMIN_USER": "lndg-admin",
		"LNDG_NETWORK":    "mainnet",
		"LNDG_RPC_SERVER": "host.docker.internal:10009",
		"LNDG_LND_DIR":    "/root/.lnd",
		"LNDG_GIT_REF":    LNDgRelease,
		"LNDG_GIT_SHA":    LNDgSourceCommit,
	}
	if len(values) != 10 {
		return runtime, errors.New("lndg environment is incomplete")
	}
	for key, expected := range allowed {
		if values[key] != expected {
			return runtime, errors.New("lndg environment does not match the catalog")
		}
	}
	hosts := strings.Split(values["LNDG_ALLOWED_HOSTS"], ",")
	runtime = LNDgRuntime{
		AdminPassword: values["LNDG_ADMIN_PASSWORD"],
		DBPassword:    values["LNDG_DB_PASSWORD"],
		AllowedHosts:  hosts,
	}
	if err := ValidateLNDgRuntime(runtime); err != nil {
		return LNDgRuntime{}, err
	}
	if values["LNDG_CSRF_TRUSTED_ORIGINS"] != strings.Join(lndgOrigins(hosts), ",") {
		return LNDgRuntime{}, errors.New("lndg CSRF origins do not match allowed hosts")
	}
	expected, _ := LNDgRuntimeEnv(runtime)
	if string(raw) != expected {
		return LNDgRuntime{}, errors.New("lndg environment is not canonical")
	}
	return runtime, nil
}

func LNDgCompose(paths LNDgComposePaths) string {
	channelDBPath := paths.ChannelDBPath
	if channelDBPath == "" {
		channelDBPath = LNDgChannelDBPath
	}
	return fmt.Sprintf(`services:
  lndg-db:
    image: %s
    container_name: lndg-db
    restart: unless-stopped
    stop_grace_period: %ds
    security_opt:
      - no-new-privileges:true
    environment:
      POSTGRES_USER: lndg
      POSTGRES_PASSWORD: ${LNDG_DB_PASSWORD}
      POSTGRES_DB: lndg
    expose:
      - "5432"
    volumes:
      - %s:/var/lib/postgresql/data

  lndg:
    image: %s
    container_name: lndg
    user: "%d:%d"
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
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
      LNDG_TLS_PATH: /etc/lnd/tls.cert
      LNDG_MACAROON_PATH: /etc/lnd/lndg.macaroon
      LNDG_DATABASE_PATH: /etc/lnd/channel.db
      LNDG_ALLOWED_HOSTS: ${LNDG_ALLOWED_HOSTS}
      LNDG_CSRF_TRUSTED_ORIGINS: ${LNDG_CSRF_TRUSTED_ORIGINS}
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%d:%d"
    entrypoint: ["/entrypoint.sh"]
    volumes:
      - %s:/etc/lnd:ro
      - %s:/etc/lnd/channel.db:ro
      - %s:/app/data:rw
      - %s:/var/log/lndg-controller.log:rw
      - %s:/entrypoint.sh:ro

networks:
  default:
    name: lndg_default
`, LNDgPostgresImage, LNDgStopTimeout, paths.PgDir,
		LNDgImage, LNDgContainerUID, LNDgContainerGID, LNDgStopTimeout,
		LNDgPort, LNDgPort, paths.LndDir, channelDBPath,
		paths.DataDir, paths.LogPath, paths.EntrypointPath)
}

func validateLNDgToken(value string, decodedBytes int) error {
	if value == "" || strings.ContainsAny(value, "\r\n\x00= ") {
		return errors.New("invalid token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != decodedBytes {
		return errors.New("invalid token")
	}
	return nil
}

func canonicalLNDgHosts(hosts []string) []string {
	base := []string{"localhost", "127.0.0.1", "host.docker.internal"}
	dynamic := make([]string, 0, len(hosts))
	seen := map[string]bool{"localhost": true, "127.0.0.1": true, "host.docker.internal": true}
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		dynamic = append(dynamic, host)
	}
	sort.Strings(dynamic)
	return append(base, dynamic...)
}

func lndgOrigins(hosts []string) []string {
	origins := make([]string, 0, len(hosts)*4)
	for _, host := range hosts {
		for _, scheme := range []string{"http", "https"} {
			base := &url.URL{Scheme: scheme, Host: host}
			origins = append(origins, base.String())
			base.Host = net.JoinHostPort(host, strconv.Itoa(LNDgPort))
			origins = append(origins, base.String())
		}
	}
	return origins
}

// LNDgDockerfile builds the LightningOS-owned image from the exact upstream
// release source. Upstream publishes no container image or signed release
// artifact, so the broker verifies the closed source archive digest before
// this Dockerfile ever receives the build context.
func LNDgDockerfile() string {
	return fmt.Sprintf(`FROM %s
ENV PYTHONUNBUFFERED=1
RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libpq-dev postgresql-client \
    && rm -rf /var/lib/apt/lists/*
COPY %s/ /app/
WORKDIR /app
RUN python -m pip install --no-cache-dir -r requirements.txt \
    && python -m pip install --no-cache-dir supervisor==%s whitenoise==%s psycopg2-binary==%s \
    && groupadd --gid %d lndg \
    && useradd --uid %d --gid %d --home-dir /app --no-create-home --shell /usr/sbin/nologin lndg \
    && chown -R %d:%d /app
`, LNDgBaseImage, LNDgSourceDir, LNDgSupervisor, LNDgWhitenoise, LNDgPsycopgBinary,
		LNDgContainerGID, LNDgContainerUID, LNDgContainerGID, LNDgContainerUID, LNDgContainerGID)
}

func LNDgDockerfileSHA256() string {
	digest := sha256.Sum256([]byte(LNDgDockerfile()))
	return hex.EncodeToString(digest[:])
}
