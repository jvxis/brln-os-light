package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/privileged"
)

func recoveryBash(t *testing.T) string {
	t.Helper()
	if bash := os.Getenv("LIGHTNINGOS_TEST_BASH"); bash != "" {
		return bash
	}
	if runtime.GOOS == "windows" {
		t.Skip("set LIGHTNINGOS_TEST_BASH for the isolated shell fixtures")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	return bash
}

func TestUpgradeStartFailureExplainsMissingTransport(t *testing.T) {
	for _, tc := range []struct {
		err     error
		status  int
		message string
	}{
		{fmt.Errorf("wrapped: %w", privileged.ErrBrokerUnavailable), http.StatusServiceUnavailable, "docs/UPGRADE_RECOVERY.md"},
		{errors.New("internal-detail-that-must-not-leak"), http.StatusInternalServerError, "lightningos-manager service journal"},
	} {
		response := httptest.NewRecorder()
		writeAppUpgradeStartError(response, tc.err)
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.message) || strings.Contains(response.Body.String(), "internal-detail") {
			t.Fatalf("unexpected upgrade response: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestUpgradePreflightFailureDoesNotPrepareOrPublish(t *testing.T) {
	content := strings.ReplaceAll(embeddedAppUpgradeScript, "\r\n", "\n")
	start := strings.Index(content, `print_step "Checking LND manager credential prerequisites before cutover"`)
	end := strings.Index(content, `print_step "Ensuring fixed native application identities"`)
	if start < 0 || end <= start {
		t.Fatal("missing preflight/publication boundary")
	}
	for _, failed := range []bool{false, true} {
		dir := t.TempDir()
		script := `set -euo pipefail
project_dir=fixture
print_step() { :; }
print_ok() { :; }
die() { echo "$*"; exit 1; }
fixture/dist/lightningos-privileged() { [[ "$1" == --check-lnd-manager-credential ]]; [[ "$fail" == false ]]; }
prepare_privilege_cutover() { echo prepare >> trace; }
publish_ui_tree() { echo publish >> trace; }
fail=` + fmt.Sprint(failed) + "\n" + content[start:end]
		path := filepath.Join(dir, "preflight.sh")
		if err := os.WriteFile(path, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(recoveryBash(t), filepath.ToSlash(path))
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if (err != nil) != failed {
			t.Fatalf("preflight failed=%t: %v %s", failed, err, output)
		}
		trace, err := os.ReadFile(filepath.Join(dir, "trace"))
		if failed {
			if !os.IsNotExist(err) {
				t.Fatalf("failed preflight changed installed state: %s", trace)
			}
		} else if err != nil || string(trace) != "prepare\npublish\n" {
			t.Fatalf("valid preflight did not continue: %s %v", trace, err)
		}
	}
}

// Run the shipped rollback with every absolute data path redirected into a
// temporary directory. Service/user commands are fixtures; cp/rm/tar and the
// rollback's own decisions run unchanged. This is not a systemd integration test.
func TestUpgradeRollbackPreservesRetryTransport(t *testing.T) {
	raw, err := os.ReadFile("assets/rollback-privilege-cutover.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                          string
		previous, healthy, credential bool
	}{
		{"legacy-healthy-broker", false, true, false},
		{"previous-broker", true, true, true},
		{"failed-new-broker", false, false, false},
		{"stopped-new-broker", false, false, false},
		{"unsafe-new-unit", false, false, false},
		{"repeated-rollback", false, true, false},
		{"restored-probe-failure", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nativeDir := t.TempDir()
			dir := filepath.ToSlash(nativeDir)
			if runtime.GOOS == "windows" {
				dir = "/" + strings.ToLower(dir[:1]) + dir[2:]
			}
			script := strings.ReplaceAll(string(raw), "\r\n", "\n")
			script = strings.ReplaceAll(script, `if [[ "${EUID}" -ne 0 ]]; then`, `if false; then`)
			for _, prefix := range []string{"/var/lib/", "/etc/", "/opt/", "/usr/local/", "/data/", "/run/"} {
				script = strings.ReplaceAll(script, prefix, filepath.ToSlash(dir)+prefix)
			}
			if tc.name == "repeated-rollback" {
				script += "\n" + script
			}
			scriptPath := filepath.Join(nativeDir, "rollback-fixture.sh")
			if err := os.WriteFile(scriptPath, []byte(recoveryRollbackFixture+"\n"+script+"\n"+recoveryRollbackAssertions), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(recoveryBash(t), filepath.ToSlash(scriptPath), dir, tc.name)
			output, err := cmd.CombinedOutput()
			if tc.name == "restored-probe-failure" {
				trace, readErr := os.ReadFile(filepath.Join(nativeDir, "services"))
				if err == nil || readErr != nil || !strings.Contains(string(trace), "restart lightningos-manager") || !strings.Contains(string(output), "cannot reach the recovery broker") {
					t.Fatalf("transport failure prevented Manager recovery: %v %s %s", err, output, trace)
				}
			} else if err != nil {
				t.Fatalf("rollback/retry fixture failed: %v\n%s", err, output)
			}
		})
	}
}

const recoveryRollbackFixture = `set -euo pipefail
fixture="$1"
scenario="$2"
state="$fixture/var/lib/lightningos/rollback/0.5.3-privilege-cutover"
mkdir -p "$state" "$fixture/etc/lightningos" "$fixture/etc/systemd/system/lightningos-manager.service.d" \
  "$fixture/opt/lightningos/manager" "$fixture/opt/lightningos/ui" "$fixture/usr/local/libexec" \
  "$fixture/etc/tmpfiles.d" "$fixture/etc/sudoers.d" "$fixture/usr/local/sbin"
printf old-config > "$state/config.yaml"
printf old-manager > "$state/lightningos-manager"
printf old-unit > "$state/lightningos-manager.service"
: > "$state/lightningos-manager.existed"
: > "$state/lightningos-manager.service.existed"
: > "$state/prepared"
: > "$state/schema-v6"
printf old-ui > "$fixture/opt/lightningos/ui/version.txt"
tar -C "$fixture/opt/lightningos" -cpf "$state/manager-ui.tar" ui
printf new-ui > "$fixture/opt/lightningos/ui/version.txt"
printf '#!/usr/bin/env bash\n# new-manager\n' > "$fixture/opt/lightningos/manager/lightningos-manager"
chmod +x "$fixture/opt/lightningos/manager/lightningos-manager"
printf new-config > "$fixture/etc/lightningos/config.yaml"
for path in "$fixture/etc/tmpfiles.d/lightningos-privileged.conf" \
  "$fixture/etc/systemd/system/lightningos-privileged.socket" \
  "$fixture/etc/systemd/system/lightningos-privileged@.service"; do printf new-unit > "$path"; done
cat > "$fixture/usr/local/libexec/lightningos-privileged" <<'BROKER'
#!/usr/bin/env bash
if [[ "$RECOVERY_SCENARIO" == failed-new-broker ]]; then exit 1; fi
printf '%s\n' '{"request_id":"rollback_recovery_self_test","ok":true,"result":{"ready":true,"upgrade_recovery":true}}'
BROKER
chmod +x "$fixture/usr/local/libexec/lightningos-privileged"
export RECOVERY_SCENARIO="$scenario"
if [[ "$scenario" == previous-broker ]]; then
  for name in lightningos-privileged lightningos-privileged.conf lightningos-privileged.socket lightningos-privileged@.service; do
    printf 'old-%s' "$name" > "$state/$name"
    : > "$state/$name.existed"
  done
  : > "$state/socket-active"
  : > "$state/socket-enabled"
  mkdir -p "$fixture/var/lib/lightningos-credentials/lnd"
  printf old-credential > "$state/lnd-manager-macaroon"
  printf old-state > "$state/lnd-manager-state"
  : > "$state/lnd-manager-macaroon.existed"
  : > "$state/lnd-manager-state.existed"
fi
systemctl() {
  printf '%s\n' "$*" >> "$fixture/services"
  if [[ "$scenario" == stopped-new-broker && "$*" == 'is-active --quiet lightningos-privileged.socket' ]]; then return 1; fi
  return 0
}
runuser() {
  printf '%s\n' "$*" >> "$fixture/runuser"
  if [[ "$scenario" == restored-probe-failure && "$*" == *broker-self-test ]]; then return 1; fi
  return 0
}
stat() {
  case "$2" in
    '%u:%g:%a') if [[ "$scenario" == unsafe-new-unit && "$3" == *lightningos-privileged.socket ]]; then printf '0:0:777\n'; return; fi; if [[ -d "$3" ]]; then printf '0:0:700\n'; else printf '0:0:755\n'; fi ;;
    *) command stat "$@" ;;
  esac
}
curl() { return 0; }
chown() { return 0; }
getent() { return 1; }
`

const recoveryRollbackAssertions = `
[[ "$(< "$fixture/opt/lightningos/manager/lightningos-manager")" == old-manager ]]
[[ "$(< "$fixture/opt/lightningos/ui/version.txt")" == old-ui ]]
[[ "$(< "$fixture/etc/lightningos/config.yaml")" == old-config ]]
case "$scenario" in
  legacy-healthy-broker|repeated-rollback)
    [[ -x "$fixture/usr/local/libexec/lightningos-privileged" ]] || { echo 'BUG: rollback removed the working retry broker'; exit 1; }
    [[ -f "$fixture/etc/systemd/system/lightningos-privileged.socket" ]]
    grep -q 'enable --now lightningos-privileged.socket' "$fixture/services"
    [[ -f "$state/broker-recovery-pending" ]]
    ;;
  previous-broker)
    [[ "$(< "$fixture/usr/local/libexec/lightningos-privileged")" == old-lightningos-privileged ]]
    if grep -q lnd-manager-credential-rollback "$fixture/runuser"; then
      echo 'BUG: rollback revoked a credential present before this upgrade'; exit 1
    fi
    ;;
  failed-new-broker|stopped-new-broker|unsafe-new-unit)
    [[ ! -e "$fixture/usr/local/libexec/lightningos-privileged" ]]
    ;;
esac
`
