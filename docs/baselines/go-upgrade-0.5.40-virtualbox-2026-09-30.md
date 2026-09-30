# 0.5.40 privileged upgrade regression — 2026-09-30

Application code tested: `26dc9f5bfff74b6bc066c0b31d6f2593d7d7c15e`, PR #211.
These results advance the draft PR's integration coverage; they do not authorize
a release or replace the remaining architecture and live-node checks below.

## Environment and baseline

- Full disposable clone of the powered-off, unchanged `brln-os-basica` template:
  `los-disposable-0540-upgrade`, VirtualBox 7.2.14, Ubuntu 24.04.3, Linux amd64,
  four vCPUs, 4 GiB assigned RAM, no swap.
- Initial guest had no LightningOS, LND, Bitcoin, Docker, Go or Node runtime.
  Its root filesystem had approximately 88 GiB available.
- Installed versions observed: managed Go 1.26.8, Node 24.21.0, PostgreSQL
  18.6 and LND 0.21.3-beta. The old-updater baseline separately installed the
  official system Go 1.24.12.
- No wallet was initialized. No Bitcoin backend, channels, funds, production
  credentials or production services were used as fixtures. LND remained
  enabled but stopped, awaiting the normal Bitcoin/wallet wizard.
- SSH server identity was compared with the fingerprint read through the VM
  console. Subsequent access used the dedicated test SSH key and pinned host
  identity. API requests verified the guest CA; authentication and CSRF checks
  remained enabled.
- LOS-TEST2 was checked separately, read-only: Manager/broker socket/LND active;
  authenticated health, system-check, LND, Bitcoin-active and apps endpoints
  returned HTTP 200. Its binaries, services and node data were not replaced by
  these integration tests.

Privileged commands ran only inside the disposable guest. Installer logs,
rollback bundles and temporary authentication material stayed in root-protected
guest storage; they are not included in this repository.

## Installer execution and harness corrections

The candidate was exported from the exact commit and run using its documented
development entry point:

```bash
LIGHTNINGOS_INSTALL_SOURCE=checkout ACCEPT_MIT_LICENSE=1 bash install.sh
```

An initial invocation without `LIGHTNINGOS_INSTALL_SOURCE=checkout` correctly
refused an archive lacking the official latest-release bootstrap checkout.

The first complete run used an accidentally restrictive **harness** umask of
077. Dependency installation, compilation and the broker's direct self-test
completed, but the Manager directory and executable were created as 0700. The
service user could not execute the Manager (`203/EXEC`). The installer returned
zero while warning that the Manager was unavailable. This run is **not a pass**.

The harness was corrected to the ordinary umask 022; its two affected Manager
paths were normalized to 0755, and the complete installer was rerun. It reused
the verified Go toolchain, preserved the existing PostgreSQL major and LND
version, built the UI, and produced a healthy Manager and broker. Auth setup and
the seven API checks listed below returned 200. This validates recovery/repeated
installation, with the manual harness correction disclosed; it is not an
untouched clean-OS installation under the corrected harness.

Observed startup checks sometimes raced the listener: both the candidate and
the official 0.5.39 installer warned that port 8443 was not ready immediately
after restarting the Manager. Independent subsequent CA-verified API checks
passed. UFW was inactive in the template, and the installers reported that
condition. Neither warning was silently counted as a functional pass.

### Pristine clean-OS rerun with the corrected harness

A second full clone, `los-disposable-0540-fresh` (UUID
`c1e5b07e-1738-4f1f-8847-ec57a1da1ac7`), started from the unchanged, powered-off
template with Ubuntu 24.04.3/amd64, four vCPUs, 4 GiB RAM and no swap. The
previous disposable clone was shut down normally with the owner's exact-VM
authorization; its disks and results were preserved. LOS-TEST2 stayed running.
The new clone initially had no LOS, LND, Go, Node or PostgreSQL and approximately
88 GiB free. Console-verified SSH identity and the dedicated test key were used.

