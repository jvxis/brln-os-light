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

## Pinned installer defaults
- LND_VERSION (default 0.21.3-beta)
- Go 1.26.8, with Linux amd64/arm64 SHA-256 checksums in
  `lightningos-light/scripts/go-toolchain.conf`
- NODE_VERSION (default 24)
- GOTTY_VERSION (default 1.8.0)

The three installers and the internal application upgrade share
`scripts/prepare-go-toolchain.sh`. Go is verified and staged under
`/opt/lightningos/toolchains/go<version>.linux-<arch>`; `/usr/local/go` and
distribution-managed Go installations are preserved. Builds explicitly use the
prepared compiler with automatic toolchain switching disabled. An existing
managed compiler is reused only after validating its ownership, permissions,
archive identity, version and architecture. Downloads are required on first use.

Release 0.5.40 retains its Go 1.24/module and Go 1.24.12/toolchain declarations
so that older installed updaters can compile the bridge release. Its new updater
prepares the target release's Go before building the next release. Installing
0.5.40 through an older updater does not itself execute the new preparation
step. See [the Go upgrade transition](GO_UPGRADE_0.5.40.md) for testing and release
constraints.

LND version and download origin are closed release inputs. Fresh installs use
the authenticated official LND artifact selected by the LightningOS release;
`LND_VERSION` and `LND_URL` environment overrides are not accepted.

## Supported environment overrides
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
