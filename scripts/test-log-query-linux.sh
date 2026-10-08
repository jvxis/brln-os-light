#!/usr/bin/env bash
# Synthetic read-only API tests; NEVER run on an installed/funds-bearing node.
# Usage (root, disposable VM): LOS_LOGS_DISPOSABLE=1 ./test-log-query-linux.sh /tmp/server.test
set -euo pipefail
[[ "${LOS_LOGS_DISPOSABLE:-}" == 1 ]] || { echo 'Explicit disposable environment required' >&2; exit 1; }
[[ $EUID == 0 && $# == 1 && -x "$1" ]] || { echo 'Root and a Linux server test binary required' >&2; exit 1; }
for unit in lnd lightningos-manager bitcoind bitcoin lightningos-lnd-upgrade lightningos-app-upgrade lightningos-tor-upgrade; do
  state=$(systemctl show "$unit" -p LoadState --value || true)
  [[ "$state" == not-found ]] || { echo "Existing unit $unit: refusing to use this host" >&2; exit 1; }
done
fixture=$(mktemp /tmp/los-log-fixture.XXXXXX.py)
cat > "$fixture" <<'PY'
import time
for i in range(1500):
    if i in (100, 300):
        print(f'[ERR] SRVR: los-query-test Unable to connect peer=fixture-{i}', flush=True)
    elif i == 299:
        print('[INF] los-query-test password=fixture-secret', flush=True)
    else:
        print(f'[INF] los-query-test background-{i}', flush=True)
    # Preserve distinct journal timestamps, including exact-bound tests.
    time.sleep(0.001)
PY
for unit in lnd lightningos-lnd-upgrade lightningos-app-upgrade lightningos-tor-upgrade; do
  systemd-run --unit="$unit" --property=Type=oneshot --property=RemainAfterExit=yes --property=LogRateLimitIntervalSec=0 /usr/bin/python3 "$fixture"
done
journalctl --sync
LOS_LOGS_TEST_JOURNAL=disposable "$1" -test.run 'TestLogQueryJournalIntegration' -test.v
# Leave fixture units and journal evidence available for inspection. Their names
# intentionally make this script fail closed on repeated execution.
