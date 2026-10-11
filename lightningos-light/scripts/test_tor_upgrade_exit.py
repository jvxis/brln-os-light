"""Exercise the real updater's shell control flow, with external tools stubbed.

No root, network, APT, systemd or LND operations are performed. This does not
replace privileged integration or a real old-updater transition test.
"""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "internal/server/assets/check-tor-update.sh"
BASH = os.environ.get("TEST_BASH") or shutil.which("bash")


@unittest.skipUnless(BASH, "bash is required")
class TorUpgradeExitTests(unittest.TestCase):
    def run_case(self, case):
        with tempfile.TemporaryDirectory(prefix="los-tor-exit-") as directory:
            root = Path(directory)
            events = root / "events"
            helper = root / "onion-helper"
            helper.write_text('#!/usr/bin/env bash\nprintf "recover\\n" >> "$EVENTS"\n'
                              '[[ "$CASE" != recovery_failure ]]\n', encoding="utf-8", newline="\n")
            helper.chmod(0o700)
            source = SCRIPT.read_text(encoding="utf-8")
            self.assertTrue(source.rstrip().endswith('main "$@"'))
            source = source.rsplit('main "$@"', 1)[0]
            source = source.replace("/usr/local/libexec/lightningos-lnd-onion", helper.as_posix())
            # Run the production main/configure/recover/traps. Only external
            # dependencies and unrelated discovery are replaced with fixtures.
            fixture = r'''
require_root() { :; }
require_command() { :; }
get_os_codename() { echo noble; }
official_repo_configured() { return 0; }
curl() {
  [[ "$CASE" != repo_failure ]] || return 9
  while [[ $# -gt 0 ]]; do
    if [[ "$1" == -o ]]; then printf fixture > "$2"; return; fi
    shift
  done
}
gpg() {
  case "$*" in
    *--fingerprint*) printf 'fpr:::::::::A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89:\n';;
    *--export*) printf fixture;;
  esac
  return 0
}
gpgv() { return 0; }
reconcile_tor_sources() { printf 'repo\n' >> "$EVENTS"; }
apt-get() {
  if [[ "$1" == update ]]; then
    [[ "$CASE" != metadata_failure ]] || return 41
    return 0
  fi
  printf 'apt-restart\n' >> "$EVENTS"
  case "$CASE" in
    apt_failure|recovery_failure) return 42;;
  esac
  return 0
}
apt-cache() { echo 'Candidate: 0.4.9.14'; }
dpkg() {
  if [[ "$1" == --print-architecture ]]; then echo amd64; return; fi
  [[ "$2" == 0.4.9.11 && "$3" == lt && "$4" == 0.4.9.14 ]]
}
installed_package_version() {
  if [[ -f "$EVENTS" ]] && grep -q apt-restart "$EVENTS"; then echo 0.4.9.14
  else echo 0.4.9.11; fi
}
candidate_package_version() { echo 0.4.9.14; }
runtime_tor_version() { echo 0.4.9.14; }
detect_tor_unit() { echo tor@default.service; }
systemctl() { printf 'systemctl %s\n' "$*" >> "$EVENTS"; }
wait_for_service() { return 0; }
wait_for_bootstrap() { return 0; }
if [[ "$CASE" == verify ]]; then main --verify-only
else main --yes --configure-repo --restart; fi
'''
            env = dict(os.environ, CASE=case, EVENTS=events.as_posix(), TMPDIR=root.as_posix())
            harness = root / "harness.sh"
            harness.write_text(source + fixture, encoding="utf-8", newline="\n")
            result = subprocess.run([BASH, harness.as_posix()], env=env,
                                    capture_output=True, text=True, timeout=20)
            recorded = events.read_text().splitlines() if events.exists() else []
            # Temporary repository key material must be cleaned on every exit.
            self.assertEqual(sorted(p.name for p in root.iterdir()),
                             sorted(["onion-helper", "harness.sh"] + (["events"] if events.exists() else [])))
            return result, recorded

    def test_partial_apt_failure_preserves_error_and_recovers_once(self):
        result, events = self.run_case("apt_failure")
        self.assertEqual(result.returncode, 42, result.stderr)
        self.assertEqual(events, ["repo", "apt-restart", "recover"])

    def test_recovery_failure_does_not_mask_package_failure(self):
        result, events = self.run_case("recovery_failure")
        self.assertEqual(result.returncode, 42, result.stderr)
        self.assertEqual(events.count("recover"), 1)
        self.assertIn("recovery needs attention", result.stdout)

    def test_success_recovers_exactly_once(self):
        result, events = self.run_case("success")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(events, ["repo", "apt-restart", "systemctl restart tor@default.service", "recover"])

    def test_verify_only_and_pre_package_failures_do_not_recover(self):
        for case, code in [("verify", 0), ("repo_failure", 1), ("metadata_failure", 41)]:
            with self.subTest(case=case):
                result, events = self.run_case(case)
                self.assertEqual(result.returncode, code, result.stderr)
                self.assertNotIn("recover", events)
                self.assertNotIn("apt-restart", events)


if __name__ == "__main__":
    unittest.main()
