# Security Model (v0.2)

## Access
- UI and API bind to the server host and are intended exclusively for a trusted LAN or private VPN such as Tailscale.
- LightningOS is not designed or supported for direct public Internet/WAN exposure. Port 8443 and App Store ports must never be publicly forwarded.
- The installers add a LAN-restricted port 8443 rule only when UFW is already installed and active. They do not activate UFW automatically because that could lock out SSH or unrelated services on existing nodes.
- Operators must verify UFW or provide an equivalent host, router, hypervisor, cloud, or VPN firewall before using the node.

## Secrets
- /etc/lightningos/secrets.env is owned by root:lightningos with mode 660.
- Secrets include LND Postgres DSN, notifications DSN, Bitcoin RPC creds, and terminal creds.
- UI never re-displays stored secrets.

## Wallet seed
- Seed words are never persisted.
- Seed is shown only once during wallet creation.
- Wallet import sends the seed directly to LND and does not store it.

## Users and permissions
- lnd runs as user lnd.
- lightningos runs the manager and owns app data.
- lightningos uses sudoers to run a small allow list of systemctl and docker commands.

## LND access
- Manager reads TLS cert and admin macaroon via group access.
- LND gRPC is on localhost only.

## App Store
- Docker is installed on demand.
- App containers are managed via docker compose with passwordless sudo for lightningos.
- App secrets are stored in app-specific .env or data files with restrictive permissions.

## Terminal
- Optional GoTTY terminal requires a credential in secrets.env.
- Terminal can be disabled by setting TERMINAL_ENABLED=0.

## Reports and notifications
- Reports data and notification history are stored in Postgres.
- Reports live endpoint caches data briefly and never writes secrets.
