#!/usr/bin/env bash
set -Eeuo pipefail
[[ $EUID -eq 0 ]] || { echo 'Kernel TCP mitigation installation requires root' >&2; exit 1; }
project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v python3 >/dev/null || { echo 'Python 3 is required for kernel TCP mitigation' >&2; exit 1; }
exec python3 "$project_dir/internal/server/assets/kernel-tcp.py" install
