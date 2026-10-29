# Dedicated LOS regtest laboratory

The production LightningOS installers remain mainnet-only. This separate lab
profile runs the same reviewed Manager binary and React build against native
Bitcoin Core 31.2 and three native LND 0.21.5-beta processes on a disposable Ubuntu
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
checks all three new instances through verified HTTPS/login. Failure restores the old
assets and starts the previously active panels. If interrupted during deployment,
the next attempt restores that backup first. A first-install failure retains lab
data for retry; it never deletes or resets wallets. Native package/toolchain
preparation is not a general OS package rollback mechanism. The deployment
backup does not undo application database migrations; developer changes must
keep their schema compatible with any Manager version they intend to restore.

`sudo python3 /opt/los-regtest/verify-los.py --setup-only` checks login and reads
without new payments; add `--node alice`, `--node bob` or `--node charlie` to check a single panel.
Without that option, verification makes small test payments.
Run `sudo python3 /opt/los-regtest/control.py status` for native regtest health.
Access, certificate and private-password instructions are below. Production
upgrades/broker/App Store lifecycle remain outside this developer profile.

## Supported workflows and visible limitations

Expand the yellow **O que funciona / Supported features** notice in any panel
to check this scope before using a production menu.

Select **Português** or **English** in the panel toolbar to read one language
at a time. The scrollable table uses distinct status badges; language controls
remain available while you scroll.

| Validated workflow | What you can test |
| --- | --- |
| Wallet and channels | Login, on-chain/Lightning balances, receiving addresses and channel reads. |
| Lightning payments | Create invoices and pay between Alice/Bob/Charlie, with password confirmation. |
| On-chain payments | Deposit/send fictitious coins, mine confirmations manually and check the received balance. |

**Lightning operations:** use the following distinction in this profile. The
same bilingual matrix is available in the yellow notice on every page, including
the Lightning operations screens.

