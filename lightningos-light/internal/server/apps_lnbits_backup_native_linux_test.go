package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/privileged"
)

// Called only inside the explicit root/regtest Docker fixture. Never download
// a real operator's backup to validate this change: archives contain secrets.
func validateLNbitsNativeBackup(t *testing.T, ctx context.Context, broker *privileged.ComposeAppManager, paths lnbitsPaths, token string, waitApp func()) {
	t.Helper()
	request := func(auth string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:5000/admin/api/v1/backup", nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		response, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
		if err != nil {
			t.Fatal("backup HTTP transport failed")
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusOK && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("successful backup must not be cached")
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, 128*1024*1024+1))
		if err != nil || len(raw) > 128*1024*1024 {
			t.Fatal("backup response unreadable or exceeds test budget")
		}
		return response.StatusCode, raw
	}
	status, _ := request("")
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("unauthenticated backup status=%d", status)
	}
	status, raw := request(token)
	if status != http.StatusOK {
		t.Fatalf("authenticated real backup status=%d (response withheld)", status)
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal("downloaded backup is not a ZIP")
	}
	found := false
	for _, entry := range archive.File {
		if entry.Name == "database.sqlite3" {
			found = true
		}
		if entry.Name == "lnbits-backup.zip" {
			t.Fatal("backup recursively includes its own output")
		}
	}
	if !found {
		t.Fatal("backup omitted the fixture database")
	}
	t.Log("authenticated real backup ZIP contains database; unauthenticated access rejected")
	run := func(args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatal("isolated backup Docker check failed (output withheld)")
		}
		return out
	}
	checkWorkspace := func(expectArchive bool) {
		t.Helper()
		code := `import os,stat,errno,shutil,sys,urllib.request,re
from pathlib import Path
assert os.getcwd()=='/run/lnbits'
p=Path.cwd();s=p.stat();assert s.st_uid==65532 and s.st_gid==65532 and stat.S_IMODE(s.st_mode)==0o700
assert (p/'lnbits').is_symlink() and (p/'lnbits').resolve()==Path('/app/lnbits')
assert (p/'lnbits-backup.zip').exists()==(sys.argv[1]=='true')
assert not Path('/app/data/lnbits-backup.zip').exists()
v=os.statvfs(p);assert v.f_blocks*v.f_frsize>64*1024*1024
try:
 with open('/app/los-write-test','w') as f:f.write('not allowed')
except OSError as e:assert e.errno==errno.EROFS
else:raise AssertionError('image root became writable')
html=urllib.request.urlopen('http://127.0.0.1:5000/',timeout=10).read().decode()
assets=re.findall(r'''(?:src|href)=["'](/static/[^"'?>]+)''',html)
assert assets,'no static assets found in actual page'
for asset in assets[:3]:
 with urllib.request.urlopen('http://127.0.0.1:5000'+asset,timeout=10) as r:assert r.status==200
try:
 with urllib.request.urlopen('http://127.0.0.1:5000/lnbits-backup.zip',timeout=10) as r:assert r.status!=200
except urllib.error.HTTPError as e:assert e.code in (401,403,404)
print('private workspace, immutable static assets and backup isolation passed; pg_dump_present='+str(bool(shutil.which('pg_dump'))))
`
		flag := "false"
		if expectArchive {
			flag = "true"
		}
		output := run("exec", "lnbits", "/app/.venv/bin/python", "-I", "-c", code, flag)
		t.Log(strings.TrimSpace(string(output)))
	}
	checkWorkspace(true)

	// ZIP data larger than the old /tmp mount must work, remain outside the
	// archived tree, and never be printed or exported from this regtest host.
	largePath := filepath.Join(paths.DataDir, "los-backup-random-fixture.bin")
	f, err := os.OpenFile(largePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		t.Fatal("cannot create isolated large-backup fixture")
	}
	t.Cleanup(func() { _ = os.Remove(largePath) })
	hash := sha256.New()
	_, writeErr := io.CopyN(io.MultiWriter(f, hash), rand.Reader, 72*1024*1024)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil || os.Chown(largePath, appmanifest.LNbitsContainerUID, appmanifest.LNbitsContainerGID) != nil {
		t.Fatal("cannot prepare isolated large-backup fixture")
	}
	status, raw = request(token)
	if status != http.StatusOK || len(raw) <= 64*1024*1024 {
		t.Fatalf("large backup failed: status=%d bytes=%d", status, len(raw))
	}
	archive, err = zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal("large backup is not a ZIP")
	}
	largeFound := false
	for _, entry := range archive.File {
		if entry.Name == filepath.Base(largePath) {
			reader, err := entry.Open()
			if err != nil {
				t.Fatal("cannot open fixture ZIP member")
			}
			digest := sha256.New()
			n, copyErr := io.Copy(digest, reader)
			_ = reader.Close()
			if copyErr != nil || n != 72*1024*1024 || !bytes.Equal(digest.Sum(nil), hash.Sum(nil)) {
				t.Fatal("large backup content/CRC/hash mismatch")
			}
			largeFound = true
		}
		if entry.Name == "lnbits-backup.zip" {
			t.Fatal("large backup includes its previous output")
		}
	}
	if !largeFound {
		t.Fatal("large backup omitted the incompressible fixture")
	}
	if err := os.Remove(largePath); err != nil {
		t.Fatal("cannot remove own isolated random fixture")
	}
	t.Log("backup larger than 64 MiB passed with exact content hash and no recursive ZIP")

	// Recreate the previous legitimate declaration with its original startup
	// gate and /app cwd. This tests real version-independent broker Stop and
	// the Manager's existing declaration reconciliation, without relaxing the
	// broker's exact catalog check for Start.
	legacyCompose := func(raw []byte) []byte {
		s := string(raw)
		start, end := strings.Index(s, "        def prepare_workdir():\n"), strings.Index(s, "        def main():\n")
		if start < 0 || end <= start {
			t.Fatal("candidate workspace fixture not found")
		}
		s = s[:start] + s[end:]
		s = strings.Replace(s, "            prepare_workdir()\n", "", 1)
		s = strings.Replace(s, "    working_dir: /run/lnbits\n", "", 1)
		s = strings.Replace(s, "      - /run/lnbits:rw,noexec,nosuid,nodev,size=25%,mode=0700,uid=65532,gid=65532\n", "", 1)
		return []byte(s)
	}
	lifecycle := func(action privileged.AppLifecycleAction) {
		t.Helper()
		if err := broker.Lifecycle(ctx, appmanifest.LNbitsID, action, false); err != nil {
			t.Fatalf("backup lifecycle %s failed", action)
		}
	}
	lifecycle(privileged.AppLifecycleStop)
	snapshot := filepath.Join(broker.PrivilegedAppsRoot, appmanifest.LNbitsID, appmanifest.LNbitsComposeFile)
	for _, path := range []string{paths.ComposePath, snapshot} {
		raw, err := os.ReadFile(path)
		if err != nil || os.WriteFile(path, legacyCompose(raw), 0600) != nil {
			t.Fatal("cannot stage isolated previous declaration")
		}
	}
	run("compose", "--project-name", appmanifest.LNbitsProject, "-f", snapshot, "up", "-d", "--no-build", "--pull", "never")
	waitApp()
	status, _ = request(token)
	if status != http.StatusInternalServerError {
		t.Fatalf("previous declaration did not reproduce backup failure: status=%d", status)
	}
	lifecycle(privileged.AppLifecycleStop)
	if err := reconcileLnbitsCatalogDeclaration(paths); err != nil {
		t.Fatal("previous declaration reconciliation failed")
	}
	lifecycle(privileged.AppLifecycleStart)
	waitApp()
	checkWorkspace(false)
	status, _ = request(token)
	if status != http.StatusOK {
		t.Fatalf("backup after previous-declaration upgrade status=%d", status)
	}
	t.Log("previous declaration Stop/reconcile/Start adopted fix, kept login/data and removed ephemeral old archive")
}
