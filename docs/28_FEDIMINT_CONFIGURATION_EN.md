# Fedimint Configuration in LightningOS Light

This guide documents the current Fedimint App Store integration in LightningOS Light.

The app installs two Docker services:

- `fedimintd`: the federation guardian.
- `gatewayd lnd`: the Lightning gateway connected to the local LightningOS LND.

It does not install a separate `bitcoind`. Fedimint reuses the Bitcoin RPC already configured in LightningOS, whether that is the App Store Bitcoin Core, an external local Bitcoin node, or a remote Bitcoin node.

## Requirements

- Bitcoin RPC configured in LightningOS.
- Local LND running and synced.
- LND files available at:
  - `/data/lnd/tls.cert`
  - `/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon`
- Docker available. The App Store installs Docker on demand when needed.

## Endpoints

For a LAN installation, replace `<SERVER_LAN_IP>` with the LightningOS server IP:

- Guardian UI: `http://<SERVER_LAN_IP>:8175`
- Gateway UI: `http://<SERVER_LAN_IP>:8176`
- Gateway Lightning V2 URL: `http://<SERVER_LAN_IP>:8176/v1`

The `http://<SERVER_LAN_IP>:8176/v1` endpoint is only suitable for tests inside the same network. For external users, register the gateway with a public HTTPS URL, for example:

```text
https://gateway.example.com/v1
```

Do not register `192.168.x.x`, `10.x.x.x`, or `172.16-31.x.x` for a federation that will be used outside the LAN.

## Ports

- `8173/tcp` and `8173/udp`: guardian P2P communication.
- `8174/udp`: guardian API/Iroh.
- `8175/tcp`: Guardian UI.
- `8176/tcp`: Gateway UI/API.
- `8177/udp`: gateway Iroh.

Because the gateway runs in LND mode (`gatewayd lnd`), the LDK Lightning port `10010` is not used by this integration.

## LND

The gateway uses the local LND over gRPC:

```text
https://host.docker.internal:10009
```

During Fedimint install/start, LightningOS may add entries to `/data/lnd/lnd.conf` to allow gRPC access from the app Docker network:

```text
tlsextradomain=host.docker.internal
rpclisten=127.0.0.1:10009
tlsextraip=<docker-gateway-ip>
rpclisten=<docker-gateway-ip>:10009
```

This change is additive. If the file changes, LightningOS removes `tls.cert`/`tls.key` so LND regenerates the certificate, then restarts the `lnd` service.

## Bitcoin

The app resolves Bitcoin RPC from the existing configuration:

- App Store Bitcoin Core: uses the Docker network `bitcoincore_default` and connects to `http://bitcoind:8332`.
- External local Bitcoin node: uses `host.docker.internal:<port>`.
- Remote Bitcoin node: uses the credentials already configured in LightningOS/LND.

Credentials are written to the app environment file:

```text
/var/lib/lightningos/apps/fedimint/.env
```

## Data Paths

- Compose: `/var/lib/lightningos/apps/fedimint/docker-compose.yaml`
- Env: `/var/lib/lightningos/apps/fedimint/.env`
- Guardian data: `/var/lib/lightningos/apps-data/fedimint/fedimintd`
- Gateway data: `/var/lib/lightningos/apps-data/fedimint/gatewayd`
- Gateway admin password: `/var/lib/lightningos/apps-data/fedimint/gateway-admin.txt`

When uninstalling from the App Store, LightningOS removes app files under `/var/lib/lightningos/apps/fedimint`, but preserves persistent data under `/var/lib/lightningos/apps-data/fedimint`.

## Uninstall and Full Reset

The App Store `Uninstall` button is conservative. It runs `docker compose down --remove-orphans` and removes operational files under:

```text
/var/lib/lightningos/apps/fedimint
```

It does not automatically remove:

- guardian data in `/var/lib/lightningos/apps-data/fedimint/fedimintd`;
- gateway data in `/var/lib/lightningos/apps-data/fedimint/gatewayd`;
- gateway admin password;
- downloaded Docker images;
- entries added to `/data/lnd/lnd.conf`;
- added UFW rules.

This avoids deleting a federation by accident. In a real federation, removing guardian data can cause operational loss or loss of access if there is no backup.

For a test installation or a partial/broken installation, use a full reset only when you are sure the data can be discarded.

First, try to stop and remove the compose project if the app files still exist:

```bash
sudo docker compose \
  --env-file /var/lib/lightningos/apps/fedimint/.env \
  --project-directory /var/lib/lightningos/apps/fedimint \
  -f /var/lib/lightningos/apps/fedimint/docker-compose.yaml \
  down --remove-orphans
```

