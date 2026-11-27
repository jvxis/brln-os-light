#!/usr/bin/env bash
set -euo pipefail

if [[ $(id -u) != 0 || $(uname -s) != Linux || $(uname -m) != x86_64 || $# != 1 ]]; then
  echo 'Run as root on Linux amd64 with one reviewed Treasury bundle directory.' >&2
  exit 1
fi
src=$(realpath -- "$1")
parent=/opt/lightningos
target=$parent/treasury-app
[[ -x /usr/bin/node && $(/usr/bin/node -p 'Number(process.versions.node.split(".")[0])') -ge 24 ]]
[[ -d "$src" && -f "$src/SHA256SUMS" && ! -L "$src/SHA256SUMS" ]]
[[ ! -L "$parent" && ! -L "$parent/treasury-releases" ]]
install -d -o root -g root -m 0755 -- "$parent" "$parent/treasury-releases"
[[ ! -e "$target" || -L "$target" ]]

stage=$(mktemp -d "$parent/treasury-releases/.staging.XXXXXXXX")
link="$stage.link"
cleanup() {
  rm -f -- "$link"
  if [[ -d "$stage" ]]; then
    find "$stage" -maxdepth 1 -type f -delete
    rmdir -- "$stage"
  fi
}
trap cleanup EXIT
names=(breez-bridge-probe libbreez_sdk_spark_bindings.so solana-worker.cjs signer.html signer.js signer.css lightningos-treasury-bridge.socket lightningos-treasury-bridge@.service)
for name in "${names[@]}"; do
  [[ -f "$src/$name" && ! -L "$src/$name" ]]
  install -o root -g root -m 0644 -- "$src/$name" "$stage/$name"
done
chmod 0755 -- "$stage/breez-bridge-probe"
install -o root -g root -m 0644 -- "$src/SHA256SUMS" "$stage/SHA256SUMS"
(
  cd -- "$stage"
  for name in "${names[@]}"; do
    awk -v name="$name" '$2 == name && NF == 2 { print }' SHA256SUMS
  done > expected
  [[ $(wc -l < expected) == "${#names[@]}" ]]
  sha256sum --strict --check expected
  mv -T -- expected SHA256SUMS
  env -u LD_LIBRARY_PATH -u LD_PRELOAD ./breez-bridge-probe --check-bindings
)
release_dir="$parent/treasury-releases/$(sha256sum "$stage/SHA256SUMS" | cut -d ' ' -f 1)"
if [[ -e "$release_dir" ]]; then
  [[ -d "$release_dir" && ! -L "$release_dir" ]]
  (cd -- "$release_dir" && sha256sum --strict --check "$stage/SHA256SUMS")
else
  chmod 0755 -- "$stage"
  mv -T -- "$stage" "$release_dir"
fi
units=(lightningos-treasury-bridge.socket lightningos-treasury-bridge@.service)
previous=$(readlink -- "$target" || true)
unit_backup=$(mktemp -d /etc/systemd/system/.treasury-rollback.XXXXXXXX)
for name in "${units[@]}"; do
  unit=/etc/systemd/system/$name
  [[ ! -L "$unit" ]]
  if [[ -e "$unit" ]]; then
    [[ -f "$unit" ]]
    cp -p -- "$unit" "$unit_backup/$name"
  fi
done
changing=1
finish() {
  local status=$?
  if [[ "$status" != 0 && "$changing" == 1 ]]; then
    for name in "${units[@]}"; do
      if [[ -e "$unit_backup/$name" ]]; then
        mv -Tf -- "$unit_backup/$name" "/etc/systemd/system/$name"
      else
        rm -f -- "/etc/systemd/system/$name"
      fi
    done
    if [[ -n "$previous" ]]; then
      rm -f -- "$link"
      ln -s -- "$previous" "$link"
      mv -Tf -- "$link" "$target"
    else
      rm -f -- "$target"
    fi
    systemctl daemon-reload || true
  fi
  find "$unit_backup" -maxdepth 1 -type f -delete
  rmdir -- "$unit_backup"
  cleanup
}
trap finish EXIT

ln -s -- "$release_dir" "$link"
mv -Tf -- "$link" "$target"
for name in "${units[@]}"; do
  install -o root -g root -m 0644 -- "$target/$name" "$unit_backup/$name.new"
  mv -Tf -- "$unit_backup/$name.new" "/etc/systemd/system/$name"
done
systemd-analyze verify /etc/systemd/system/lightningos-treasury-bridge.socket /etc/systemd/system/lightningos-treasury-bridge@.service
systemctl daemon-reload
systemctl enable --now lightningos-treasury-bridge.socket
changing=0
