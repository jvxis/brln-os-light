#!/usr/bin/env bash
set -euo pipefail

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'The Breez bridge package requires a native Linux amd64 build host' >&2
  exit 1
fi
if [[ $# -ne 1 || -e $1 ]]; then
  echo 'Usage: package-linux.sh NEW_OUTPUT_DIRECTORY' >&2
  exit 1
fi
output=$(realpath -m -- "$1")
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
umask 022
export CGO_ENABLED=1
go mod download
go mod verify
go test ./...
go vet ./...
mkdir -- "$output"
go build -trimpath -buildvcs=false -mod=readonly -o "$output/breez-bridge-probe" .
module=$(go list -m -f '{{.Dir}}' github.com/breez/breez-sdk-spark-go)
install -m 0644 -- "$module/breez_sdk_spark/lib/linux-amd64/libbreez_sdk_spark_bindings.so" "$output/"
patchelf --set-rpath '$ORIGIN' "$output/breez-bridge-probe"
install -m 0644 -- systemd/lightningos-treasury-bridge.socket systemd/lightningos-treasury-bridge@.service "$output/"
env -u LD_LIBRARY_PATH -u LD_PRELOAD "$output/breez-bridge-probe" --check-bindings
wallet_dir=$(realpath ../solana-treasury-wallet)
wallet_build=$(mktemp -d "$output/.wallet-build.XXXXXXXX")
trap 'rm -rf -- "$wallet_build"' EXIT
install -m 0644 -- "$wallet_dir"/package.json "$wallet_dir"/package-lock.json \
  "$wallet_dir"/*.mjs "$wallet_dir"/signer.html "$wallet_dir"/signer.css "$wallet_build/"
(
  cd -- "$wallet_build"
  npm ci --ignore-scripts
  npm test
  ./node_modules/.bin/esbuild signer.mjs --bundle --format=iife --platform=browser --minify --outfile="$output/signer.js"
  ./node_modules/.bin/esbuild solana-worker.mjs --bundle --format=cjs --platform=node --target=node24 --minify --outfile="$output/solana-worker.cjs"
  node --check "$output/solana-worker.cjs"
  install -m 0644 -- signer.html signer.css "$output/"
)
(
  cd -- "$output"
  sha256sum breez-bridge-probe libbreez_sdk_spark_bindings.so solana-worker.cjs signer.html signer.js signer.css \
    lightningos-treasury-bridge.socket lightningos-treasury-bridge@.service > SHA256SUMS
)
