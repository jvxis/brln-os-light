#!/usr/bin/env python3
"""Opt-in recovery for issue #230. Never called by installers or the updater."""

import argparse
from contextlib import ExitStack
import errno
import os
import pwd
import re
import stat
import subprocess
import sys


TARGET = "/opt/lightningos"


class Refused(Exception):
    pass


def require(condition, message):
    if not condition:
        raise Refused(message)


def directory(stack, name, parent=None):
    # Pin each directory; never resolve a symlink and never chown by pathname.
    fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                 dir_fd=parent)
    stack.callback(os.close, fd)
    return fd


def metadata(fd):
    info = os.fstat(fd)
    return info.st_uid, info.st_gid, stat.S_IMODE(info.st_mode)


def no_acl(fd, label):
    for name in ("system.posix_acl_access", "system.posix_acl_default"):
        try:
            os.getxattr(fd, name)
        except OSError as exc:
            if exc.errno in (errno.ENODATA, errno.ENOTSUP):
                continue
            raise
        raise Refused(f"{label}: extended ACL present; requires individual review")


def idle_updater():
    result = subprocess.run(
        ["/usr/bin/systemctl", "show", "lightningos-app-upgrade.service",
         "--property=ActiveState", "--value"],
        capture_output=True, text=True, timeout=15, check=False,
        env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL": "C"})
    require(result.returncode == 0 and result.stdout.strip() in ("inactive", "failed"),
            "updater is active or its state cannot be established; wait for it to finish")


def known_layout(stack, target):
    manager = directory(stack, "manager", target)
    binary = os.stat("lightningos-manager", dir_fd=manager, follow_symlinks=False)
    require(stat.S_ISREG(binary.st_mode) and binary.st_mode & 0o111,
            "expected regular executable Manager is missing; not a recognized layout")
    ui = directory(stack, "ui", target)
    fd = os.open("version.txt", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=ui)
    stack.callback(os.close, fd)
    info = os.fstat(fd)
    require(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= 64,
            "UI version is not a small regular file")
    version = os.read(fd, 65).decode("ascii").strip()
    require(re.fullmatch(r"0\.5\.(40|41)-[Bb]eta", version),
            "recovery is limited to installed 0.5.40/0.5.41-Beta; other versions require review")
    return version


def recover(apply=False):
    require(sys.platform == "linux" and os.geteuid() == 0,
            "run on the affected Linux node with sudo python3 -I (check mode also requires root)")
    idle_updater()
    with ExitStack() as stack:
        root = directory(stack, "/")
        opt = directory(stack, "opt", root)
        for fd, label in ((root, "/"), (opt, "/opt")):
            uid, gid, mode = metadata(fd)
            require(uid == 0 and mode & 0o022 == 0,
                    f"{label}: unsafe ancestor (uid={uid}, gid={gid}, mode={mode:o}); no automatic repair")
            no_acl(fd, label)
        target = directory(stack, "lightningos", opt)
        uid, gid, mode = metadata(target)
        print(f"[CHECK] {TARGET}: uid={uid} gid={gid} mode={mode:o}")
        require(mode == 0o755, "only the known 0755 directory layout is eligible")
        no_acl(target, TARGET)
        # Reject custom mount layouts, including same-filesystem bind mounts.
        with open("/proc/self/mountinfo", encoding="utf-8") as mounts:
            require(all(line.split()[4] != TARGET for line in mounts),
                    f"{TARGET}: mount point; requires individual review")
        version = known_layout(stack, target)
        print(f"[CHECK] Recognized LightningOS {version}; updater is idle")
        if (uid, gid) == (0, 0):
            print("[OK] Already root:root; nothing changed")
            return 0
        admin = pwd.getpwnam("admin")
        require(admin.pw_uid != 0 and (uid, gid) == (admin.pw_uid, admin.pw_gid),
                "owner/group does not match the known admin:primary-group case; requires review")
        if not apply:
            print("[ACTION REQUIRED] Known legacy owner detected; nothing changed")
            print("After reviewing this result, rerun the same script with --apply")
            return 2
        idle_updater()
        before = os.fstat(target)
        current = os.stat("lightningos", dir_fd=opt, follow_symlinks=False)
        require((before.st_dev, before.st_ino) == (current.st_dev, current.st_ino)
                and metadata(target) == (uid, gid, mode), "directory changed during inspection; retry diagnosis")
        no_acl(target, TARGET)
        print(f"[BEFORE] {TARGET}: uid={uid} gid={gid} mode={mode:o}", flush=True)
        os.fchown(target, 0, 0)
        require(metadata(target) == (0, 0, mode), "post-change verification failed; request support")
        current = os.stat("lightningos", dir_fd=opt, follow_symlinks=False)
        require((before.st_dev, before.st_ino) == (current.st_dev, current.st_ino),
                "path changed concurrently; pinned directory was repaired, request support")
        print(f"[OK] Only {TARGET} owner/group changed to root:root; mode remains {mode:o}")
        print("No descendants or services changed. Retry the upgrade in the UI yourself.")
        return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--check", action="store_true", help="read-only diagnosis (default)")
    modes.add_argument("--apply", action="store_true", help="repair only the recognized parent owner")
    args = parser.parse_args()
    try:
        return recover(apply=args.apply)
    except (Refused, OSError, KeyError, UnicodeError, subprocess.SubprocessError) as exc:
        # Only operation metadata; never read configs, secrets, or service logs.
        print(f"[REFUSED] {str(exc)!r}. Stop and request support; do not use recursive chown.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
