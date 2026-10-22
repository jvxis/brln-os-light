#!/usr/bin/env bash
set -Eeuo pipefail
project=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$project/scripts/prepare-node-upgrade.sh"
lightningos_node_policy
fixture=$(mktemp -d)
trap '[[ "$fixture" == /tmp/tmp.* && -d "$fixture" && ! -L "$fixture" ]] && rm -rf -- "$fixture"' EXIT
python3 - "$fixture" "node-v${LOS_NODE_VERSION}-linux-${LOS_NODE_ARCH}" <<'PY'
import io, os, sys, tarfile
destination, root = sys.argv[1:]
cases = {
    'valid': (root + '/bin/node', None, 0o755),
    'valid-npm': (root + '/bin/npm', '../lib/node_modules/npm/bin/npm-cli.js', 0o777),
    'traversal': (root + '/../escape', None, 0o644),
    'absolute': ('/escape', None, 0o644),
    'writable': (root + '/bin/node', None, 0o777),
    'setuid': (root + '/bin/node', None, 0o4755),
    'link': (root + '/bin/npm', '/usr/bin/npm', 0o777),
    'parent-link': (root + '/bin', '../outside', 0o777),
    'hardlink': (root + '/bin/node', None, 0o755),
    'duplicate': (root + '/bin/node', None, 0o755),
    'through-link': (root + '/bin/npm/child', None, 0o644),
}
for case, (name, link, mode) in cases.items():
    with tarfile.open(os.path.join(destination, case + '.tgz'), 'w:gz') as tar:
        if case == 'through-link':
            parent = tarfile.TarInfo(root + '/bin/npm')
            parent.type, parent.linkname, parent.mode = tarfile.SYMTYPE, '../lib/node_modules/npm/bin/npm-cli.js', 0o777
            tar.addfile(parent)
        entry = tarfile.TarInfo(name)
        entry.mode = mode
        if link is not None:
            entry.type, entry.linkname = tarfile.SYMTYPE, link
        if case == 'hardlink':
            entry.type, entry.linkname = tarfile.LNKTYPE, '/etc/passwd'
        tar.addfile(entry)
        if case == 'duplicate': tar.addfile(entry)
PY
lightningos_node_validate_archive "$fixture/valid.tgz"
lightningos_node_validate_archive "$fixture/valid-npm.tgz"
for case in traversal absolute writable setuid link parent-link hardlink duplicate through-link; do
  if lightningos_node_validate_archive "$fixture/$case.tgz"; then
    echo "FAIL: accepted $case" >&2; exit 1
  fi
done
[[ ! -e "$fixture/escape" ]] || exit 1
if lightningos_node_safe_directory relative; then echo 'FAIL: relative path accepted'; exit 1; fi
if lightningos_node_safe_directory /tmp; then echo 'FAIL: writable ancestor accepted'; exit 1; fi
echo 'PASS: archive traversal, links, permissions, duplicates and unsafe parents rejected'
[[ "$(lightningos_node_dropin_content)" == *"PATH=$LOS_NODE_ROOT/bin:"* ]]
echo 'PASS: compatibility PATH targets only the release-pinned toolchain'

if [[ "$EUID" != 0 ]]; then
  echo 'SKIP: root-owned install/retry fixtures require a disposable Linux host'
  exit 0
fi
# Own a fresh, bounded fixture, never the real toolchain store or global Node.
root_fixture=$(mktemp -d /opt/.los-node-fixture.XXXXXX)
trap '[[ "$fixture" == /tmp/tmp.* && -d "$fixture" && ! -L "$fixture" ]] && rm -rf -- "$fixture"; [[ "$root_fixture" == /opt/.los-node-fixture.* && -d "$root_fixture" && ! -L "$root_fixture" ]] && rm -rf -- "$root_fixture"' EXIT
mkdir -m 0755 "$root_fixture/store" "$root_fixture/payload"
payload="$root_fixture/payload/node-v${LOS_NODE_VERSION}-linux-${LOS_NODE_ARCH}"
mkdir -p "$payload/bin" "$payload/lib/node_modules/npm/bin"
printf '#!/bin/sh\nif [ "$1" = --version ]; then echo v24.22.1; else echo 11.19.1; fi\n' > "$payload/bin/node"
chmod 0755 "$payload/bin/node"
printf '// test npm cli\n' > "$payload/lib/node_modules/npm/bin/npm-cli.js"
ln -s ../lib/node_modules/npm/bin/npm-cli.js "$payload/bin/npm"
tar -czf "$root_fixture/download.tgz" -C "$root_fixture/payload" "$(basename "$payload")"
LOS_NODE_STORE="$root_fixture/store"
LOS_NODE_ROOT="$LOS_NODE_STORE/$(basename "$payload")"
curl() { cp "$root_fixture/download.tgz" "${!#}"; }
reject_install() {
  if lightningos_node_install; then echo "FAIL: $1 accepted" >&2; exit 1; fi
  [[ ! -e "$LOS_NODE_ROOT" && ! -L "$LOS_NODE_ROOT" ]] || { echo 'FAIL: failed install published'; exit 1; }
  [[ -z "$(find "$LOS_NODE_STORE" -maxdepth 1 -name '.node-stage.*' -print -quit)" ]] || { echo 'FAIL: interrupted stage left behind'; exit 1; }
}
LOS_NODE_SHA256=$(printf '0%.0s' {1..64})
reject_install 'wrong checksum'
LOS_NODE_SHA256=$(sha256sum "$root_fixture/download.tgz"); LOS_NODE_SHA256="${LOS_NODE_SHA256%% *}"
curl() { printf 'partial download' > "${!#}"; return 1; }
reject_install 'interrupted download'
curl() { cp "$root_fixture/download.tgz" "${!#}"; }
tar() { return 1; }
reject_install 'extraction failure (including ENOSPC)'
unset -f tar
lightningos_node_install
lightningos_node_check_tree "$LOS_NODE_ROOT"
curl() { echo 'FAIL: repeated install downloaded again' >&2; return 1; }
lightningos_node_install
chmod 0777 "$LOS_NODE_ROOT/bin/node"
if lightningos_node_install; then echo 'FAIL: unsafe installed toolchain reused' >&2; exit 1; fi
[[ "$(stat -c '%a' "$LOS_NODE_ROOT/bin/node")" == 777 ]] || { echo 'FAIL: silently repaired existing permissions'; exit 1; }
echo 'PASS: checksum/interruption/extraction failure, cleanup, retry, reuse and unsafe-existing rejection'
