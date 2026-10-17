"""Execute the real bootstrap with local Git repositories and an HTTP fixture.

No installer/system service is run: fixture installers only record dispatch.
This is regression coverage, NOT full Linux installation/upgrade validation.
Run: python -m unittest discover -s scripts/tests -p test_install_bootstrap.py -v
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
BASH = os.environ.get("BOOTSTRAP_TEST_BASH", "bash")
SOURCE = "https://github.com/jvxis/brln-os-light"
CATALOG = "https://github.com/jvxis/brln-os-light-updates.git"
TAG = "0.5.46-Beta"


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="los-bootstrap-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.source = self.root / "source"
        self.target = self.root / "target"
        self.marker = self.root / "executed"
        self.env = os.environ.copy()
        for key in list(self.env):
            if key.startswith(("BRLN_", "LIGHTNINGOS_", "GIT_CONFIG_")) or key == "REPO_URL":
                self.env.pop(key)
        self.env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                        MSYS_NO_PATHCONV="1",
                        GIT_TERMINAL_PROMPT="0", GIT_AUTHOR_NAME="Fixture",
                        GIT_AUTHOR_EMAIL="fixture@example.invalid",
                        GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid")
        self.source.mkdir()
        self.git("init", "-b", "main", str(self.source))
        self.git("-C", str(self.source), "config", "core.autocrlf", "false")
        app = self.source / "lightningos-light"
        (app / "ui/public").mkdir(parents=True)
        (app / "ui/public/version.txt").write_text(TAG + "\n", encoding="utf8")
        (app / "scripts").mkdir()
        shutil.copyfile(ROOT / "lightningos-light/scripts/install-release-bootstrap.sh", app / "scripts/install-release-bootstrap.sh")
        shutil.copyfile(ROOT / "lo_bootstrap.sh", self.source / "lo_bootstrap.sh")
        for name in ("install.sh", "install_existing.sh", "install_existing_pi.sh"):
            (app / name).write_text('#!/usr/bin/env bash\nset -eu\nREPO_ROOT="$(cd "$(dirname "$0")" && pwd)"\nsource "$REPO_ROOT/scripts/install-release-bootstrap.sh"\nlightningos_bootstrap_latest_release "$(basename "$0")" "$@"\nprintf "%s|%s|%s|%s\\n" "$(basename "$0")" "$(cat "$REPO_ROOT/ui/public/version.txt")" "${LIGHTNINGOS_RELEASE_BOOTSTRAPPED:-}" "$*" >> "$TEST_MARKER"\n', encoding="utf8", newline="\n")
            (app / name).chmod(0o755)
        (self.source / ".github").mkdir()
        (self.source / ".github/private-plan.md").write_text("repository only\n")
        self.git("-C", str(self.source), "add", ".")
        for name in ("install.sh", "install_existing.sh", "install_existing_pi.sh"):
            self.git("-C", str(self.source), "update-index", "--chmod=+x", "lightningos-light/" + name)
        self.git("-C", str(self.source), "commit", "-m", "fixture")
        self.git("-C", str(self.source), "tag", "-a", TAG, "-m", "annotated release")
        self.git("-C", str(self.source), "tag", "0.5.40-Beta")
        self.catalog = self.root / "catalog.git"
        self.git("clone", "--bare", str(self.source), str(self.catalog))
        self.release = dict(tag_name=TAG, draft=False, prerelease=False, immutable=True)
        self.write_release()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.wrapper("curl", '''#!/usr/bin/env bash
set -eu
printf '%s\\n' "${!#}" >> "$TEST_HTTP_LOG"
case "${!#}" in
  */brln-os-light-updates/releases/latest|*/brln-os-light-updates/releases/tags/*)
    [[ "${TEST_NETWORK_FAIL:-0}" != 1 ]] || exit 22
    cat "$TEST_RELEASE" ;;
  */brln-os-light/releases\\?per_page=10) printf '[{"tag_name":"0.5.40-Beta"}]\\n' ;;
  */brln-os-light/releases/tags/*) printf '{"tag_name":"0.5.40-Beta","draft":false,"immutable":true}\\n' ;;
  *) exit 22 ;;
esac
''')
        # Git's get-url expands insteadOf; return the stored official origin in
        # this fixture while retaining real Git for all other operations.
        self.wrapper("git", '''#!/usr/bin/env bash
if [[ "${TEST_WINDOWS:-0}" == 1 ]]; then
  args=("$@")
  for ((i=0; i<${#args[@]}-1; i++)); do
    if [[ "${args[i]}" == -C ]]; then
      args[i+1]=$(cygpath -m "${args[i+1]}")
    fi
  done
  set -- "${args[@]}"
fi
if [[ "${*: -3}" == 'remote get-url origin' ]]; then
  args=("${@:1:$#-3}")
  exec "$TEST_REAL_GIT" "${args[@]}" config --get remote.origin.url
fi
exec "$TEST_REAL_GIT" "$@"
''')
        # Resolve before adding our shim to PATH.
        git_exe = Path(shutil.which("git")).as_posix()
        if os.name == "nt":
            self.wrapper("python3", '#!/usr/bin/env bash\nexec "' + Path(sys.executable).as_posix() + '" "$@"\n')
        self.env.update(PATH=str(self.bin) + os.pathsep + self.env["PATH"],
                        BRLN_DIR=self.target.as_posix(), TEST_MARKER=self.marker.as_posix(),
                        TEST_RELEASE=(self.root / "release.json").as_posix(),
                        TEST_HTTP_LOG=(self.root / "http.log").as_posix(),
                        TEST_BIN=self.bin.as_posix(), TEST_REAL_GIT=git_exe,
                        TEST_WINDOWS="1" if os.name == "nt" else "0",
                        GIT_CONFIG_COUNT="2",
                        GIT_CONFIG_KEY_0=f"url.{self.source.as_posix()}.insteadOf",
                        GIT_CONFIG_VALUE_0=SOURCE,
                        GIT_CONFIG_KEY_1=f"url.{self.catalog.as_posix()}.insteadOf",
                        GIT_CONFIG_VALUE_1=CATALOG)

    def wrapper(self, name, text):
        path = self.bin / name
        path.write_text(text, encoding="utf8", newline="\n")
        path.chmod(0o755)

    def git(self, *args):
        return subprocess.check_output(["git", *args], env=self.env, text=True, stderr=subprocess.STDOUT).strip()

    def write_release(self):
        (self.root / "release.json").write_text(json.dumps(self.release, indent=2), encoding="utf8")

    def run_bootstrap(self, entry=None, **env):
        script = entry or ROOT / "lo_bootstrap.sh"
        if os.environ.get("BOOTSTRAP_BASELINE_REF"):
            script = self.root / "baseline.sh"
            script.write_text(subprocess.check_output(["git", "show", os.environ["BOOTSTRAP_BASELINE_REF"] + ":lo_bootstrap.sh"], cwd=ROOT, text=True), encoding="utf8", newline="\n")
        prefix = 'TEST_BIN=$(cygpath -u "$TEST_BIN"); ' if os.name == "nt" else ""
        launcher = prefix + 'export PATH="$TEST_BIN:$PATH"; shell=$(command -v bash); if [[ "${TEST_MISSING_PYTHON:-0}" == 1 ]]; then export PATH="$TEST_BIN"; fi; exec "$shell" "$1" --fixture-argument'
        return subprocess.run([BASH, "-c", launcher, "fixture", script.as_posix()],
                              env={**self.env, **env}, text=True, stdout=subprocess.PIPE,
                              stderr=subprocess.STDOUT, timeout=45)

    def assert_rejected(self, **env):
        result = self.run_bootstrap(**env)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertFalse(self.marker.exists(), result.stdout)
        return result

    def test_latest_dispatch_all_installers_and_repeat(self):
        for installer in ("install.sh", "install_existing.sh", "install_existing_pi.sh"):
            with self.subTest(installer=installer):
                result = self.run_bootstrap(BRLN_INSTALLER=installer)
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertIn(f"Using LightningOS release/ref: {TAG}", result.stdout)
                self.assertIn(f"{installer}|{TAG}|1|--fixture-argument", self.marker.read_text())
                self.assertFalse((self.target / ".github").exists())
        self.assertNotIn("/brln-os-light/releases", (self.root / "http.log").read_text())

    def test_reject_invalid_release_before_clone(self):
        for changes in ({"immutable": False}, {"draft": True}, {"prerelease": True},
                        {"tag_name": "main"}, {"tag_name": "../../evil"}, {"immutable": "true"}):
            with self.subTest(changes=changes):
                saved = self.release.copy()
                self.release.update(changes)
                self.write_release()
                self.assert_rejected()
                self.assertFalse(self.target.exists())
                self.release = saved

    def test_api_failure_no_fallback_and_retry(self):
        self.assert_rejected(TEST_NETWORK_FAIL="1")
        self.assertFalse(self.target.exists())
        result = self.run_bootstrap()
        self.assertEqual(result.returncode, 0, result.stdout)

    def test_malformed_json(self):
        (self.root / "release.json").write_text('{"tag_name":')
        self.assert_rejected()
        self.assertFalse(self.target.exists())

    def test_source_tag_must_match_immutable_catalog(self):
        (self.source / "changed").write_text("wrong source")
        self.git("-C", str(self.source), "add", ".")
        self.git("-C", str(self.source), "commit", "-m", "wrong source")
        self.git("-C", str(self.source), "tag", "-f", TAG)
        self.assert_rejected()

    def test_source_version_must_match_release(self):
        (self.source / "lightningos-light/ui/public/version.txt").write_text("0.5.39-Beta\n")
        self.git("-C", str(self.source), "add", ".")
        self.git("-C", str(self.source), "commit", "-m", "wrong version")
        self.git("-C", str(self.source), "tag", "-f", TAG)
        self.git("-C", str(self.catalog), "fetch", "--force", str(self.source), f"refs/tags/{TAG}:refs/tags/{TAG}")
        self.assert_rejected()

    def test_dirty_checkout_preserved(self):
        self.assertEqual(self.run_bootstrap().returncode, 0)
        self.marker.unlink()
        changed = self.target / "lightningos-light/install.sh"
        changed.write_text("operator changes\n")
        self.assert_rejected()
        self.assertEqual(changed.read_text(), "operator changes\n")

    def test_explicit_development_ref_keeps_escape_hatch(self):
        result = self.run_bootstrap(BRLN_REF="main")
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertFalse((self.root / "http.log").exists())

    def test_invalid_installer_and_conflicting_refs(self):
        self.assert_rejected(BRLN_INSTALLER="../../bad")
        self.assert_rejected(BRLN_REF="main", BRLN_BRANCH="main")
        self.assertFalse(self.target.exists())

    def test_direct_installer_entry_points_reenter_once(self):
        self.git("-C", str(self.source), "remote", "add", "origin", SOURCE)
        for name in ("install.sh", "install_existing.sh", "install_existing_pi.sh"):
            with self.subTest(installer=name):
                result = self.run_bootstrap(entry=self.source / "lightningos-light" / name)
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertIn(f"{name}|{TAG}|1|--fixture-argument", self.marker.read_text())
        self.assertEqual(len(self.marker.read_text().splitlines()), 3)

    def test_unrelated_origin_and_occupied_directory_are_preserved(self):
        self.target.mkdir()
        note = self.target / "operator.txt"
        note.write_text("preserve me")
        self.assert_rejected()
        self.assertEqual(note.read_text(), "preserve me")
        self.git("init", str(self.target))
        self.git("-C", str(self.target), "remote", "add", "origin", "https://example.invalid/other")
        self.assert_rejected()
        self.assertEqual(note.read_text(), "preserve me")

    def test_custom_source_or_catalog_requires_explicit_development_mode(self):
        self.assert_rejected(REPO_URL="https://example.invalid/source")
        self.assert_rejected(BRLN_RELEASE_API_URL="https://example.invalid/releases")
        self.assertFalse(self.target.exists())

    def test_lightweight_tag(self):
        self.git("-C", str(self.source), "tag", "-f", TAG, "HEAD")
        self.git("-C", str(self.catalog), "fetch", "--force", str(self.source), f"refs/tags/{TAG}:refs/tags/{TAG}")
        result = self.run_bootstrap()
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("Source commit and version match", result.stdout)

    def test_missing_catalog_tag_stops_before_checkout(self):
        self.git("-C", str(self.catalog), "tag", "-d", TAG)
        self.assert_rejected()
        self.assertFalse(self.target.exists())

    def test_missing_python_prerequisite_fails_before_network(self):
        shim = self.bin / "python3"
        if shim.exists():
            shim.unlink()
        result = self.assert_rejected(TEST_MISSING_PYTHON="1")
        self.assertIn("Missing 'python3'", result.stdout)
        self.assertFalse((self.root / "http.log").exists())
        self.assertFalse(self.target.exists())

    def test_branch_cannot_substitute_for_missing_source_tag(self):
        self.git("-C", str(self.source), "tag", "-d", TAG)
        self.git("-C", str(self.source), "branch", TAG)
        result = self.assert_rejected()
        self.assertIn("Release tag is missing", result.stdout)


if __name__ == "__main__":
    unittest.main()
