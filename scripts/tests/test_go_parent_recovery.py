"""Privileged Linux integration tests: run ONLY on a disposable VM.

Each scenario mounts a private tmpfs over /opt; no real /opt file is modified.
No install, service restart, network access or recursive deletion is performed.
"""
import importlib.util
import os
from pathlib import Path
import pwd
import stat
import subprocess
import sys
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).resolve().parents[1] / "recover-go-parent-owner.py"


def load_script():
    spec = importlib.util.spec_from_file_location("recovery", SCRIPT)
    recovery = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(recovery)
    return recovery


def scenario(name, parent_namespace):
    assert sys.platform == "linux" and os.geteuid() == 0
    assert os.readlink("/proc/self/ns/mnt") != parent_namespace
    recovery = load_script()
    subprocess.run(["mount", "-t", "tmpfs", "-o", "mode=0755", "tmpfs", "/opt"], check=True)
    target = Path("/opt/lightningos")
    admin = pwd.getpwnam("admin")
    target.mkdir(mode=0o755)
    (target / "manager").mkdir()
    binary = target / "manager/lightningos-manager"
    binary.write_bytes(b"fixture only - must never execute\n")
    binary.chmod(0o755)
    (target / "ui").mkdir()
    (target / "ui/version.txt").write_text("0.5.40-Beta\n")
    (target / "sentinel").write_bytes(b"unchanged\n")
    os.chown(target / "sentinel", admin.pw_uid, admin.pw_gid)
    (target / "sentinel").chmod(0o640)
    os.chown(target, admin.pw_uid, admin.pw_gid)
    mode = stat.S_IMODE(target.stat().st_mode)
    assert mode == 0o755
    expected_error = None
    if name == "legacy":
        before = target.stat().st_ctime_ns
        assert recovery.recover() == 2
        assert target.stat().st_uid == admin.pw_uid
        assert target.stat().st_ctime_ns == before
        assert recovery.recover(apply=True) == 0
        assert recovery.recover(apply=True) == 0
        assert recovery.recover() == 0
        assert (target.stat().st_uid, target.stat().st_gid) == (0, 0)
    elif name == "healthy":
        os.chown(target, 0, 0)
        before = target.stat().st_ctime_ns
        assert recovery.recover() == 0
        assert recovery.recover(apply=True) == 0
        assert target.stat().st_ctime_ns == before
    elif name == "chown_failure":
        before = target.stat().st_ctime_ns
        with patch.object(recovery.os, "fchown", side_effect=PermissionError("fixture denied")):
            try:
                recovery.recover(apply=True)
            except PermissionError:
                pass
            else:
                raise AssertionError("failed chown reported success")
        assert target.stat().st_ctime_ns == before
        assert recovery.recover(apply=True) == 0
    else:
        if name == "foreign_owner":
            os.chown(target, 65534, 65534)
            expected_error = "owner/group"
        elif name == "wrong_group":
            os.chown(target, admin.pw_uid, 0)
            expected_error = "owner/group"
        elif name == "writable":
            target.chmod(0o777)
            expected_error = "0755"
        elif name == "unsafe_ancestor":
            Path("/opt").chmod(0o777)
            expected_error = "unsafe ancestor"
        elif name == "unsupported_version":
            (target / "ui/version.txt").write_text("0.5.39-Beta\n")
            expected_error = "limited to"
        elif name == "missing_binary":
            binary.unlink()
            expected_error = "No such file"
        elif name == "symlink":
            target.rename("/opt/original")
            target.symlink_to("/opt/original", target_is_directory=True)
            expected_error = "Not a directory"
        elif name == "ui_symlink":
            (target / "ui").rename(target / "original-ui")
            (target / "ui").symlink_to(target / "original-ui", target_is_directory=True)
            expected_error = "Not a directory"
        elif name == "version_symlink":
            (target / "ui/version.txt").unlink()
            (target / "ui/version.txt").symlink_to(target / "sentinel")
            expected_error = "Too many levels"
        elif name == "acl":
            import struct
            # Linux POSIX access ACL: owner rwx, named uid 65534 r-x,
            # owning group r-x, mask r-x, other r-x (still mode 0755).
            acl = struct.pack("<I", 2) + b"".join(struct.pack("<HHI", *entry) for entry in (
                (1, 7, 0xffffffff), (2, 5, 65534), (4, 5, 0xffffffff),
                (16, 5, 0xffffffff), (32, 5, 0xffffffff)))
            os.setxattr(target, "system.posix_acl_access", acl)
            expected_error = "extended ACL"
        elif name == "mountpoint":
            subprocess.run(["mount", "--bind", str(target), str(target)], check=True)
            expected_error = "mount point"
        elif name == "missing":
            target.rename("/opt/original")
            expected_error = "No such file"
        else:
            raise AssertionError(name)
        reference = Path("/opt/original") if name in ("symlink", "missing") else target
        before = reference.stat()
        for apply in (False, True):
            try:
                recovery.recover(apply=apply)
            except (recovery.Refused, OSError) as exc:
                assert expected_error in str(exc), str(exc)
            else:
                raise AssertionError("unsafe recovery accepted")
        after = reference.stat()
        assert (before.st_uid, before.st_gid, before.st_mode, before.st_ctime_ns) == (
            after.st_uid, after.st_gid, after.st_mode, after.st_ctime_ns)
    reference = Path("/opt/original") if name in ("symlink", "missing") else target
    sentinel = reference / "sentinel"
    assert sentinel.read_bytes() == b"unchanged\n"
    assert (sentinel.stat().st_uid, sentinel.stat().st_gid, stat.S_IMODE(sentinel.stat().st_mode)) == (
        admin.pw_uid, admin.pw_gid, 0o640)
    assert not (reference / "toolchains").exists()


@unittest.skipUnless(sys.platform == "linux" and os.geteuid() == 0,
                     "requires disposable Linux VM, root and private mount namespaces")
class RecoveryTests(unittest.TestCase):
    def test_real_filesystem_scenarios(self):
        for name in ("legacy", "healthy", "chown_failure", "foreign_owner", "wrong_group", "writable",
                     "unsafe_ancestor", "unsupported_version", "missing_binary", "symlink",
                     "ui_symlink", "version_symlink", "acl", "mountpoint", "missing"):
            with self.subTest(name=name):
                result = subprocess.run(["unshare", "--mount", "--propagation", "private",
                    sys.executable, "-I", str(Path(__file__).resolve()), "--scenario", name,
                    os.readlink("/proc/self/ns/mnt")], capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_busy_or_unknown_updater_refuses_before_open(self):
        recovery = load_script()
        for result in (subprocess.CompletedProcess([], 0, "active\n", ""),
                       subprocess.CompletedProcess([], 1, "", "failure"),
                       subprocess.CompletedProcess([], 0, "", "")):
            with patch.object(recovery.subprocess, "run", return_value=result), \
                    patch.object(recovery.os, "open") as opened:
                with self.assertRaises(recovery.Refused):
                    recovery.recover(apply=True)
                opened.assert_not_called()


if __name__ == "__main__":
    if len(sys.argv) == 4 and sys.argv[1] == "--scenario":
        scenario(sys.argv[2], sys.argv[3])
    else:
        unittest.main()
