# Dedicated LOS regtest laboratory

The production LightningOS installers remain mainnet-only. This separate lab
profile runs the same reviewed Manager binary and React build against native
Bitcoin Core 31.1 and two native LND 0.21.5-beta processes on a disposable Ubuntu
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
without new payments; add `--node alice` or `--node bob` to check a single panel.
Without that option, verification makes small test payments.
Run `sudo python3 /opt/los-regtest/control.py status` for native regtest health.
Access, certificate and private-password instructions are below. Production
upgrades/broker/App Store lifecycle remain outside this developer profile.

## Supported workflows and visible limitations

Expand the yellow **O que funciona / Supported features** notice in either panel
to check this scope before using a production menu.

Select **Português** or **English** in the panel toolbar to read one language
at a time. The scrollable table uses distinct status badges; language controls
remain available while you scroll.

| Validated workflow | What you can test |
| --- | --- |
| Wallet and channels | Login, on-chain/Lightning balances, receiving addresses and channel reads. |
| Lightning payments | Create invoices and pay between Alice/Bob, with password confirmation. |
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
| Manual rebalance through the UI | Qualified: moved 49,824 of 50,000 requested sats for 2 sats; job correctly reported partial, with 176 sats remaining. |
| Manual AutoFee | Validated: Dry run preserved policy; Run now applied a change to a test channel. |
| Scheduled automations and failure recovery | Not yet validated: automatic rebalance, auto-heal, HTLC Manager and failure scenarios require dedicated tests. |
| Node Retirement | Validated dry-run and live flow: cooperative close, offline-peer decision, force-close maturity/recovery and final reconciliation. Use a dedicated test node; this retires all its channels. |
| Succession | Qualified local validation: proof of life, grace period, automatic deadline trigger and on-chain transfer. Test database dates were advanced; actual Telegram delivery remains unvalidated. The whole-wallet transfer waits for all current funds to reach its confirmation threshold; new unconfirmed deposits can postpone it. |

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
[LND 0.21.5-beta](https://github.com/lightningnetwork/lnd/releases/tag/v0.21.5-beta).

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
