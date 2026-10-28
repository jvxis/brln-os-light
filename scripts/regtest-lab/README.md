# Dedicated LOS regtest laboratory

The production LightningOS installers remain mainnet-only. This separate lab
profile runs the same reviewed Manager binary and React build against native
Bitcoin Core 31.1 and two native LND 0.21.4-beta processes on a disposable Ubuntu
24.04 amd64 VM. Regtest coins have no value. It does not create a production
release or change the production configuration contract.

## Developer installer (0.5.50)

On a fresh **Ubuntu 24.04 amd64** VM, clone the release branch, name the VM's
Linux host `los-regtest`, and explicitly acknowledge the disposable host:

```sh
sudo apt-get update
sudo apt-get install -y git python3
git clone --branch agent/0.5.50-release https://github.com/jvxis/brln-os-light.git
cd brln-os-light
sudo hostnamectl set-hostname los-regtest
sudo bash lightningos-light/install_regtest.sh --disposable-host
```

Recommend 4 GiB RAM and at least 20 GiB free disk. The installer compiles the exact
clean, committed checkout with the release-pinned Go/Node toolchains; dependency
downloads require Internet. The build runs as an unprivileged dedicated account.
It provisions the native lab below, creates private login credentials, checks
authenticated HTTPS/native balances, and verifies Lightning/on-chain sends using
single-use financial intents. The retry check uses the identical intent/result.
Each installer run deliberately creates new small regtest test transfers.

This does not call the production installers or switch to a published release.
It refuses existing standard node data/service definitions (even inactive), LOS
configuration/state, unowned lab paths and symlinks in managed data. The explicit
flag never bypasses these checks. No wallet, seed or credential is imported.
Commit developer edits before rerunning the same command to rebuild the panels;
wallet identities, balances, configuration and authentication are retained.

A failed build does not stop existing panels. Manager deployment keeps a private
backup of the old Manager/UI/source receipt, stops only the lab UI units, then
checks both new instances through verified HTTPS/login. Failure restores the old
assets and starts the previously active panels. If interrupted during deployment,
the next attempt restores that backup first. A first-install failure retains lab
data for retry; it never deletes or resets wallets. Native package/toolchain
preparation is not a general OS package rollback mechanism. The deployment
backup does not undo application database migrations; developer changes must
keep their schema compatible with any Manager version they intend to restore.

`sudo python3 /opt/los-regtest/verify-los.py --setup-only` checks login and reads
without new payments. Without that option, verification makes small test payments.
Run `sudo python3 /opt/los-regtest/control.py status` for native regtest health.
Access, certificate and private-password instructions are below. Production
upgrades/broker/App Store lifecycle remain outside this developer profile.

## Host boundary

`LOS-TEST2` is a mainnet node with real funds and must remain running and untouched.
Only **one** `los-disposable-*` VM may run at a time. Use `Start-RegtestVM.ps1`;
it refuses another running disposable, a stopped LOS-TEST2, an unexpected target
path, or insufficient host RAM. It never stops another VM automatically.

Create a full clone of the protected powered-off `brln-os-basica` template using
the VirtualBox lab skill. Name it `los-disposable-regtest`; use 4 CPUs and 4 GiB
RAM initially. Verify its new MAC, disk, UUID and SSH host key. The template must
remain unchanged. Do not import production wallet data or credentials.

## Provisioning

On the **verified new clone only**, set hostname `los-regtest`, create the root-owned
file `/etc/los-regtest/LAB_ONLY` containing `LOS_REGTEST_ONLY`, and copy these
scripts to a dedicated tools directory. This deliberate marker is not created
by the provisioning script. Then run:

```sh
sudo bash provision.sh
sudo install -m 0755 control.py /opt/los-regtest/control.py
sudo python3 /opt/los-regtest/control.py initialize
sudo python3 /opt/los-regtest/control.py smoke
sudo python3 /opt/los-regtest/control.py status
```

