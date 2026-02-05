#!/usr/bin/env bash
set -Eeuo pipefail

LND_DIR="/data/lnd"
CHAIN_DIR="${LND_DIR}/data/chain/bitcoin/mainnet"
LND_CONF="${LND_DIR}/lnd.conf"

if [[ -d "$LND_DIR" ]]; then
  chown lnd:lnd "$LND_DIR"
  chmod 750 "$LND_DIR"
fi

if [[ -f "$LND_CONF" ]]; then
  chown lnd:lnd "$LND_CONF"
  chmod 660 "$LND_CONF"
fi

for dir in "$LND_DIR/data" "$LND_DIR/data/chain" "$LND_DIR/data/chain/bitcoin" "$CHAIN_DIR"; do
  if [[ -d "$dir" ]]; then
    chown lnd:lnd "$dir"
    chmod 750 "$dir"
  fi
done

if [[ -f "$LND_DIR/tls.cert" ]]; then
  chown lnd:lnd "$LND_DIR/tls.cert"
  chmod 640 "$LND_DIR/tls.cert"
fi

if [[ -d "$CHAIN_DIR" ]]; then
  shopt -s nullglob
  for mac in "$CHAIN_DIR"/*.macaroon; do
    chown lnd:lnd "$mac"
    chmod 640 "$mac"
  done
  shopt -u nullglob
fi
