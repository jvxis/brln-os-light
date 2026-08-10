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
- LND_VERSION (default 0.21.1-beta)
- GO_VERSION (default 1.24.12)
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
10) Build and install lightningos-manager and the root-owned privileged broker
    foundation. Create its protected audit/lock directories, install
    `/etc/tmpfiles.d/lightningos-privileged.conf` so the runtime lock directory
    is recreated after every boot, and require the non-mutating protocol
    self-test to pass.
11) Build and install UI.
12) Generate TLS certs for the UI.
13) Install and enable systemd units:
   - lnd.service
   - lightningos-manager.service
   - lightningos-terminal.service (optional)
   - lightningos-reports.service and timer

## App Store and Docker
- Docker is installed on demand by the manager when the first app is installed.
- Current `0.5.2` installers add `lightningos` to the `docker` group and install
  passwordless wildcard sudo rules for `apt-get`, `apt`, `dpkg`, `docker`,
  `docker-compose`, `systemd-run`, and `ufw`. Both mechanisms are root-equivalent;
  they are a documented legacy boundary, not a security sandbox.
- New privileged behavior must not extend that boundary. The `0.5.3` migration
  replaces it with the typed broker described in
  `docs/32_PRIVILEGE_HARDENING_PLAN.md`, then removes Docker group membership and
  wildcard sudo only after rollback and regression checks pass.
- During Phase 1 the installers add only
  `/usr/local/libexec/lightningos-privileged ""` to the manager sudo alias. The
  empty argument constraint and the helper's own argument rejection prevent
  using it as a generic root command. Configuration defaults to `disabled`;
  installer and upgrader self-tests invoke the helper directly as root without
  changing service state.

## Output
- UI available on https://<host>:8443
- Services enabled and started
- Wizard ready for first run