The script refuses an existing `/data/lnd` or active standard `lnd`, `bitcoind`
or `lightningos-manager` service. Downloaded binaries are checked against pinned
SHA256 digests before extraction/execution. The Bitcoin digest matches the
repository artifact manifest; the LND digest matches its official release asset.
Sources: [Bitcoin Core 31.1](https://bitcoincore.org/bin/bitcoin-core-31.1/),
[LND 0.21.4-beta](https://github.com/lightningnetwork/lnd/releases/tag/v0.21.4-beta).

Wallet creation holds seed words only in process memory; they are never saved,
printed or included in reports. LND keeps its normal encrypted wallet database.
Wallet unlock passwords stay in private local files. Initialization preserves
existing wallets and funds a node with two regtest BTC only if its total on-chain
balance is below one BTC. It mines blocks to confirm the outputs. The smoke
check opens a one-million-sat channel if needed and makes a real 1000-regtest-sat
Lightning payment. Each smoke run creates another test invoice/payment.

Services are `los-regtest-bitcoin`, `los-regtest-alice` and `los-regtest-bob`.
Data stays under `/var/lib/los-regtest`; executables under `/opt/los-regtest`.
All Bitcoin/LND listeners bind localhost. Bitcoin has outbound peer discovery
disabled; systemd IP filters restrict all three services to localhost.

## LOS laboratory variant

Build Manager for Linux amd64 from the exact reviewed source commit and run the
normal frontend build. Package the contents of `ui/dist` at the archive root.
Supply both digests and the full source commit explicitly:

```sh
sudo python3 configure-los.py \
  --manager /path/to/lightningos-manager --manager-sha256 <sha256> \
  --ui-archive /path/to/ui.tar.gz --ui-sha256 <sha256> \
  --source-commit <full-40-character-commit>
sudo python3 verify-los.py
```

`configure-los.py` requires the existing regtest backend to be ready. It creates
two isolated PostgreSQL databases and two Manager services, with separate mount
namespaces for `/etc/lightningos` and `/var/lib/lightningos`, independent authentication state, localhost
HTTPS, a lab CA and a persistent yellow REGTEST notice in the served HTML.
The host's `/etc/lightningos` contains no production configuration. Auth secrets
are preserved on repeated configuration. A changed Manager artifact requires
stopping the two lab UI services first; the script refuses an in-use replacement.

Before each start, `control.py guard` verifies the real Bitcoin chain, the real
LND network, the fixed local LND endpoint/credential paths, login protection,
the lab database name/host and disabled privileged broker. A failed check prevents
Manager startup. External service access is blocked by the UI unit's IP filter.

The UI services are `los-regtest-ui-alice` (127.0.0.1:8443) and
`los-regtest-ui-bob` (127.0.0.1:8543). This is a wallet/channel test profile;
production installation, upgrades, broker operations, external integrations and
App Store lifecycle are not supported test flows in this profile. Some system
health panels may report missing production services. Never bypass a failure
by connecting these configurations to a mainnet LND.

`verify-los.py` creates a private random UI password on first setup, verifies
HTTPS using the lab CA, logs in, compares each LOS balance with native LND,
reads channels, creates a LOS invoice and pays it through the other LOS instance,
then sends 10000 on-chain regtest sats and confirms them in the destination wallet.
It first verifies that sends without an intent are rejected, prepares the immutable
snapshot, reauthenticates its scope and checks that identical retry returns the
original result. Passwords
remain under `/etc/los-regtest/{alice,bob}/ui-password` (root-only); tokens and
cookies exist only in memory. Neither scripts nor reports print these secrets.

Access through SSH forwarding with host-key verification:

```sh
ssh -N -L 127.0.0.1:19043:127.0.0.1:8443 -L 127.0.0.1:19053:127.0.0.1:8543 admin@<verified-lab-ip>
```

The URLs are `https://127.0.0.1:19043` and `https://127.0.0.1:19053`.
Use the public CA `/etc/los-regtest/ca.crt` for certificate validation; do not
disable TLS verification. Retrieve login credentials privately over verified SSH.

To mine confirmations: `sudo python3 /opt/los-regtest/control.py mine 6`.
Choose an explicit funding/closing fee such as `2 sat/vB`. Automatic estimates
may be unavailable on a short regtest chain; public fee services are not enabled
by this isolated profile.
For a service restart test, restart only `los-regtest-*` units inside this VM;
no Windows networking or other VM needs a restart. Reset/deletion is a separate
authorized operation; provisioning never deletes a wallet or chain directory.

## Recorded validation, 2026-10-27

- VM `los-disposable-regtest`, UUID `06fb6142-d6ee-49b8-ae3a-ca9ba753b3d6`,
  Ubuntu 24.04 amd64, 4 CPUs, 4 GiB, 100 GiB dynamic disk; SSH 192.168.68.95.
- Native Bitcoin 31.1 and LND 0.21.4-beta; real regtest funding, active channel,
  and settled native Lightning payment. No mainnet funds were used.
- Manager/UI source `4bee25128cd702b2ab0bd29868d80b8b6e3512d4`, Go 1.26.10;
  cross-build and `npm run build` passed. Production source code was unchanged.
- Both LOS instances passed verified HTTPS, required login, native balance and
  channel checks, LOS invoice creation, reauthentication rejection and approved
  native Lightning payment. The separate native Balanced Open regression matrix
  and its reviewed candidate are recorded in PR #326.
- Repeated provisioning preserved balances and authentication material. Native
  service restarts preserved identities, automatically unlocked both wallets
  and allowed both LOS instances to resume. A real startup-guard invocation
  rejected a configuration pointing to an unexpected LND endpoint.
- That original baseline preceded the Vite security update in PR #331; it is
  historical evidence, not the validation report for the developer installer.

Keep exact artifact digests and full runtime results outside the worktree.
