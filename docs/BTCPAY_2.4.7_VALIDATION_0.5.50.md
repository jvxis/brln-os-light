# BTCPay 2.4.7 validation for LOS 0.5.50

Date: 2026-10-30.

The Store catalog advances BTCPay Server **2.4.4 → 2.4.7** and NBXplorer
**2.6.13 → 2.6.21**. PostgreSQL remains on major version **16**.
The official [2.4.7 release](https://github.com/btcpayserver/btcpayserver/releases/tag/v2.4.7)
contains security and payment-accounting fixes. The NBXplorer version follows
the upstream Docker project's recommended image at validation time.

## Scope and environment

Baseline source: `fbd3113f0f959b5d6ccdf44fa9e77decabc545f7`.
Candidate application source: `0ee66bbff5f55c5e1d51007fb3e6d47d75750435`.

Native backend checks ran on Ubuntu 24.04, Linux amd64, Go 1.26.10.
Container checks used Docker 29.1.3, Compose 2.40.3, official amd64 images,
Bitcoin Core 31.2 regtest and native LND 0.21.5-beta in a disposable fixture.
No production node or real funds served as a regression fixture.

The container harness renders the real shared catalog, then overrides **only
test configuration** to use regtest, loopback networking, isolated ports and
resource limits. The harness and its credentials remain outside the repository.
On-chain payments use a separate regtest miner; Lightning payments use a
separate fixture's native LND. Neither chain requires restarting an owner's node.

The production manifest stays mainnet. Comparing baseline/candidate Manager
and broker manifests for all four Bitcoin app/remote × Tor on/off combinations
confirmed byte-for-byte equality after substituting the two image tags. SQL
initialization, volumes, networking, PostgreSQL major version and LND connection
settings are unchanged.

## Executed checks

| Scenario | Observable result |
| --- | --- |
| Baseline backend | `go test ./...` and `go vet ./...` passed before the change. |
| Candidate backend | Focused BTCPay/catalog/broker/registry tests, `go test ./...`, `go vet ./...`, native Manager and broker builds passed. |
| Windows amd64 | Focused BTCPay/catalog/broker/registry tests passed. |
| Old container baseline | BTCPay 2.4.4 + NBXplorer 2.6.13 + PostgreSQL 16 registered a merchant, created a store/API key and configured a watch-only wallet and native Lightning receiver. |
| Baseline payments | One Lightning and one on-chain invoice settled with fictitious funds. |
| Existing installation upgrade | Stop old stack, apply 2.4.7/2.6.21 to the same bind mounts, start. Existing store, API access, payment configuration and settled invoice amounts/statuses survived; two new invoices settled. |
| Repeated Stop/Start | Same data and credentials survived another restart; two more invoices settled. |
| Partial startup and retry | Stop stack, start only PostgreSQL, stop it, then start the whole stack. Existing data survived and two more invoices settled. This is a startup retry test, not an interrupted database-migration test. |
| Fresh current stack | Empty PostgreSQL/BTCPay/NBXplorer directories and a new database password: merchant/store/API key creation, watch-only wallet and Lightning setup, then both payment types settled. |
| Credential preservation | Existing `.env`, dedicated macaroon and public TLS certificate hashes were identical across upgrade and repeated restart. No admin macaroon was mounted in BTCPay. |
| Restricted LND access | Node information and invoice receipt worked with certificate verification; empty requests to on-chain SendCoins and Lightning SendPaymentV2 were rejected for insufficient macaroon permissions, before a valid withdrawal was supplied. |

Focused backend command:

```sh
go test ./internal/appmanifest ./internal/privileged ./internal/server \
  -run 'TestBtcpay|TestBTCPay|TestComposeApp.*BTCPay|TestValidateAppRegistry' -count=1
go test ./...
go vet ./...
go build -trimpath -buildvcs=false ./cmd/lightningos-manager
go build -trimpath -buildvcs=false ./cmd/lightningos-privileged
```

The exact catalog guard was advanced alongside the release constants; existing
permission, closed-image and Manager/broker equality assertions were retained.

## Limits and operator procedure

These checks validate actual container migration, persistence and fictitious
payment settlement. They do not claim an end-to-end browser/privileged-broker
mainnet installation test: the payment harness needs a private regtest override
that the production broker deliberately does not accept. Production Bitcoin/Tor
wiring is covered by existing backend tests and the manifest comparison, not a
new live-mainnet/Tor payment test.

Official image metadata includes arm64/arm variants; native execution here was
amd64 only. Installer scripts, the LOS updater and frontend behavior do not
change in this PR. No native arm64, public release assembly or production payment
test was performed. Arbitrary old PostgreSQL major versions and third-party
plugins are outside this upgrade baseline.

After upgrading LOS, use **Apps → BTCPay Server → Stop → Start**. Updating LOS
alone leaves a running BTCPay container unchanged. Preserve the installed app
and its data; do not reset or remove the database to upgrade. Retain a verified
backup before applying an upstream database migration; downgrading only the
container image is not a validated database rollback.
