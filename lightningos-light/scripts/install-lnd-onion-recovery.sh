#!/usr/bin/env bash
set -Eeuo pipefail

# Called from trusted installers/checkouts before apt or LOS can restart Tor.
[[ $EUID -eq 0 ]] || { echo 'LND onion helper installation requires root' >&2; exit 1; }
project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v python3 >/dev/null || { echo 'Python 3 is required for LND onion recovery' >&2; exit 1; }
helper=/usr/local/libexec/lightningos-lnd-onion
dropin=/etc/systemd/system/lnd.service.d/50-lightningos-onion.conf
for path in /usr/local /usr/local/libexec /etc/systemd /etc/systemd/system /etc/systemd/system/lnd.service.d "$helper" "$dropin"; do
  [[ ! -L "$path" ]] || { echo 'Unsafe LND onion helper installation path' >&2; exit 1; }
  if [[ -e "$path" ]]; then
    [[ $(stat -c %u "$path") == 0 && -z $(find "$path" -maxdepth 0 -perm /022 -print) ]] || {
      echo 'LND onion helper installation path must be root-controlled' >&2; exit 1;
    }
  fi
done
for path in "$helper" "$dropin"; do
  [[ ! -e "$path" || -f "$path" ]] || { echo 'LND onion helper target must be a regular file' >&2; exit 1; }
done
install -d -o root -g root -m 0755 /usr/local/libexec /etc/systemd/system/lnd.service.d
temporary=$(mktemp /usr/local/libexec/.lightningos-lnd-onion.XXXXXX)
trap 'rm -f -- "$temporary"' EXIT
install -o root -g root -m 0755 "$project_dir/internal/privileged/assets/lnd-onion.py" "$temporary"
mv -f -- "$temporary" "$helper"
temporary=$(mktemp /etc/systemd/system/lnd.service.d/.lightningos-onion.XXXXXX)
printf '[Service]\nExecStartPre=-+/usr/local/libexec/lightningos-lnd-onion release\n' > "$temporary"
chmod 0644 "$temporary"
mv -f -- "$temporary" "$dropin"
systemctl daemon-reload
