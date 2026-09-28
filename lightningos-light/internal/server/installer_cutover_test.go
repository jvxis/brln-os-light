package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Execute the actual installer orchestration, not a copy of its condition.
// All privileged commands are replaced by fixtures and paths stay in TempDir.
func TestExistingInstallerFirstInstallCutover(t *testing.T) {
	bash := os.Getenv("LIGHTNINGOS_TEST_BASH")
	if bash == "" {
		if runtime.GOOS == "windows" {
			t.Skip("set LIGHTNINGOS_TEST_BASH to a native Git Bash for shell fixtures")
		}
		var err error
		bash, err = exec.LookPath("bash")
		if err != nil {
			t.Skip("bash unavailable")
		}
	}
	for _, installer := range []string{"install_existing.sh", "install_existing_pi.sh"} {
		t.Run(installer, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", installer))
			if err != nil {
				t.Fatal(err)
			}
			content := strings.ReplaceAll(string(raw), "\r\n", "\n")
			start := strings.LastIndex(content, "    ensure_go\n")
			end := strings.Index(content, "    bash \"$REPO_ROOT/internal/server/assets/upgrade-app.sh\" --stage-cutover-only\n")
			if start < 0 || end < start {
				t.Fatal("installer manager/cutover block not found")
			}
			end += len("    bash \"$REPO_ROOT/internal/server/assets/upgrade-app.sh\" --stage-cutover-only\n")
			block := strings.NewReplacer(
				"/opt/lightningos/manager/lightningos-manager", `"$fixture/manager"`,
				"/etc/systemd/system/lightningos-manager.service", `"$fixture/unit"`,
			).Replace(content[start:end])
			for _, tc := range []struct {
				name, initial, failAt, want, snapshot string
				fail                                  bool
			}{
				{"fresh", "", "", "go build unit prepare broker-units reload socket stage", "new-manager|new-unit", false},
				{"existing", "both", "", "go prepare build unit broker-units reload socket stage", "old-manager|old-unit", false},
				{"binary-only", "manager", "", "go prepare", "", true},
				{"unit-only", "unit", "", "go prepare", "", true},
				{"dangling-symlink", "symlink", "", "go prepare", "", true},
				{"fresh-build-failure", "", "build", "go build", "", true},
				{"existing-prepare-failure", "both", "prepare", "go prepare", "", true},
				{"fresh-prepare-failure", "", "prepare", "go build unit prepare", "", true},
				{"existing-build-failure", "both", "build", "go prepare build", "old-manager|old-unit", true},
				{"fresh-stage-failure", "", "stage", "go build unit prepare broker-units reload socket stage", "new-manager|new-unit", true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					for _, artifact := range []string{"manager", "unit"} {
						if tc.initial == "both" || tc.initial == artifact {
							if err := os.WriteFile(filepath.Join(dir, artifact), []byte("old-"+artifact), 0o600); err != nil {
								t.Fatal(err)
							}
						}
					}
					if tc.initial == "symlink" {
						if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "manager")); err != nil {
							t.Skipf("symlink fixture unavailable: %v", err)
						}
					}
					script := installerCutoverFixture + "\nrun_installer() {\n" + block + "\n}\nrun_installer\n"
					cmd := exec.Command(bash, "-c", script, "test", filepath.ToSlash(dir), tc.failAt)
					if output, err := cmd.CombinedOutput(); (err != nil) != tc.fail {
						t.Fatalf("unexpected result: %v, output: %s", err, output)
					}
					trace, err := os.ReadFile(filepath.Join(dir, "trace"))
					if err != nil {
						t.Fatal(err)
					}
					if got := strings.Join(strings.Fields(string(trace)), " "); got != tc.want {
						t.Fatalf("order = %q; want %q", got, tc.want)
					}
					snapshot, err := os.ReadFile(filepath.Join(dir, "snapshot"))
					if tc.snapshot == "" {
						if !os.IsNotExist(err) {
							t.Fatalf("unexpected rollback snapshot: %q (%v)", snapshot, err)
						}
					} else if err != nil || string(snapshot) != tc.snapshot {
						t.Fatalf("snapshot = %q (%v); want %q", snapshot, err, tc.snapshot)
					}
					if strings.HasPrefix(tc.want, "go prepare") && tc.failAt != "stage" && tc.fail {
						for _, artifact := range []string{"manager", "unit"} {
							if tc.initial == "both" || tc.initial == artifact {
								data, err := os.ReadFile(filepath.Join(dir, artifact))
								if err != nil || string(data) != "old-"+artifact {
									t.Fatalf("old %s overwritten on failure", artifact)
								}
							}
						}
					}
				})
			}
		})
	}
}

const installerCutoverFixture = `set -Eeuo pipefail
fixture="$1"
fail_at="$2"
REPO_ROOT=fixture-only
manager_user=lightningos
manager_group=lightningos
record() { printf '%s\n' "$1" >> "$fixture/trace"; }
ensure_go() { record go; }
build_manager() {
  record build
  [[ "$fail_at" != build ]]
  printf new-manager > "$fixture/manager"
}
ensure_manager_service() { record unit; printf new-unit > "$fixture/unit"; }
ensure_privileged_broker_units() { record broker-units; }
systemctl() {
  case "$*" in
    daemon-reload) record reload ;;
    'enable --now lightningos-privileged.socket') record socket ;;
    *) return 99 ;;
  esac
}
bash() {
  [[ "$1" == fixture-only/internal/server/assets/upgrade-app.sh ]]
  case "$2" in
    --prepare-cutover-only)
      record prepare
      [[ "$fail_at" != prepare ]]
      [[ -f "$fixture/manager" && ! -L "$fixture/manager" ]]
      [[ -f "$fixture/unit" && ! -L "$fixture/unit" ]]
      printf '%s|%s' "$(< "$fixture/manager")" "$(< "$fixture/unit")" > "$fixture/snapshot"
      ;;
    --stage-cutover-only)
      record stage
      [[ -f "$fixture/snapshot" && "$fail_at" != stage ]]
      ;;
    *) return 99 ;;
  esac
}
`