| Operation | Lab status |
| --- | --- |
| Read channels and balances | Validated. |
| Initial channel created by setup through `lncli` | Validated in setup; this does not validate opening through the LOS UI. |
| Open a single channel through the UI | Validated with manual fees, password confirmation and native active-channel verification. |
| Batch-open channels through the UI | Validated: two distinct peers, one funding transaction, both channels active. |
| Balanced channel opening through the UI | Validated: Alice proposes, Bob accepts, Alice executes; active 50/50 channel. |
| Cooperative channel close through the UI | Validated after correcting native error reconciliation and fee hints ([#343](https://github.com/jvxis/brln-os-light/issues/343), [#344](https://github.com/jvxis/brln-os-light/issues/344)). Manual and Auto closes verified against native txids. |
| Force-close a channel through the UI | Validated through confirmation, CSV maturity and native balance recovery. |
| Edit one channel’s fees | Validated: outbound/base fees and timelock matched native LND policy. |
| Manual rebalance through the UI | Validated: moved 49,824 sats for 2 sats and correctly recorded the partial result. A rebalance attempt does not need to move 100% of the requested amount to validate the workflow. |
| Manual AutoFee | Validated: Dry run preserved policy; Run now applied a change to a test channel. |
| Scheduled automations and failure recovery | Not yet validated: automatic rebalance, auto-heal, HTLC Manager and failure scenarios require dedicated tests. |
| Node Retirement | Validated dry-run and live flow: cooperative close, offline-peer decision, force-close maturity/recovery and final reconciliation. Use a dedicated test node; this retires all its channels. |
| Succession | Validated: proof of life, grace period, automatic deadline trigger, retirement and on-chain transfer respecting minimum confirmations. Test database dates were advanced. The whole-wallet transfer waits for all current funds to mature; new unconfirmed deposits can postpone it. |
Telegram notification delivery is a separate integration: configure its token and destination to use it. External delivery was not tested in this localhost-only lab; that does not downgrade the successful Succession workflow. Likewise, an expected partial rebalance is a valid execution result. Historical reports retain the exact observed amounts and timing fixtures.

Additional authenticated LOS API checks, independently verified against native LND (these do not claim that every related UI control was exercised):

| Operation | Lab status |
| --- | --- |
| Sign messages | Validated: peer LND verifies the signer; changing the message does not retain that identity, and empty input is rejected. |
| Organize UTXOs | Validated: labels/tags/colors and group create, assign, detach and delete; original metadata restored. |
| Audit Log | Validated: native lock/release actions persisted and queried by action, target and limit; unauthenticated access and invalid limits rejected. |
| Lock and unlock UTXOs | Validated after [#349](https://github.com/jvxis/brln-os-light/issues/349): locked rows and metadata stay visible; UI release, expiry and unchanged spending-preview exclusion verified. |

See the [additional operations report](../../docs/REGTEST_COVERAGE_2026-10-29.md).
To repeat these opt-in checks on the configured disposable host, mine a block
if needed, then run `sudo python3 scripts/regtest-lab/verify-operations.py`
from the source checkout. Do not run it concurrently with wallet operations.
It creates no payments/channels, restores temporary metadata/groups/leases,
and now passes on all three nodes. The locked-output assertion remains
enabled and fails if that inventory regression returns. Each scenario reports its result.
This helper is not part of the installation or its automatic smoke tests.

See the [dated test report](../../docs/REGTEST_LIGHTNING_VALIDATION_2026-10-28.md)
for the environment, procedures, observed results and coverage limits. Manual
runs do not validate scheduled execution or long-term automation economics.

The [Retirement/Succession follow-up](../../docs/REGTEST_RETIREMENT_SUCCESSION_VALIDATION_2026-10-28.md)
records the later corrections and the native confirmation-policy boundary test.

**Not yet validated** means the end-to-end UI workflow has not been exercised on
this lab installation. It does not mean it is known to fail, and it must not be
confused with the explicitly unsupported production controls listed below. An
existing channel or a successful status request alone does not validate opening
or closing channels. The notice contains Portuguese and English descriptions.

Mining is manual. After a long period without new blocks, LND may report that
it is not synced even at the current height. With the miner wallet loaded, run
`sudo python3 /opt/los-regtest/control.py mine 1` and wait for synchronization
before testing payments. See the funding instructions in the main README to load the miner.

**Outside this profile:** LND configuration through the UI (including name/alias,
color and network settings), maintenance/restart controls, LND/LOS UI upgrades,
App Store and external integrations. Other features require their own validation.
The standard UI retains these menus; visibility does not imply regtest support.
Health panels may also report missing production services.

Each lab Manager is restricted to localhost and has the production privileged
broker disabled. GitHub update checks can therefore time out, and the LND config
page can fail to read the production configuration path. These are documented
lab limitations; do not change production permissions/networking to make the
menus work. The production installers and application remain unchanged.

## Host boundary

Use a dedicated disposable Linux host or VM without real funds. The lab does
not require VirtualBox, a particular VM name, a maintainer's template, or another
node to be running. Allocate resources according to your host's capacity; the
recorded amd64 validation used 4 CPUs and 4 GiB RAM. Leave enough resources for
the host and any other workloads.

When cloning a VM, verify its independent disks, new network identity and SSH
host key before provisioning. Do not import production wallet data or
credentials, and do not stop or reconfigure unrelated nodes to make room.

## Provisioning

On the **verified new clone only**, set hostname `los-regtest`, create the root-owned
file `/etc/los-regtest/LAB_ONLY` containing `LOS_REGTEST_ONLY`, and copy these
scripts to a dedicated tools directory. This deliberate marker is not created
by the provisioning script. Then run:

```sh
sudo bash provision.sh
sudo install -m 0755 nodes.py control.py /opt/los-regtest/
sudo python3 /opt/los-regtest/control.py initialize
sudo python3 /opt/los-regtest/control.py smoke
sudo python3 /opt/los-regtest/control.py status
```

The script refuses an existing `/data/lnd` or active standard `lnd`, `bitcoind`
or `lightningos-manager` service. Downloaded binaries are checked against pinned
SHA256 digests before extraction/execution. The Bitcoin digest matches the
repository artifact manifest; the LND digest matches its official release asset.
Sources: [Bitcoin Core 31.2](https://bitcoincore.org/bin/bitcoin-core-31.2/),
[LND 0.21.5-beta](https://github.com/lightningnetwork/lnd/releases/tag/v0.21.5-beta).

Wallet creation holds seed words only in process memory; they are never saved,
printed or included in reports. LND keeps its normal encrypted wallet database.
Wallet unlock passwords stay in private local files. Initialization preserves
existing wallets and funds a node with two regtest BTC only if its total on-chain
balance is below one BTC. It mines blocks to confirm the outputs. The smoke
check creates missing edges of Alice–Bob–Charlie–Alice. Each new channel has
1,000,000 sats capacity with 500,000 sats initially pushed to the peer. Existing
channels and pending funding are reused; extra user channels are retained.
Each native smoke run sends 1,000 regtest sats on each of the three directed
edges. The authenticated LOS smoke separately pays the same three pairs and
confirms an on-chain transfer. These are actual fictitious transfers, not resets.

Services are `los-regtest-bitcoin`, `los-regtest-alice`, `los-regtest-bob` and
`los-regtest-charlie`; all are enabled at boot.
Data stays under `/var/lib/los-regtest`; executables under `/opt/los-regtest`.
All Bitcoin/LND listeners bind localhost. Bitcoin has outbound peer discovery
disabled; systemd IP filters restrict these services to localhost.

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
three isolated PostgreSQL databases and three Manager services, with separate mount
namespaces for `/etc/lightningos` and `/var/lib/lightningos`, independent authentication state, localhost
HTTPS, a lab CA and a persistent yellow REGTEST notice in the served HTML.
The host's `/etc/lightningos` contains no production configuration. Auth secrets
are preserved on repeated configuration. A changed Manager artifact requires
stopping all three lab UI services first; the script refuses an in-use replacement.

Before each start, `control.py guard` verifies the real Bitcoin chain, the real
LND network, the fixed local LND endpoint/credential paths, login protection,
the lab database name/host and disabled privileged broker. A failed check prevents
Manager startup. External service access is blocked by the UI unit's IP filter.

The UI services are `los-regtest-ui-alice` (127.0.0.1:8443),
`los-regtest-ui-bob` (127.0.0.1:8543) and `los-regtest-ui-charlie`
(127.0.0.1:8643). Each is enabled at boot. This is a wallet/channel test profile;
production installation, upgrades, broker operations, external integrations and
App Store lifecycle are not supported test flows in this profile. Some system
health panels may report missing production services. Never bypass a failure
by connecting these configurations to a mainnet LND.

`verify-los.py` creates a private random UI password on first setup, verifies
HTTPS using the lab CA, logs in, compares each LOS balance with native LND,
reads channels, creates LOS invoices and pays across the three node pairs,
then sends 10000 on-chain regtest sats and confirms them in the destination wallet.
It first verifies that sends without an intent are rejected, prepares the immutable
snapshot, reauthenticates its scope and checks that identical retry returns the
original result. Passwords
remain under `/etc/los-regtest/{alice,bob,charlie}/ui-password` (root-only); tokens and
cookies exist only in memory. Neither scripts nor reports print these secrets.

Access through SSH forwarding with host-key verification:

```sh
ssh -N -L 127.0.0.1:19043:127.0.0.1:8443 -L 127.0.0.1:19053:127.0.0.1:8543 -L 127.0.0.1:19063:127.0.0.1:8643 user@<verified-lab-host>
```

| Alias | HTTPS URL through SSH | LND gRPC / REST / peer ports (localhost) |
| --- | --- | --- |
| `LOS-Regtest-Alice` | `https://alice.localhost:19043` | 10009 / 8080 / 9735 |
| `LOS-Regtest-Bob` | `https://bob.localhost:19053` | 11009 / 8180 / 9835 |
| `LOS-Regtest-Charlie` | `https://charlie.localhost:19063` | 12009 / 8280 / 9935 |

Use these three distinct browser hostnames to isolate host-only session cookies;
ports alone do not separate cookies. They resolve to loopback in browsers without
changing the operating system's DNS/hosts file. SSH still binds all forwards to
`127.0.0.1`. Certificates include the relevant per-node name plus localhost and
127.0.0.1; migration preserves the existing CA and private keys, reissuing a leaf
only when needed. Internal scripted API checks keep separate cookie jars and use
127.0.0.1 directly. On an OS command-line client that does not resolve these names,
use its explicit loopback-resolution option rather than disabling TLS verification.

On first installation, the triangle provides two peers/channels per node and
liquidity in both directions. Existing laboratories may keep additional channels.
It is useful for routed payments, circular rebalance, batch channel openings and
retirement/succession experiments. Those possibilities are not blanket validation
of every related workflow. Retiring a node closes its channels, including the
other endpoints' sides of those channels. Rerunning installation recreates missing
triangle edges while retaining wallets and unrelated channels.

Old built-in aliases (`LOS-REGTEST-ALICE`, etc.) migrate to the aliases above.
Custom aliases stay unchanged. Initialization compares the actual native alias
to the configuration and briefly restarts only an affected lab LND if needed;
this also converges after interruption. Repeated correct aliases do not trigger
a restart. Login, TLS, wallet identity and user configuration are retained.
If manual experiments exhaust Lightning liquidity, a smoke payment can fail;
the installer does not wipe or forcibly rebalance existing channels to pass it.
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

- Disposable Ubuntu 24.04 amd64 VM, 4 CPUs, 4 GiB RAM and 100 GiB dynamic disk.
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
