package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Execute the exact reconciliation code embedded in the broker-pinned helper.
// Fixtures never invoke APT, systemctl, downloads, or access the host's /etc.
func torReconcileFixture(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	pythonName := "python3"
	if runtime.GOOS == "windows" {
		pythonName = "python"
	}
	python, err := exec.LookPath(pythonName)
	if err != nil {
		t.Skip("Python is required for Tor reconciliation fixture tests")
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		t.Skip("root-owned source-file safety checks require root for reconciliation fixtures")
	}
	_, script, ok := strings.Cut(embeddedTorUpgradeScript, "<<'TOR_SOURCES_PY'\n")
	if !ok {
		t.Fatal("embedded source reconciler not found")
	}
	script, _, ok = strings.Cut(script, "\nTOR_SOURCES_PY\n")
	if !ok {
		t.Fatal("embedded source reconciler terminator not found")
	}
	root := t.TempDir()
	apt := filepath.Join(root, "apt")
	key := filepath.Join(root, "keyrings", "tor.gpg")
	for _, dir := range []string{filepath.Join(apt, "sources.list.d"), filepath.Dir(key)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	program := filepath.Join(root, "reconcile.py")
	writeTorFixture(t, program, script)
	authenticated := filepath.Join(root, "authenticated.gpg")
	writeTorFixture(t, authenticated, "authenticated fixture key")
	return python, program, apt, key, authenticated
}

func writeTorFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readTorFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTorReconcileLegacyAndDeb822WithBackupAndIdempotence(t *testing.T) {
	for _, suite := range []string{"jammy", "noble"} {
		t.Run(suite, func(t *testing.T) {
			python, program, apt, key, authenticated := torReconcileFixture(t)
			main := filepath.Join(apt, "sources.list")
			foreign := "deb https://ubuntu.example/ubuntu " + suite + " main\n"
			legacy := "deb [signed-by=/usr/share/keyrings/tor-archive-keyring.gpg] https://deb.torproject.org/torproject.org/ " + suite + " main\n"
			writeTorFixture(t, main, foreign+legacy+"# "+legacy)
			d822 := filepath.Join(apt, "sources.list.d", "legacy.sources")
			other := "# retain me\nTypes: deb\nURIs: https://ubuntu.example/ubuntu\nSuites: " + suite + "\nComponents: main\n"
			old := "Types: deb deb-src\nURIs:\n https://deb.torproject.org/torproject.org/\nSuites: " + suite + "\nSigned-By: /old.gpg\n"
			writeTorFixture(t, d822, old+"\n"+other)
			writeTorFixture(t, key, "old fixture key")
			cmd := exec.Command(python, "-I", program, apt, key, authenticated, suite, "amd64")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("reconcile: %v\n%s", err, out)
			}
			if got := readTorFixture(t, main); !strings.HasPrefix(got, foreign) || strings.Contains(got, "\n"+legacy) || !strings.HasSuffix(got, "# "+legacy) {
				t.Fatalf("unexpected list rewrite: %s", got)
			}
			if got := readTorFixture(t, d822); !strings.Contains(got, other) || !strings.HasPrefix(got, "# Disabled by LightningOS: Types") {
				t.Fatalf("unexpected deb822 rewrite: %s", got)
			}
			managed := filepath.Join(apt, "sources.list.d", "tor.sources")
			canonical := readTorFixture(t, managed)
			if !strings.Contains(canonical, "Signed-By: "+key) || !strings.Contains(canonical, "Suites: "+suite) {
				t.Fatal("canonical source missing")
			}
			backups, _ := filepath.Glob(filepath.Join(apt, "lightningos-tor-backup-*"))
			if len(backups) != 1 {
				t.Fatalf("expected one backup, got %v", backups)
			}
			if got := readTorFixture(t, filepath.Join(backups[0], "0.original")); got != foreign+legacy+"# "+legacy {
				t.Fatal("original file not backed up exactly")
			}
			if !strings.Contains(readTorFixture(t, filepath.Join(backups[0], "manifest.json")), "backup") {
				t.Fatal("restore manifest missing")
			}
			cmd = exec.Command(python, "-I", program, apt, key, authenticated, suite, "amd64")
			if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "already reconciled") {
				t.Fatalf("second run: %v\n%s", err, out)
			}
			if readTorFixture(t, managed) != canonical || readTorFixture(t, key) != "authenticated fixture key" {
				t.Fatal("idempotence/key mismatch")
			}
		})
	}
}

