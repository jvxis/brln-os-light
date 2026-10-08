# Log explorer validation — 0.5.42

Date: 2026-10-08. Base: `7164841f` (`agent/0.5.42-release`).

## Scope and preserved workflows

The Logs page uses a new authenticated `GET /api/logs/query` endpoint. The
existing `GET /api/logs`, `getLogs` helper and journal tail implementation remain
unchanged for the LND restart, LOS/LND/Tor upgrade views and legacy log clients.
No installer, updater, broker protocol, service permissions or database schema
changes are required.

The new endpoint queries retained journal records and grouped Autofee runs by
inclusive time bounds, severity, literal text and (LND only) message category.
Filtering precedes the output limit. LND context and TXT export use the same
sanitized snapshot. Source/read/output limits are disclosed separately.
Docker Bitcoin/Fedimint remain recent-only, with explicit capabilities and
rejection of unsupported filters; they use the existing broker interface.

## Baseline

Before implementation, on Windows amd64 with Go 1.26.0 and Node 24.13.1:

- `go test ./...`: passed at the base commit.
- `npm ci --no-audit --no-fund` and `npm run build`: passed. The existing Vite
  large-bundle warning was already present.
- Reviewed journal timestamp/relative-since tests, app-upgrade filtering and all
  five `getLogs` callers. The old UI only offered a recent tail and no query/export.

## Automated regression checks

From `lightningos-light/` on Windows amd64:

```text
go test ./...
go vet ./...
go test ./internal/server ./internal/system -run 'TestLogQuery|TestJournal' -count=1
```

All passed. Optional Linux journal and PostgreSQL integration tests skip without
their explicit environment variables; those skips are not integration passes.
They were separately enabled and passed in the disposable Linux environment below.

Focused checks cover timezone normalization, inclusive bounds, invalid ranges and
filters, authentication, filter-before-limit with old matching records, context
boundaries, application severity overriding journal priority, representative LND
messages, false positives, sanitization of results/context, empty partial results,
scan/byte/record/output budgets and explicit Docker capability rejection.

From `lightningos-light/ui/`:

```text
npm run build
PLAYWRIGHT_MODULE=<external-playwright-index.mjs> node tests/logs-ui-smoke.mjs
NODE_PATH=<external-node_modules> LOS_UI_TEST_URL=http://127.0.0.1:5184 node tests/browser-smoke.cjs
```

Build and browser tests passed with headless Microsoft Edge on Windows. Vite was
served on `127.0.0.1:5184`. The focused Logs fixture uses API response fixtures;
it exercises the real React page and typed fetch helper in English and pt-BR,
America/Sao_Paulo timezone, and widths 320, 390, 768 and 1440:

- Editing filters makes no request until submission; local times become UTC.
- Context expands; downloaded UTF-8 TXT contains the displayed response,
  metadata and UTC times, excluding expanded context.
- Invalid ranges, incomplete queries, output limits, empty results, errors and
  retry; pending filters disable export and failed queries clear stale results.
- Source switching cancels/ignores earlier requests; Docker capabilities disable
  unsupported fields while keeping recent export available.
- No browser page errors or document horizontal overflow. Desktop and narrow
  layouts were captured; screenshots are local test artifacts, not source assets.
- Existing login/notification smoke tests also passed in both languages.

## Linux runtime validation

An isolated full clone of the powered-off `brln-os-basica` template was created:

- VM: `los-disposable-0542-logs`, UUID `3619cb5d-2889-448e-94cd-1dbf8a4a181e`.
- Ubuntu 24.04.3, kernel 6.14, amd64, 2 vCPUs, 4 GiB RAM.
- Baseline: no LND, Bitcoin or LightningOS Manager service; no node data mounted.
- PostgreSQL 16.15 installed only in this clone for a dedicated local test DB.
- Go test binaries were cross-built on Windows, then **executed on Linux**.

The fixture provisioner is `scripts/test-log-query-linux.sh`. It requires an
explicit disposable-environment flag and refuses existing LND/LOS/Bitcoin or
upgrade units. It creates synthetic journal producers, not Lightning nodes.

```bash
# Build on the host from lightningos-light/:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o ../server-logs-linux.test ./internal/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o ../system-logs-linux.test ./internal/system

# After copying binaries/script into the disposable VM:
LOS_LOGS_DISPOSABLE=1 bash /tmp/test-log-query-linux.sh /tmp/server-logs.test
LOS_LOGS_TEST_JOURNAL=disposable \
LOS_LOGS_TEST_PG_DSN='host=/var/run/postgresql dbname=los_logs_test user=root' \
  /tmp/server-logs.test -test.run TestLogQuery -test.v
/tmp/system-logs.test -test.run TestJournal -test.v
```

All selected tests passed with their integration flags enabled:

- Real journal: 1,500 messages with errors at positions 100 and 300; the query
  finds both despite 1,200 newer normal records. Inclusive exact microsecond
  bounds, chronological output, context and redaction passed.
- Real journal legacy tails: LND, LOS upgrade, LND upgrade and Tor upgrade retain
  two-line limits and RFC3339 `since` behavior. The same journal test also passed
  as an unprivileged disposable user in `systemd-journal`, matching Manager log
  access without granting root.
- Real PostgreSQL: session-local temporary Autofee table with 1,500 runs; time
  bounds, filtering before result limits and summary/seed grouping passed. The
  original Autofee tail still orders by maximum row ID; the structured query
  intentionally orders by timestamp. An initial test expectation incorrectly
  assumed timestamp ordering for the old tail; it was corrected against the
  unchanged implementation and the later inserted fixture seed row.
- Docker adapters: fixed-path placeholder declarations in the disposable VM and
  a synthetic broker client validated Bitcoin/Guardian/Gateway dispatch, the
  500-line cap, redaction, capability metadata, filter rejection and the old
  `lines`/`since` contract. Only fixture-created files were removed by the test.

The VM and synthetic journal units are retained for inspection. No production
node or funds-bearing service was accessed; no protected VM was modified.

## Limits of this validation

- Browser API responses were mocked; native backend queries were exercised
  separately through handlers/readers against real journal/PostgreSQL fixtures.
- Docker adapters used a synthetic broker, not running Bitcoin/Fedimint containers.
  Full Docker period/filter support is deliberately unavailable in this PR.
- LND event classification uses representative upstream messages, not live
  channel opening/closing or force-close transactions. Categories remain heuristic.
- No native arm64 run, installation/upgrade deployment, or live funds operations
  were performed. Installer/updater/broker code and requirements are unchanged.
- Rotated logs cannot be reconstructed. A complete scan of retained records does
  not prove that a historical interval has full coverage.