The same candidate archive had SHA-256
`e6dc7d1acef69db634a0cea645016b93f9bcc3c1d93c9b61625579ffb9ed7cab`
on both the host and guest. The first installer invocation used `umask 022`,
explicit checkout/license acceptance and a transient systemd unit with
`--uid=root`, so HOME was defined. It completed with exit 0 without repeating
installation, manually fixing permissions or preinstalling product prerequisites.

Independent post-install checks passed:

- Manager directory and executable were 0755, executable owner root.
- Manager, broker socket and PostgreSQL 18-main were active. The broker probe
  ran successfully as the unprivileged `lightningos` service user.
- Managed Go and the installed Manager's build metadata both reported 1.26.8;
  no `/usr/local/go` tree was created. Node was 24.21.0, PostgreSQL 18.6 and LND
  0.21.3-beta.
- First authentication setup and all seven authenticated API checks returned
  HTTP 200 using verified TLS. Upgrade status reported current 0.5.40-beta.
- Both application/admin notification PostgreSQL credentials authenticated
  through libpq, without logging credentials or passing them in process args.
- LND stayed enabled but inactive, awaiting the intended Bitcoin/wallet wizard;
  no wallet or Bitcoin backend was initialized by the test.

The installer waited for an initial APT lock and completed its package work.
Its immediate listener check again emitted the previously observed port/health
warnings. Subsequent independent checks above succeeded without service repair
or another installer run. This closes the pristine **amd64** installation gate;
it does not validate wallet initialization or native arm64 installation.

### First LOS installation on an existing native-LND fixture

After the upgrade tests below, the guest's LOS Manager, UI, configuration,
application state and broker installation were archived under root-only storage.
Manager binary and service were confirmed absent. Existing native LND, its
configuration/binaries, PostgreSQL, service accounts and installed prerequisites
were retained. Bitcoin had only an empty directory; this was not a synchronized
Bitcoin node or an initialized LND wallet.

The complete `install_existing.sh` received blank responses selecting its
documented prompt defaults, with license/checkout acceptance as above. A first
service launch omitted the login environment, so Go had neither HOME nor a
module-cache location; it failed before creating the Manager. The harness was
restarted with `systemd-run --uid=root`, which supplied `/root` as HOME. No
application code was changed to accommodate either harness correction.

The subsequent run passed the **absent Manager + absent service** path: compile,
install Manager/service, prepare rollback, stage/commit the privilege boundary,
build UI and enable services. This exercises the first-install ordering that
shell orchestration fixtures alone could not establish.

Postconditions passed:

- LND configuration, `lnd`/`lncli` binaries and a test data sentinel retained
  their hashes; configuration/sentinel ownership and modes were unchanged.
- PostgreSQL's active timestamp was unchanged; LND stayed in its prior stopped
  wizard state.
- Manager and broker socket active; broker self-test passed as `lightningos`.
- Running Manager executable matched the installed file; version 0.5.40-Beta.
- Auth setup and health/system-check/LND/Bitcoin-active/apps/upgrade-status/
  wizard-status returned HTTP 200 with TLS verification.

Two further complete executions covered repetition. Accepting database
provisioning again with blank password answers generated new notification-role
passwords, as `provision_notifications_db` already did in the official 0.5.39
source. A whole-`secrets.env` checksum assertion therefore failed; it was not
reported as unchanged. Login with the existing UI password and API checks still
passed. A separate repetition declined database reprovisioning: both the LOS
configuration and secrets file then remained byte-for-byte unchanged, as did
the LND preservation checks and PostgreSQL active timestamp. Direct libpq
connections using the configured notification app/admin DSNs succeeded without
printing or passing their credentials as process arguments or environment
variables.

## Native Go regression

The installed, checksum-verified Go 1.26.8 executed:

```bash
umask 022
GOTOOLCHAIN=local GOENV=off GOWORK=off GOMAXPROCS=2 go test -p 2 ./... -count=1
GOTOOLCHAIN=local GOENV=off GOWORK=off GOMAXPROCS=2 go vet -p 2 ./...
```

Both passed on Linux/amd64 as root in the disposable VM, including the
Linux/root-specific package tests skipped by the Windows run. Module/build
caches were explicitly assigned in the harness.

