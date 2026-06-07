# Installer Spec (v0.2)

## Goal
Install and configure the LightningOS stack:
- LND (native)
- Postgres
- lightningos-manager
- UI build
- systemd units
- default configs and secrets

## Supported OS
- Ubuntu Server 22.04 or 24.04

## Installer inputs (environment overrides)
- LND_VERSION (default 0.21.0-beta)
- GO_VERSION (default 1.22.7)
- NODE_VERSION (default current, fallback 20)
- GOTTY_VERSION (default 1.0.1)
- POSTGRES_VERSION (default latest)
- ALLOW_STOP_UNATTENDED_UPGRADES (default 1)

## High level steps
1) Validate OS and require sudo.
2) Create users and groups:
   - lnd (system user)
   - lightningos (system user)
   - operator user (TERMINAL_OPERATOR_USER)
3) Install base packages:
   - postgresql, smartmontools, tor, jq, curl, git, build tools
4) Configure Tor and optional i2pd.
5) Install Go, Node.js, and GoTTY.
6) Prepare directories:
   - /etc/lightningos, /opt/lightningos, /var/lib/lightningos, /var/log/lightningos
7) Configure secrets and templates:
   - /etc/lightningos/config.yaml
   - /etc/lightningos/secrets.env
   - /data/lnd/lnd.conf
8) Configure Postgres:
   - role and DB for LND
   - role and DB for notifications and reports (losapp)
   - admin role for provisioning (losadmin)
9) Install LND binaries (lnd, lncli).
10) Build and install lightningos-manager.
11) Build and install UI.
12) Generate TLS certs for the UI.
13) Install and enable systemd units:
   - lnd.service
   - lightningos-manager.service
   - lightningos-terminal.service (optional)
   - lightningos-reports.service and timer

## App Store and Docker
- Docker is installed on demand by the manager when the first app is installed.
- The installer sets sudoers rules so the lightningos user can run docker commands without a password.

## Output
- UI available on https://<host>:8443
- Services enabled and started
- Wizard ready for first run
