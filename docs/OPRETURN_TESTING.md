# OP_RETURN validation

Run unit tests from `lightningos-light/`:

```sh
go test ./...
go test ./internal/server -run 'TestValidateAppRegistry|TestOPReturn'
```

`TestOPReturnPostgresLifecycleAndPublication` is opt-in using
`LIGHTNINGOS_TEST_POSTGRES_DSN`. It forces a single connection and `search_path`
to `pg_temp`; every fixture table is session-local and cannot address the
installed application's tables. It exercises install/stop/start/uninstall,
preview leases, fresh reauth, replay protection, fee drift, finalization failure,
timeout reconciliation, and preservation of confirmed local history.

`TestOPReturnRegtestEndToEnd` uses a disposable fixture only when
`LIGHTNINGOS_OPRETURN_REGTEST=1`. It requires:

- a synchronized LND regtest wallet at `127.0.0.1:19009`, with its TLS certificate
  and macaroon under `/tmp/los-opreturn-137-lab/lnd`;
- confirmed spendable regtest funds;
- a Bitcoin Core fixture container named `los-opreturn-137-bitcoin`, with
  `bitcoin-cli -datadir=/regtest` configured for its own regtest wallet;
- Docker access for mining a test block.

The test explicitly verifies **regtest** before funding, and again before
mining. It calls the package-private WalletKit funding implementation; the
production entry point's mainnet guard has no bypass flag. It previews and
checks lease release, rebuilds, finalizes, decodes the transaction, checks the
single zero-value canonical output, publishes, mines a block and checks LND's
confirmed TXID, actual fee and height. Fixture credentials are never printed.
Never point this fixture at a real wallet or modify a node's existing chain data.

The manager uses the same typed LND boundary for managed, native and remote Core;
it neither inspects the configured Core source nor signs/funds via Core RPC.
Record the actual backend used when reporting compatibility validation.

LOS-TEST2 validation for issue #137 used an isolated Core 31.1/LND 0.21.3 regtest
fixture: 23 UTF-8 bytes, one input, 144 estimated vbytes, 144 sat fee, one
confirmation. The installed node's mainnet source was remote Core. No mainnet
message was published by this test.