An earlier test run inherited umask 077 and failed file-mode fixture assertions.
Three representative failures (Bitcoin credential storage, LND credential
transaction/rollback and mesh binary repair) were reproduced against the
official 0.5.39 source with the same umask. The entire candidate suite passed
after correcting the harness to 022. Neither the assertions nor application
permission validation were weakened.

## Real preflight, snapshots and rollback

The installed candidate broker's root CLI was exercised against an inert
credential-file fixture owned by the guest's actual LND identity:

- Mode 0755 rejected with `admin_macaroon_mode`; 0600 and 0640 accepted by
  dry-run preflight. Configuration, binaries and running Manager PID unchanged.
- Preparing a real rollback bundle marked the transaction pending. A second
  preparation refused it and preserved the original snapshot.
- Real configuration/UI changes were restored by the shipped rollback helper,
  using actual systemd units and the socket-activated broker.
- Repeated rollback remained healthy. A subsequent preparation archived the
  previous bundle and captured current state.
- A legacy-bundle fixture omitted the saved broker binary, tmpfiles and units.
  Rollback retained the healthy recovery-capable installed transport, recorded
  the recovery marker, and passed a service-user broker probe and verified HTTPS
  health request. PostgreSQL and LND state were preserved.

These were root integration operations, not mocked `systemctl`/`runuser` calls.
The inert credential fixture did not exercise a successful LND macaroon RPC.

## Actual old-updater transition, failure and API retry

The official 0.5.39 installer/source at
`ea829834be0e85c12c41775da7cc84b209ef0857` was installed in the guest with the
official Go 1.24.12 archive. Health/authentication/broker baseline passed and the
API reported 0.5.39-Beta. This baseline reused the disposable guest's installed
prerequisites and data; it was not a second pristine OS installation.

Because 0.5.40 was unpublished, a **guest-local TLS release/Git transport fixture**
served its metadata and exact candidate commit. A short-lived CA and guest-only
host resolution mapped GitHub hostnames to the loopback server. Manager/broker
source, embedded updater bytes and release checks were unmodified. No public
tag or release was created, and release/API TLS verification was not disabled. This tests
the installed updater's real transition while substituting release transport;
it does not attest a publicly published release.

1. Prepared an old-helper snapshot containing the real 0.5.39 Manager/UI, then
   omitted broker backup artifacts to model a historical pre-broker bundle.
   An inert LND-owned admin file with mode 0755 reproduced the prerequisite
   failure. The snapshot included that original mode.
2. Authenticated to the **0.5.39 Manager API**, obtained CSRF, and requested
   `POST /api/app/upgrade/start`. HTTP 200 launched the actual embedded old
   helper, whose installed SHA-256 remained
   `6a6d39d79d642d4565aba4778bd381d9f72e24d96d105d1e597b9b2eb6ee1a4c`.
3. The old helper authenticated the fixture release, compiled the candidate,
   published its broker, and failed at credential convergence with the expected
   mode diagnostic. Its explicit rollback and EXIT-trap rollback both ran.
4. The restored 0.5.39 configuration/Manager/UI matched their baseline hashes.
   Verified health returned 200; the retained candidate broker accepted the
   old Manager's self-test. PostgreSQL had not restarted.
5. Corrected the inert file to 0640 and retried through the same authenticated
   API. The retained broker accepted the exact old helper only for 0.5.40.
   Upgrade completed to 0.5.40; build metadata confirmed **Go 1.24.12**.

LND was uninitialized: credential convergence completed in the supported
pending state. This does not validate credential baking or wallet/channel
functionality against a running funded node. The inert file was removed before
the next upgrade test.

## Upgrade performed by the new 0.5.40 updater

A local-only future fixture based on the exact candidate changed only `go.mod`
to Go 1.26/toolchain 1.26.8 and `ui/public/version.txt` to 0.5.41-Beta. Its local
commit was `cea7e6db5180a14328d327f4f4c3c6c62c783365`; this is not a public release.

The managed Go 1.26.8 directory was moved out of the toolchain store to ensure
it was absent. The system Go remained 1.24.12. The real installed 0.5.40 Manager
accepted an authenticated API upgrade to this fixture and installed its new
helper with SHA-256
`d6fe92b0e689ff59ec8993d9f906373c56e365538bc42928ea3ceda012decc2f`.