If this fails because `.env` or `docker-compose.yaml` is missing, the installation was partial. Check for leftover containers and networks:

```bash
sudo docker ps -a --format "table {{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}" | grep -i fedimint
sudo docker network ls | grep -i fedimint
sudo ss -ltnp | grep -E ':8175|:8176'
```

Remove leftovers if any are listed:

```bash
sudo docker ps -aq --filter "name=fedimint" | xargs -r sudo docker rm -f
sudo docker ps -aq --filter "ancestor=fedimint/fedimintd:v0.11.1" | xargs -r sudo docker rm -f
sudo docker ps -aq --filter "ancestor=fedimint/gatewayd:v0.11.1" | xargs -r sudo docker rm -f
sudo docker network rm fedimint_default 2>/dev/null || true
```

Then remove app files and persistent data:

```bash
sudo systemctl stop lightningos-manager

sudo rm -rf /var/lib/lightningos/apps/fedimint
sudo rm -rf /var/lib/lightningos/apps-data/fedimint

sudo systemctl start lightningos-manager
```

Verify both directories are gone:

```bash
sudo ls -la /var/lib/lightningos/apps/fedimint
sudo ls -la /var/lib/lightningos/apps-data/fedimint
```

Both commands should return `No such file or directory`.

Then update/reinstall LightningOS Manager and install Fedimint again from the App Store:

```bash
cd ~/brln-os-light
git pull
cd lightningos-light
sudo ./install_existing.sh
sudo systemctl restart lightningos-manager
```

## Create a Test Federation with 1 Guardian

1. Install Fedimint from the App Store.
2. Open the Guardian UI at `http://<SERVER_LAN_IP>:8175`.
3. Create a solo federation for testing only.
4. Download the guardian backup.
5. Open the Gateway UI at `http://<SERVER_LAN_IP>:8176`.
6. Confirm LND appears as `External LND` and `Synced`.
7. Paste the federation invite code into `Connect a new Federation`.
8. In the Guardian UI, under Lightning V2, register the gateway:

```text
http://<SERVER_LAN_IP>:8176/v1
```

Use a public HTTPS URL instead of the LAN IP if external clients will participate.

## Create a Basic Federation with 4 Guardians

An existing solo federation should not be converted to 4 guardians. Create a new federation.

Recommended test model:

- Guardian 1: your LightningOS server, running `fedimintd` and `gatewayd lnd`.
- Guardian 2: user A server, running `fedimintd`.
- Guardian 3: user B server, running `fedimintd`.
- Guardian 4: user C server, running `fedimintd`.

Only the server operating the gateway needs to connect to LND. The other guardians do not need access to your LND.

Operational flow:

1. Each guardian installs the Fedimint app on their own server.
2. Each guardian opens their Guardian UI.
3. One guardian starts the federation setup with 4 guardians.
4. Each guardian generates their setup code.
5. The 4 guardians share setup codes through a secure out-of-band channel.
6. Each guardian enters the other guardians' codes in the Guardian UI.
7. Everyone completes setup/DKG.
8. Each guardian downloads and stores their backup.
9. The gateway operator pastes the federation invite code into the Gateway UI.
10. The federation registers the gateway in Lightning V2 with a URL reachable by clients.

With `FM_ENABLE_IROH=true`, guardian communication can work even behind NAT/firewalls. For production, prefer stable networking and a public HTTPS endpoint for the gateway.

## Checks

Container status:

```bash
sudo docker compose -f /var/lib/lightningos/apps/fedimint/docker-compose.yaml ps
```

Logs:

```bash
sudo docker compose -f /var/lib/lightningos/apps/fedimint/docker-compose.yaml logs --tail=200 fedimintd
sudo docker compose -f /var/lib/lightningos/apps/fedimint/docker-compose.yaml logs --tail=200 gatewayd
```

Check whether LND was prepared for Docker:

```bash
sudo grep -E 'host.docker.internal|rpclisten=.*10009|tlsextraip' /data/lnd/lnd.conf
```

In the Gateway UI, verify:

- `Gateway Network Information`: `Running`.
- `Lightning Node`: `External LND`.
- `Status`: `Synced`.
- `Block Height`: close to the current LND block height.

In the Guardian UI, verify:

- Bitcoin RPC connected.
- Lightning V2 with the gateway registered.
- Wallet with no consensus errors.

## Security Notes

- A 1-guardian federation is for testing only.
- For production, use several independent guardians. A 4-guardian federation tolerates 1 offline/malicious guardian within Fedimint's BFT model.
- Each guardian must keep their own backup.
- Do not share macaroons, gateway passwords, or data files between guardians.
- The gateway operator's LND must not be exposed directly to the other guardians.
