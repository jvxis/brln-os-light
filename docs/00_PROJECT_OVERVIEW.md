# LightningOS - Project Overview

## Goal
LightningOS is a guided control center for running an LND node with a clean web UI, optional apps, and safe defaults.

## Current scope (v0.2)
- Native LND install (systemd, no Docker in the core)
- Go manager API plus React UI
- Wizard for Bitcoin remote credentials, wallet create or import, and unlock
- Dashboard status for system, disks, Postgres, Bitcoin, and LND
- Wallet UI (balances, invoice, pay, decode, send)
- Lightning Ops (channels, peers, fees, open or close)
- Reports (daily table plus live results)
- Notifications (history plus optional Telegram backup)
- App Store (optional native and Docker-based apps)
- Terminal (GoTTY) optional

## Principles
- LAN or VPN access only by default
- Minimal privileges and explicit secrets
- Idempotent automation (installer and reports job)
- Mobile friendly UI

## Access defaults
- UI and API: https://127.0.0.1:8443
- LND gRPC: 127.0.0.1:10009
- LND data: /data/lnd (symlinked from /home/lnd/.lnd)

## Out of scope
- Public WAN exposure
- Multi-user auth system
- Fleet management

## Repository layout
- cmd/lightningos-manager: manager CLI
- internal/: server, LND client, reports, notifications, app store
- ui/: React SPA
- templates/: systemd and config templates
- docs/: product and operational documentation