The updater downloaded and verified Go 1.26.8, prepared the versioned store,
built Manager/broker/mesh/UI, archived the historical rollback bundle, captured
current state and committed the upgrade. Running health/API/broker checks
passed; Manager build metadata reported **Go 1.26.8**. The system Go binary's
hash was unchanged. Neither first-install script was used for either API upgrade.

The temporary GitHub host mappings, loopback service and added CA trust were
removed after these tests. The final existing-node installer tests again used
the ordinary public network and candidate 0.5.40 source.

## Browser regression and LOS-TEST2 baseline

Firefox 141.0 (the Ubuntu template's installed Snap browser) ran headlessly
through geckodriver 0.36.0 inside the disposable guest. Its isolated profile
trusted the node CA, with `acceptInsecureCerts=false`. No host trust store was
changed. Browser credentials were supplied in memory; password saving was
disabled.

- Real 0.5.40 login page rendered, rejected an incorrect password with a visible
  error, accepted the test password and opened the welcome wizard for the
  uninitialized node. Real logout returned to the login page.
- For dashboard-only UI checks, a browser `fetch` wrapper changed only the
  wizard's `wallet_exists` response. The installed application and backend
  remained unchanged. The real release response displayed current 0.5.40 and
  public latest 0.5.39, with no upgrade available.
- Browser-only future-release metadata enabled the upgrade button. Clicking it
  opened a confirmation naming 0.5.41. Cancelling sent no upgrade request.
  Confirming once received an intentionally simulated HTTP 503, displayed the
  error and left the modal closable. These are UI fixture tests, not another
  successful backend upgrade. The backend transitions are documented above.
- The same browser then logged into the actual LOS-TEST2 over CA-verified TLS,
  rendered its real dashboard, confirmed that its 0.5.39/public-0.5.39 state
  disabled the upgrade button, and logged out. No upgrade or service action was
  submitted to LOS-TEST2.
- The separate authenticated LOS-TEST2 baseline returned HTTP 200 for health,
  upgrade status, wizard status, LND, wallet summary, Bitcoin source and apps.
  Its wallet existed and was unlocked; LND was synced to chain and graph, with
  three active channels. Its active Bitcoin source was remote. Manager, broker
  socket and LND were active. The post-browser check again confirmed health OK,
  Bitcoin RPC reachable, LND active/unlocked/synced and the same three active
  channels. This node was not used for fault injection.

The harness initially selected I2P's occupied port 4444 and a directory outside
the Firefox Snap's writable profile area. It was corrected to loopback port
4445 and an isolated profile under the Snap common directory before successful
browser execution. An incorrect heading selector was also corrected to the
rendered `App upgrade` label; application assertions were not weakened.
Screenshots were visually inspected; the browser session was closed afterward.

## Remaining release gates

A subsequent [RPi4 read-only baseline](go-upgrade-0.5.40-rpi4-2026-09-30.md)
identified an existing native ARM64 node on 0.5.28 with two active channels.
Its published updater asset matches the tested legacy helper. The owner then
explicitly waived native ARM installation/upgrade validation for this delivery
and requested installer parity review instead. The [parity review and focused
Linux tests](../GO_UPGRADE_0.5.40.md#existing-node-installer-parity-review--2026-09-30)
passed. Native ARM execution remains untested, not passed, and is no longer a
required gate for this delivery under that explicit instruction.

- Browser-observed successful upgrade through Manager restart/reconnection.
  Login/dashboard and confirmation/cancel/error flows now have browser coverage;
  successful backend upgrade transitions above used the authenticated API.
- Initialized-wallet credential baking/verification and an assisted upgrade
  of the actual LOS-TEST2 installation. Fault injection must stay in disposable,
  unfunded environments. The direct starting-version test here was 0.5.39,
  not an installed 0.5.33 Manager.
- Public release/catalog enforcement of the mandatory 0.5.40 bridge. Local
  fixture ordering does not prove old clients will be prevented from skipping it.

PR #211 remains draft. These tests do not add a PostgreSQL major-version
migration or change the requirement to validate one separately.
