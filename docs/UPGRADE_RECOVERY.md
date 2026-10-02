# Recovering a LightningOS upgrade

Use this procedure when an upgrade failed during privilege cutover, or the
panel reports that the privileged upgrade service is unavailable. Do not rerun
`install.sh`, `install_existing.sh` or `install_existing_pi.sh` to update LOS.
Those are first-installation entry points.

## Identify the failure

On the affected node, inspect only service state and bounded logs:

```bash
systemctl is-active lightningos-manager lightningos-privileged.socket
sudo journalctl -u lightningos-manager -n 60 --no-pager
sudo tail -n 60 /var/log/lightningos-app-upgrade.log
```

The upgrade log may not exist or may describe an earlier attempt if the broker
could not start the new upgrade. The Manager journal records start failures.
Do not publish complete configurations, credential files or unredacted logs.

From 0.5.40, credential failures carry a bounded diagnostic code in the broker
response and completion audit. For `admin_macaroon_mode`, first compare the
file's numeric owner/group with the effective `User` and `Group` of `lnd.service`
(an empty `Group` means the user's primary group):

```bash
systemctl show lnd.service --property=User --property=Group
sudo stat -c 'owner=%u group=%g mode=%a' /data/lnd/data/chain/bitcoin/mainnet/admin.macaroon
```

Once ownership has been confirmed, change only the reported unsafe mode to
`0600`, then retry the upgrade. Do not assume LND runs as `lnd`: existing-node
installations may use `admin` or another service identity. Do not print or copy
the macaroon's contents. Owner, service identity, unsupported path and incomplete
transaction errors require resolving that specific condition first.

## Broker available: retry through the panel

After fixing the reported prerequisite, retry the upgrade in the panel. A
successful retry to 0.5.40 installs a matching Manager/broker pair. The recovery
broker accepts the exact helper shipped in 0.5.33 through 0.5.39 only for the
0.5.40 bridge. It does not allow that old helper to skip to 0.5.41 or later.

The rollback preserves a healthy, recovery-capable broker only when its snapshot
contained no previous broker. If the snapshot contained a broker, it restores
that broker with its previous Manager and socket configuration. A broker that
fails validation is not retained as a working upgrade transport.

## Broker missing: manual upgrade from a trusted checkout

This path is for an administrator with root access. Merely starting the socket
cannot restore a missing executable or missing unit files.

Select a **published** release and independently verify its full commit SHA.
For the mandatory bridge use `0.5.40-Beta` once it is published; a development
branch or draft PR is not a published recovery release. Use a new, root-owned
checkout under a root-controlled directory, for example:

```bash
TAG=0.5.40-Beta
COMMIT='<verified full 40-character release commit>'
sudo install -d -o root -g root -m 0755 /opt/lightningos-recovery
sudo git clone --branch "$TAG" --single-branch https://github.com/jvxis/brln-os-light.git /opt/lightningos-recovery/source
sudo git -C /opt/lightningos-recovery/source rev-parse HEAD
```

Compare the printed SHA to the independently verified value. If the directory
already exists, inspect it or choose a new empty directory; do not overwrite or
delete an existing checkout. Then run the upgrade helper from that checkout:

```bash
sudo bash /opt/lightningos-recovery/source/lightningos-light/internal/server/assets/upgrade-app.sh \
  --trusted-checkout --version "$TAG" --commit "$COMMIT"
```

`--trusted-checkout` deliberately relies on the administrator's source trust.
It still checks the commit and source version. It must not be used with an
unreviewed user-writable checkout. The helper performs the upgrade and provisions
the broker; no first-installation script is involved.

If the helper reports an interrupted cutover, recover the pending transaction
first with the installed root-owned recovery command, then retry:

```bash
sudo /usr/local/sbin/lightningos-rollback-privilege-cutover
```

The new updater refuses to replace a pending snapshot. Finished snapshots are
archived under `/var/lib/lightningos/rollback/0.5.3-privilege-cutover.previous.*`
before capturing the current installation. These root-only directories may
contain credentials; preserve them for recovery and do not publish their contents.

## Verify recovery

```bash
systemctl is-active lightningos-manager lightningos-privileged.socket lnd
sudo -u lightningos /opt/lightningos/manager/lightningos-manager broker-self-test
cat /opt/lightningos/ui/version.txt
```

Confirm in the panel that LND connectivity and the expected node functions are
healthy, and that another upgrade check works. Preserve the failure and retry
logs for review. Bitcoin, LND and application data are outside the rollback's
file replacement scope; this is a Manager/privilege recovery operation.

## Transition limitation

An installed old Manager runs its **embedded old updater** to enter 0.5.40. That
attempt cannot use the new early preflight or fresh-snapshot orchestration.
It does load the target checkout's rollback helper and install the target broker,
so the recovery transport and specific credential diagnostics can apply during
that transition. A node already missing its broker needs the manual recovery
above to receive these changes. Release validation must exercise this old-updater
transition, not just run the new helper against old source code.