func TestTorReconcileRefusesMixedStanzaWithoutAnyWrite(t *testing.T) {
	for _, fields := range []string{
		"URIs: https://deb.torproject.org/torproject.org/ https://unrelated.example/repo\nSuites: jammy\n",
		"URIs: https://deb.torproject.org/torproject.org/\nSuites: jammy noble\n",
	} {
		python, program, apt, key, authenticated := torReconcileFixture(t)
		path := filepath.Join(apt, "sources.list.d", "mixed.sources")
		original := "Types: deb\n" + fields + "Components: main\n"
		writeTorFixture(t, path, original)
		writeTorFixture(t, key, "old key")
		out, err := exec.Command(python, "-I", program, apt, key, authenticated, "jammy", "amd64").CombinedOutput()
		if err == nil || !strings.Contains(string(out), "split it manually") {
			t.Fatalf("expected safe refusal: %v %s", err, out)
		}
		if readTorFixture(t, path) != original || readTorFixture(t, key) != "old key" {
			t.Fatal("preflight failure wrote files")
		}
		if _, err := os.Stat(filepath.Join(apt, "sources.list.d", "tor.sources")); !os.IsNotExist(err) {
			t.Fatal("canonical source created despite failed preflight")
		}
	}
}

func TestTorReconcilePreservesMixedFileDisabledEntriesAndOtherSuites(t *testing.T) {
	python, program, apt, key, authenticated := torReconcileFixture(t)
	path := filepath.Join(apt, "sources.list.d", "tor.sources")
	preserved := "# another repository\nTypes: deb\nURIs: https://other.example/repo\nSuites: jammy\nSigned-By:\n -----BEGIN PGP PUBLIC KEY BLOCK-----\n .\n fixture\n -----END PGP PUBLIC KEY BLOCK-----\n\nTypes: deb\nURIs: https://deb.torproject.org/torproject.org/\nSuites: noble\nEnabled: no\n"
	writeTorFixture(t, path, preserved)
	list := filepath.Join(apt, "sources.list")
	other := "deb https://deb.torproject.org/torproject.org/ noble main\ndeb https://deb.torproject.org/torproject.org/experimental/ jammy main\n"
	writeTorFixture(t, list, other)
	if out, err := exec.Command(python, "-I", program, apt, key, authenticated, "jammy", "arm64").CombinedOutput(); err != nil {
		t.Fatalf("reconcile: %v %s", err, out)
	}
	if !strings.HasPrefix(readTorFixture(t, path), preserved) || readTorFixture(t, list) != other {
		t.Fatal("unrelated configuration changed")
	}
}

func TestTorReconcileRollsBackPartialCommit(t *testing.T) {
	for _, failAt := range []string{"2", "3"} {
		t.Run("replace-"+failAt, func(t *testing.T) {
			python, program, apt, key, authenticated := torReconcileFixture(t)
			path := filepath.Join(apt, "sources.list")
			old := "deb https://deb.torproject.org/torproject.org/ jammy main\n"
			writeTorFixture(t, path, old)
			writeTorFixture(t, key, "old key")
			harness := `import runpy, sys, os
from pathlib import Path
module = runpy.run_path(sys.argv[1])
replace = os.replace
calls = 0
def fail_once(src, dst):
    global calls
    calls += 1
    if calls == int(sys.argv[5]):
        raise OSError('injected commit failure')
    return replace(src, dst)
os.replace = fail_once
module['reconcile'](Path(sys.argv[2]), Path(sys.argv[3]), Path(sys.argv[4]), 'jammy', 'amd64')
`
			out, err := exec.Command(python, "-I", "-c", harness, program, apt, key, authenticated, failAt).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "injected commit failure") {
				t.Fatalf("expected injected failure: %v %s", err, out)
			}
			if readTorFixture(t, path) != old || readTorFixture(t, key) != "old key" {
				t.Fatal("partial commit not rolled back")
			}
			if _, err := os.Stat(filepath.Join(apt, "sources.list.d", "tor.sources")); !os.IsNotExist(err) {
				t.Fatal("new source survived rollback")
			}
		})
	}
}

func TestTorReconcileRejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	python, program, apt, key, authenticated := torReconcileFixture(t)
	target := filepath.Join(filepath.Dir(apt), "outside.list")
	original := "deb https://deb.torproject.org/torproject.org/ jammy main\n"
	writeTorFixture(t, target, original)
	if err := os.Symlink(target, filepath.Join(apt, "sources.list")); err != nil {
		t.Skipf("symlink fixtures unavailable: %v", err)
	}
	out, err := exec.Command(python, "-I", program, apt, key, authenticated, "jammy", "amd64").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Unsafe APT/keyring path") {
		t.Fatalf("expected symlink refusal: %v %s", err, out)
	}
	if readTorFixture(t, target) != original {
		t.Fatal("symlink target changed")
	}
}
