#!/usr/bin/env python3
"""Prepare a reviewed source tag in the correct catalog; never publish it."""
import argparse
import json
from pathlib import Path
import re
import subprocess

LEGACY = "jvxis/brln-os-light"
MODERN = "jvxis/brln-os-light-updates"


def release_destination(tag):
    match = re.fullmatch(r"[vV]?(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?", tag)
    if not match:
        raise ValueError("Invalid release tag")
    return MODERN if tuple(map(int, match.groups())) > (0, 5, 40) else LEGACY


def output(*args, cwd=None):
    return subprocess.check_output(args, cwd=cwd, encoding="utf-8").strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", required=True, help="Existing reviewed source tag")
    parser.add_argument("--notes-file", required=True, type=Path)
    parser.add_argument("--execute", action="store_true", help="Push the exact tag and create a draft; default is read-only")
    args = parser.parse_args()
    destination = release_destination(args.tag)
    root = Path(__file__).resolve().parents[1]
    if not args.notes_file.is_file():
        raise ValueError("Release notes file does not exist")
    if output("git", "status", "--porcelain", cwd=root):
        raise ValueError("Release preparation requires a clean worktree")
    commit = output("git", "rev-parse", "refs/tags/" + args.tag + "^{commit}", cwd=root)
    version = output("git", "show", commit + ":lightningos-light/ui/public/version.txt", cwd=root)
    if args.tag.lstrip("vV").lower() != version.lower():
        raise ValueError("Source tag and embedded version do not match")
    command = ["gh", "release", "create", args.tag, "--repo", destination,
               "--verify-tag", "--draft", "--title", "LightningOS Version " + version,
               "--notes-file", str(args.notes_file.resolve())]
    print(json.dumps({"destination": destination, "tag": args.tag, "commit": commit,
                      "action": "create draft" if args.execute else "read-only plan"}, indent=2))
    if not args.execute:
        return
    settings = json.loads(output("gh", "api", "repos/" + destination + "/immutable-releases",
                                 "-H", "X-GitHub-Api-Version: 2026-03-10"))
    if settings.get("enabled") is not True:
        raise ValueError("Enable immutable releases before preparing this catalog")
    # Push only this exact reviewed source tag, without force or branch updates.
    subprocess.run(["git", "push", "https://github.com/" + destination + ".git",
                    "refs/tags/" + args.tag + ":refs/tags/" + args.tag], cwd=root, check=True)
    subprocess.run(command, cwd=root, check=True)


if __name__ == "__main__":
    main()
