# 0.5.46 Manager bridge firewall — PR #267 / issue #264

## Scope and migration

Community and Bark use port 8443 for sensitive operations that require Manager
authorization. Their normal UI/other endpoints are not all behind this check.
This change preserves the proxy, TLS trust, recent-reauth checks, images, ports,
app data and lifecycle. It only changes the bridge firewall preparation.

On each **start/install through LOS** with active UFW:

1. Discover the app's Docker bridge/subnet; reject invalid input before mutation.
2. Add the restricted source-subnet rule first. An add failure leaves old rules.
3. Remove the broad rule only for that same current bridge. A delete failure
   keeps the restricted path and returns an error; retry finishes the migration.
4. Preserve every other bridge's rules, whether or not that interface exists.

There is deliberately no automatic restart or global cleanup on Manager startup
or release upgrade. An operator upgrading from an earlier release should use
the App Store stop/start flow for **each installed affected app**, when convenient,
then apply/verify the desired Manager policy in System Check. Stopped apps can
wait until needed. A running second app's old broad rule remains until that app
is migrated, so System Check may correctly remain non-protected meanwhile.
The narrow change does not make a bare Docker restart or a host reboot a LOS
migration action.

If an old rule refers to a deleted/recreated bridge, its ownership cannot be
proven from an absent interface. Preserve it and the warning for individual
operator review; do not bulk-delete `br-*` rules. This also protects externally
managed services on install_existing nodes. Rules restricted to a former subnet
are likewise not guessed to be disposable. No changes to SSH, LND or Bitcoin
services/configuration are part of this migration.

Rollback to the old broker can reintroduce the broad rule on a subsequent app
start. There is no irreversible data/schema migration; retain this operational
limitation when evaluating rollback. Hardwallet's separate 0.6.0 PR #260 still
needs its own adoption of this policy; it is not silently modified by PR #267.

## Baseline and regression evidence (2026-10-16)

Reviewed starting point: `9aa6171ca8620b3ccb9825f18727e8f35bbbc601`, base
`2179b7983ff4513005272538c1461dea71d6bad9`. Clean worktree before changes.
Baseline Windows amd64 `go test -count=1 ./internal/privileged ./internal/server`
passed. Windows skips some symlink/ownership and Linux-only checks; those are
not counted as executed Linux validations.

New stateful regressions failed against that starting implementation for add
failure, loss of access during migration, delete failure/retry, and migration of
two apps that wrongly removed another app's rule. They pass with the correction.
The tests assert retained firewall state/access, not just a copied command list.
Additional tests cover cancellation before mutation and after the replacement,
retry, unrelated service ports, invalid subnet rejection and idempotence.

Commands from `lightningos-light/`:

```text
go test -count=1 ./internal/privileged -run 'TestManagerBridge|TestRestrictedBridge|TestBroadRuleParsing'
go test ./...
go vet ./...
```

Final Windows amd64 `go test -count=1 ./...` and `go vet ./...` passed. No UI,
API, manifest or installer implementation was changed by this follow-up.

## Disposable Linux gate

VM: `los-disposable-0542-logs`, UUID
`3619cb5d-2889-448e-94cd-1dbf8a4a181e`, Ubuntu 24.04.3 amd64,
kernel 7.0.0-38-generic, 2 vCPU / 4 GiB. No active Manager, broker socket,
LND, Bitcoin or Docker at baseline. UFW inactive with no added rules.
Installed Docker from Ubuntu packages for this isolated test (29.1.3).
The test container is BusyBox 1.37.0, pinned to manifest digest
`sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e`.

`TestManagerBridgeFirewallLinuxGate` is opt-in and refuses active node services,
running containers, existing app networks, an existing tailscale0 interface, or
an active/non-empty UFW. It uses real Docker/UFW, actual native app firewall entry
points, a dummy HTTP listener at 8443 and disposable probe containers. It does
not initialize wallets, issue transactions or use production nodes.

Enable only on such a disposable host, with the checkout's exact shell helper:

```bash
export LIGHTNINGOS_BRIDGE_FIREWALL_GATE=1
export LIGHTNINGOS_MANAGER_FIREWALL_TEST_HELPER="$PWD/internal/server/assets/lightningos-manager-firewall.sh"
go test -count=1 -v ./internal/privileged -run TestManagerBridgeFirewallLinuxGate
```

For this run, the Go test binary was cross-built on Windows and **executed
natively as root in the Linux VM**. This is not cross-compilation-only evidence.
The gate exercises blocked-before-allow, both app bridges, real IPv4/IPv6 broad
rule removal, add/delete failure injection and retry, repeated application,
unrelated Bitcoin/LND/app/SSH rules, explicit preservation of an orphaned rule,
repeated LAN/VPN policy application and network recreation. `tailscale0` is a
dummy test interface: this does not validate a Tailscale daemon or remote VPN.
Cleanup disables the test UFW and deletes only fixture-created rules/networks;
it never calls `ufw reset`. Logs remain under
`/var/lib/los0546-firewall-validation` in the disposable VM.

Final Linux run: **passed**, including network recreation and LAN/VPN switching
(75.14 seconds for the real integration gate). The focused native Go checks also
passed, including the symlink-safety check skipped on Windows. Post-test UFW was
inactive with `(None)` added rules; no probe containers remained. This restores
the initial firewall state, not an unrelated node's firewall configuration.

## Remaining release gates

Keep the PR **draft** until critical remaining flows are exercised:

- Real Community/Bark proxy/TLS and authenticated sensitive UI operations with
  disposable identities. TCP/HTTP reachability plus unit authorization tests is
  not the actual app's full authenticated browser flow.
- Actual installed 0.5.45 UI updater transition, followed by migration of both
  already-installed apps; fresh install.sh/install_existing.sh paths and relevant
  interrupted/retry/rollback lifecycle scenarios.
- Native ARM64 where applicable. No ARM64 runtime validation is claimed.
- Do not claim all optional apps were installed/exercised: the shared suites and
  preservation of their firewall rules are narrower evidence.

No production deployment, release publication or issue closure is part of this
validation. The disposable VM and evidence are retained; cleanup/power-off is a
separate owner decision.
