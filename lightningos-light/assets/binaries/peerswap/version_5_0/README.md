# Peerswap binaries

## Bundle shipped with LightningOS 0.5.45-Beta

The supplied Linux amd64 binaries are pinned in `internal/appmanifest/peerswap.go`:

| Binary | Version | Source commit | SHA-256 |
| --- | --- | --- | --- |
| peerswapd | v7.0.2 | 62b80bf6b2e3392f0f61352560bccbb1e81bef65 | 27c7b166707bba2020add3c837213c8354a147e42e6c9ee9b5110116b8c94c1f |
| pscli | v7.0.2 | 62b80bf6b2e3392f0f61352560bccbb1e81bef65 | 0f19eb7f2e968ce7239be011c5fd2c3a0d511e82851673a6eb74d29822b8d32f |
| psweb | v7.0.2 | a50002685808f7a47f0758827f355f8a80bf666a | 39c55c62a4aacd68164ec3e556086b95a7e21c72dfd6a1acf1441293c7b5b0d0 |

The commits match upstream [PeerSwap v7.0.2](https://github.com/ElementsProject/peerswap/releases/tag/v7.0.2)
and [PSWeb v7.0.2](https://github.com/Impa10r/peerswap-web/releases/tag/v7.0.2).
Embedded Go build metadata reports clean source trees, Linux/amd64, Go 1.23.4
for peerswapd/pscli and Go 1.27.2 for psweb. Commit metadata and hashes identify
the supplied artifacts; they are not a claim of reproducible-build verification.

The legacy directory name `version_5_0` is retained for packaging compatibility;
it is not the application version. No arm64 binaries are included in this bundle.

## Updating an installed app

First upgrade LightningOS so the Manager, privileged broker and staged assets
belong to the same release. Then use **Stop**, wait for PeerSwap to stop, and
**Start** in the App Store. Do this when no swap is in progress.

Stop does not depend on the new bundle checksums or Elements/LND credentials.
Start invokes the existing broker Ensure operation: it validates the staged
hashes, installs all three binaries and the version marker, preserves existing
runtime data and supported operator configuration, then starts both services.
The local/remote Elements choice is retained. No LND restart is required by this
bundle update. Do not uninstall the app to update it.

On the target host, copy these into:
/opt/lightningos/manager/assets/binaries/peerswap/version_5_0/<arch>/

Updating only these files on an older LightningOS release is not sufficient:
its compiled checksum allowlist will reject the new bundle on Start.
