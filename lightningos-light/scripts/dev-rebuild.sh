#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "==> Building backend"
GO_BIN="$(command -v go || echo /usr/local/go/bin/go)"
(cd "$REPO_ROOT" && sudo "$GO_BIN" build -buildvcs=false -o dist/lightningos-manager ./cmd/lightningos-manager)
sudo install -m 0755 "$REPO_ROOT/dist/lightningos-manager" /opt/lightningos/manager/lightningos-manager
echo "[OK] Backend built"

echo "==> Building UI"
(cd "$REPO_ROOT/ui" && npm install && npm run build)
sudo rm -rf /opt/lightningos/ui/*
sudo cp -a "$REPO_ROOT/ui/dist/." /opt/lightningos/ui/
echo "[OK] UI built"

echo "==> Restarting lightningos-manager"
sudo systemctl restart lightningos-manager
echo "[OK] Service restarted"

echo ""
echo "Done. Open the App Store to verify."
