# Phase 2 App Store privilege inventory

Captured from `internal/server/apps_registry.go`, the app handlers, and
`internal/server/apps_docker.go` on 2026-08-10. This document is secret-free.

## Catalog shape

The registry exposes 20 app IDs through the common `Info`, `Install`,
`Uninstall`, `Start`, and `Stop` interface.

| Family | App IDs | Current lifecycle boundary | Additional privileged dependencies |
| --- | --- | --- | --- |
| Docker Compose | `bitcoincore`, `bark-wallet`, `electrs`, `mempool`, `fedimint-guardian`, `fedimint-gateway`, `lndg`, `lnbits`, `btcpay`, `robosats`, `publicpool`, `cpuminer`, `tapd` | Generic `runCompose(root, composePath, args...)` with caller-selected paths and argument slices | Docker/Compose installation, image probes and pulls, container inspect/stats/exec/update, Docker network inspection, UFW, LND compatibility changes, and storage repair, depending on the app |
| Native systemd | `elements`, `peerswap`, `loop` | Generic privileged systemd and shell helpers | Units, binaries, config/data trees, users/groups, ownership, firewall, and credential material |
| In-process feature toggle | `depixbuy`, `fswap`, `loopout-brln`, `magma-sales` | Manager service state/configuration | Persistent database or fixed environment state; no container lifecycle |

The 13 Compose apps do not share one safe free-form command shape. Observed
fixed behavior includes whole-project `up -d`, `stop`, and `down`; destructive
`down --volumes`; service-specific start/stop; build; `up --no-start`; Bitcoin
dependency restarts; and container/network inspection. These must become
separate catalog capabilities rather than an argument allowlist.

## First broker manifest

The first migration slice is deliberately limited to `cpuminer` `start` and
`stop` through protocol operation `app.compose.lifecycle`.

- The request contains only `app_id` and the `start` or `stop` enum.
- The manager and broker share the exact catalog Compose document from
  `internal/appmanifest`; the broker rejects any byte change.
- The broker rejects missing, extra, or duplicated environment keys, unknown
  images, non-catalog pool targets, invalid payout/worker values, invalid thread
  counts, non-regular files, and symlinks.
- After validation, Compose receives root-owned temporary snapshots. It never
  receives the manager-writable manifest or environment paths.
- Executable choices are fixed to `/usr/bin/docker compose` and the compatibility
  fallback `/usr/bin/docker-compose`; callers cannot select either path or add
  arguments.
- `shadow` performs validation only and preserves the legacy lifecycle path.
  `enforce` executes the broker action and fails closed.

This slice does not yet migrate CPU Miner install, uninstall, status, config
apply, thread updates, image probes, stats, or counter reads. It therefore does
not authorize removal of Docker access from the manager. Those surfaces remain
explicit Phase 2 work.

## Migration order

1. Prove CPU Miner start/stop and negative validation on a disposable node.
2. Add fixed status/inspection capabilities and migrate CPU Miner completely.
3. Generalize the catalog schema for simple whole-project Compose apps.
4. Add service-specific and dependency capabilities for complex Compose apps.
5. Separate Docker package installation, image management, networking,
   firewall, storage, and LND compatibility operations.
6. Run the complete per-app lifecycle and reboot-state matrix on Ubuntu 24 and
   Ubuntu 26.
7. Remove direct Docker calls and manager Docker-group membership only after
   every supported app passes.

## Acceptance gates for this slice

- strict protocol rejection of unknown apps, actions, fields, paths, and args;
- manifest/environment tampering and symlink rejection before command execution;
- mutation lock and paired audit events for real lifecycle calls;
- no command execution or mutation lock in `dry_run`;
- fixed executable and arguments operating only on validated snapshots;
- full Go tests, vet, registry validation, and privilege-boundary budgets;
- disposable-VM start/stop with final state restored.
