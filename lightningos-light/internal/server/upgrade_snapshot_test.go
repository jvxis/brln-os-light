package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpgradeSnapshotsCurrentStateOnEveryAttempt(t *testing.T) {
	raw, err := os.ReadFile("assets/upgrade-app.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	start := strings.Index(content, "capture_optional_file() {")
	end := strings.Index(content, "write_manager_build_stamp() {")
	if start < 0 || end <= start {
		t.Fatal("snapshot functions not found")
	}
	for _, scenario := range []string{"legacy", "committed", "rolled_back", "pending", "symlink-state", "partial-capture"} {
		t.Run(scenario, func(t *testing.T) {
			nativeDir := t.TempDir()
			dir := filepath.ToSlash(nativeDir)
			if runtime.GOOS == "windows" {
				dir = "/" + strings.ToLower(dir[:1]) + dir[2:]
			}
			functions := content[start:end]
			for _, prefix := range []string{"/var/lib/", "/etc/", "/opt/", "/usr/local/", "/data/"} {
				functions = strings.ReplaceAll(functions, prefix, dir+prefix)
			}
			scriptPath := filepath.Join(nativeDir, "snapshot.sh")
			if err := os.WriteFile(scriptPath, []byte(snapshotFixture+"\n"+functions+"\n"+snapshotAssertions), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(recoveryBash(t), filepath.ToSlash(scriptPath), dir, scenario)
			output, err := cmd.CombinedOutput()
			if strings.Contains(string(output), "SYMLINK_UNAVAILABLE") {
				t.Skip("fixture host does not support symlinks")
			}
			if err != nil {
				t.Fatalf("snapshot fixture: %v\n%s", err, output)
			}
		})
	}
}

const snapshotFixture = `set -euo pipefail
fixture="$1"
scenario="$2"
state="$fixture/var/lib/lightningos/rollback/0.5.3-privilege-cutover"
project_dir="$fixture/project"
mkdir -p "$state" "$fixture/etc/lightningos" "$fixture/etc/systemd/system/lightningos-manager.service.d" \
  "$fixture/opt/lightningos/manager" "$fixture/opt/lightningos/ui" "$fixture/usr/local/libexec" \
  "$fixture/etc/tmpfiles.d" "$fixture/etc/sudoers.d" "$fixture/usr/local/sbin" "$project_dir/internal/server/assets"
printf current-config > "$fixture/etc/lightningos/config.yaml"
printf current-manager > "$fixture/opt/lightningos/manager/lightningos-manager"
printf current-unit > "$fixture/etc/systemd/system/lightningos-manager.service"
printf current-ui > "$fixture/opt/lightningos/ui/version.txt"
printf recovery-script > "$project_dir/internal/server/assets/rollback-privilege-cutover.sh"
printf historic-manager > "$state/lightningos-manager"
: > "$state/lightningos-manager.existed"
for name in prepared schema-v2 schema-v3 schema-v4 schema-v5 schema-v6 manager-ui.tar; do : > "$state/$name"; done
case "$scenario" in
  legacy) ;;
  symlink-state)
    printf pending > "$fixture/outside"
    ln -s "$fixture/outside" "$state/transaction-state"
    [[ -L "$state/transaction-state" ]] || { echo SYMLINK_UNAVAILABLE; exit 0; }
    ;;
  partial-capture) rm "$state/prepared" ;;
  *) printf '%s\n' "$scenario" > "$state/transaction-state" ;;
esac
PRIVILEGED_BROKER="$fixture/usr/local/libexec/lightningos-privileged"
PRIVILEGED_TMPFILES_CONFIG="$fixture/etc/tmpfiles.d/lightningos-privileged.conf"
printf current-broker > "$PRIVILEGED_BROKER"
INSTALL_BIN=fixture_install
STAT_BIN=fixture_stat
CP_BIN=cp
MV_BIN=mv
FIND_BIN=find
TAR_BIN=tar
DATE_BIN=date
SYSTEMCTL_BIN=fixture_systemctl
fixture_install() {
  local args=() directory=0
  while [[ $# -gt 0 ]]; do
    case "$1" in -o|-g|-m) shift 2 ;; -d) directory=1; shift ;; *) args+=("$1"); shift ;; esac
  done
  if [[ "$directory" == 1 ]]; then mkdir -p "${args[@]}"; else cp "${args[@]}"; fi
}
fixture_stat() { printf '0:0:700\n'; }
fixture_systemctl() { if [[ "$1" == show ]]; then printf lightningos; fi; }
validate_root_regular_file() { [[ -f "$1" && ! -L "$1" ]]; }
validate_legacy_manager_sudoers() { return 0; }
validate_legacy_auth_enable_sudoers() { return 0; }
id() { printf lightningos; }
print_ok() { :; }
die() { echo "$*" >&2; exit 1; }
`

const snapshotAssertions = `
if [[ "$scenario" == pending || "$scenario" == symlink-state ]]; then
  if (prepare_privilege_cutover); then echo 'BUG: accepted interrupted or symlinked transaction'; exit 1; fi
  [[ "$(< "$state/lightningos-manager")" == historic-manager ]]
  [[ "$(< "$fixture/opt/lightningos/manager/lightningos-manager")" == current-manager ]]
  exit 0
fi
prepare_privilege_cutover
[[ "$(< "$state/lightningos-manager")" == current-manager ]] || { echo 'BUG: reused a historical Manager snapshot'; exit 1; }
[[ "$(< "$state/lightningos-privileged")" == current-broker ]]
[[ -f "$state/lightningos-privileged.existed" ]]
[[ "$(< "$state/transaction-state")" == pending ]]
archives=("$state".previous.*)
[[ ${#archives[@]} == 1 && "$(< "${archives[0]}/lightningos-manager")" == historic-manager ]]
printf committed > "$state/transaction-state"
printf next-manager > "$fixture/opt/lightningos/manager/lightningos-manager"
prepare_privilege_cutover
[[ "$(< "$state/lightningos-manager")" == next-manager ]]
archives=("$state".previous.*)
[[ ${#archives[@]} == 2 ]]
`
