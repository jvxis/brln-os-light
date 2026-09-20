# App Store catalog upgrades

The supported user workflow is **Stop, then Start** after upgrading LightningOS
to a release containing the newly approved app version. This is not an upstream
auto-updater: the LightningOS catalog still chooses the image, digest or binary.

## Separate status, stop and start

- **Status** observes Docker using fixed catalog project/service labels. A stale
  image or declaration must not turn a running app into `unknown` or hide Stop.
  This observation does not authorize execution of that declaration.
- **Stop** identifies all active non-one-off containers in the catalog project,
  verifies every full ID and ownership label, and stops dependents before their
  databases. No installed Compose file, image reference, secret or executable is
  trusted to stop the runtime. Sidecars and services removed from later catalog
  versions are included. No image is downloaded and no volume is removed.
- **Start** prepares the current approved images and declaration through the app's
  existing preparation/migration path. The broker still validates execution
  snapshots, credentials, storage, dependencies and image attestations where
  applicable. Old versions must not be added to execution allowlists merely to
  make Stop work.

This removes the installed-image-version dependency, not upstream migration
requirements. Database/schema incompatibilities, unsupported architectures,
missing storage, unavailable dependencies and Docker failures can still prevent
an upgrade. Unknown identities fail closed; manually relabeled projects are not
automatically adopted. Stable catalog project identifiers are a compatibility
contract. Changing one requires an explicit, tested identity migration.

## Coverage and exceptions

The shared Docker stop path covers LNbits, LNDg, BTCPay, Electrs, Mempool,
Fedimint Guardian/Gateway, CPU Miner, RoboSats, Public Pool, Bark Wallet,
BRLN Community and Taproot Assets. CPU Miner selects/prepares the currently
approved CPU-compatible image on Start; RoboSats prepares all three images.
Existing pool/payout settings and persistent app data remain in place.

Bitcoin Core retains its dedicated storage-enrollment and migration protections.
Elements and Lightning Loop retain their native service preparation paths.
PeerSwap also uses native services: Stop must not require a healthy Elements
source or currently readable credentials. Internal apps do not have independent
Docker image upgrades. This change does not generalize uninstall behavior or
change network modes, TLS management, macaroon permissions or image versions.

## Regression checklist for future catalog changes

1. Test Status and Stop against an older declaration/image, not just a fresh install.
2. Verify Stop never pulls an image, executes old Compose, rewrites settings or
   touches LND/Bitcoin or containers outside the app project.
3. Include partially running stacks, replicas, stopped stacks and legacy Compose
   without dependency labels. Keep the legacy dependency order up to date if a
   stack changes topology.
4. Verify Start prepares the new approved artifacts, preserves user data/settings,
   and fails closed if preparation fails. Review upstream schema migrations.
5. Run `go test ./...`. Shared stop/status regression coverage is in
   `internal/privileged/apps_stop_test.go`; Community's previous-release fixture
   exercises Stop, current preparation and Start with persistent-file hashes.
6. Before release, perform an assisted Linux/Docker smoke test of Stop/Start and
   app functionality. Mock-command tests and cross-compilation are not live tests.
