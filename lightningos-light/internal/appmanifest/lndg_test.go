package appmanifest

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLNDgImageCatalogIsClosed(t *testing.T) {
	image, err := LNDgImageForVariant(LNDgImageApp)
	if err != nil || image != LNDgImage {
		t.Fatalf("image/error=%q/%v", image, err)
	}
	if _, err := LNDgImageForVariant("latest"); err == nil {
		t.Fatal("expected an unknown LNDg image variant to be rejected")
	}
	if !strings.Contains(LNDgBaseImage, "@sha256:") || strings.HasSuffix(LNDgImage, ":latest") {
		t.Fatalf("unpinned LNDg image catalog: base=%q image=%q", LNDgBaseImage, LNDgImage)
	}
	if image, err := LNDgImageForVariant(LNDgImagePostgres); err != nil || image != LNDgPostgresImage {
		t.Fatalf("postgres image/error=%q/%v", image, err)
	}
	if !strings.Contains(LNDgPostgresImage, "16.14-trixie@sha256:") {
		t.Fatalf("LNDg PostgreSQL image is not release and digest pinned: %q", LNDgPostgresImage)
	}
}

func TestLNDgDockerfileUsesOnlyVerifiedSourceContext(t *testing.T) {
	dockerfile := LNDgDockerfile()
	for _, required := range []string{
		"FROM " + LNDgBaseImage,
		"COPY " + LNDgSourceDir + "/ /app/",
		"supervisor==" + LNDgSupervisor,
		"whitenoise==" + LNDgWhitenoise,
		"psycopg2-binary==" + LNDgPsycopgBinary,
		"useradd --uid 1000",
		"chown -R 1000:1000 /app",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("Dockerfile missing %q", required)
		}
	}
	for _, forbidden := range []string{"git clone", "git fetch", "master", ":latest"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Fatalf("Dockerfile contains mutable source selector %q", forbidden)
		}
	}
}

func TestLNDgRuntimeEnvAndComposeAreCanonical(t *testing.T) {
	runtime := LNDgRuntime{
		AdminPassword: base64.RawURLEncoding.EncodeToString([]byte("01234567890123456789")),
		DBPassword:    base64.RawURLEncoding.EncodeToString([]byte("012345678901234567890123")),
		AllowedHosts:  []string{"10.42.0.92", "localhost", "host.docker.internal", "127.0.0.1"},
	}
	raw, err := LNDgRuntimeEnv(runtime)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseLNDgRuntimeEnv([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(parsed.AllowedHosts, ","); got != "localhost,127.0.0.1,host.docker.internal,10.42.0.92" {
		t.Fatalf("unexpected canonical hosts: %q", got)
	}
	for _, required := range []string{
		"LNDG_GIT_REF=" + LNDgRelease,
		"LNDG_GIT_SHA=" + LNDgSourceCommit,
		"https://10.42.0.92:" + "8889",
	} {
		if !strings.Contains(raw, required) {
			t.Fatalf("canonical environment missing %q", required)
		}
	}
	for _, tampered := range []string{
		strings.Replace(raw, "LNDG_NETWORK=mainnet", "LNDG_NETWORK=testnet", 1),
		strings.Replace(raw, "LNDG_RPC_SERVER=host.docker.internal:10009", "LNDG_RPC_SERVER=evil:10009", 1),
		strings.Replace(raw, "LNDG_ADMIN_PASSWORD=", "UNKNOWN=x\nLNDG_ADMIN_PASSWORD=", 1),
	} {
		if _, err := ParseLNDgRuntimeEnv([]byte(tampered)); err == nil {
			t.Fatal("expected tampered LNDg environment to be rejected")
		}
	}

	compose := LNDgCompose(LNDgComposePaths{
		DataDir:        "/var/lib/lightningos/apps-data/lndg/data",
		PgDir:          "/var/lib/lightningos/apps-data/lndg/pgdata",
		LogPath:        "/var/lib/lightningos/apps-data/lndg/data/lndg-controller.log",
		LndDir:         "/var/lib/lightningos-privileged/apps/lndg/lnd",
		ChannelDBPath:  "/var/lib/lightningos-privileged/apps/lndg/lnd/channel.db",
		EntrypointPath: "/var/lib/lightningos-privileged/apps/lndg/entrypoint.sh",
	})
	for _, required := range []string{
		"image: " + LNDgImage,
		"image: " + LNDgPostgresImage,
		"user: \"1000:1000\"",
		"cap_drop:\n      - ALL",
		"no-new-privileges:true",
		"name: lndg_default",
		"/var/lib/lightningos-privileged/apps/lndg/lnd/channel.db:/etc/lnd/channel.db:ro",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("closed LNDg compose missing %q", required)
		}
	}
	for _, forbidden := range []string{"build:", "admin.macaroon", "/data/lnd:/", "docker.sock", ":latest"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("closed LNDg compose contains %q", forbidden)
		}
	}
}

func TestLNDgEntrypointAlwaysSelectsPostgres(t *testing.T) {
	for _, required := range []string{
		"replacement = [",
		"django.db.backends.postgresql_psycopg2",
		"raw = raw[:start] + replacement + raw[end+1:]",
		"LNDG_DB_PASSWORD is required",
	} {
		if !strings.Contains(LNDgEntrypoint, required) {
			t.Fatalf("LNDg entrypoint missing %q", required)
		}
	}
	csrfEnd := strings.Index(LNDgEntrypoint, "      csrf_trusted.append(f\"{scheme}://{host}:8889\")")
	replacement := strings.Index(LNDgEntrypoint, "django.db.backends.postgresql_psycopg2")
	if csrfEnd < 0 || replacement <= csrfEnd {
		t.Fatal("PostgreSQL replacement is unexpectedly nested in the CSRF fallback")
	}
	if LNDgDockerfileSHA256() == strings.Repeat("0", 64) || len(LNDgDockerfileSHA256()) != 64 {
		t.Fatalf("invalid LNDg Dockerfile digest: %q", LNDgDockerfileSHA256())
	}
}

func TestLNDgEntrypointMigratesOnlyLegacySQLiteIntoEmptyPostgres(t *testing.T) {
	for _, required := range []string{
		`if [ -s "$SQLITE_FILE" ]`,
		`select to_regclass('public.django_migrations');`,
		`Refusing automatic SQLite import into an initialized PostgreSQL schema`,
		`DJANGO_SETTINGS_MODULE=lndg.sqlite_migration_settings python manage.py dumpdata`,
		`'ENGINE': 'django.db.backends.sqlite3'`,
		`python manage.py loaddata "$MIGRATION_FIXTURE"`,
		`: > "$MIGRATION_MARKER"`,
	} {
		if !strings.Contains(LNDgEntrypoint, required) {
			t.Fatalf("LNDg entrypoint missing guarded SQLite migration %q", required)
		}
	}
	if strings.Index(LNDgEntrypoint, "python manage.py dumpdata") > strings.LastIndex(LNDgEntrypoint, "raw = raw[:start] + replacement") {
		t.Fatal("legacy SQLite must be exported before selecting PostgreSQL settings")
	}
	if strings.Index(LNDgEntrypoint, "python manage.py loaddata") < strings.Index(LNDgEntrypoint, "python manage.py migrate") {
		t.Fatal("legacy fixture must be loaded only after PostgreSQL migrations")
	}
}
