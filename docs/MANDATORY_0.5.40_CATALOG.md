# Mandatory 0.5.40 bridge: catalog proposal

Status: implementation prepared for review; repository organization awaiting
the owner's choice. No public repository, tag or release was created by this
work. The 0.5.40 release itself is still unpublished.

## Why a client-only condition is insufficient

Managers already shipped through 0.5.39 call
`https://api.github.com/repos/jvxis/brln-os-light/releases?per_page=10` and
select the first non-draft release with a recognized tag. They do not read a
minimum-version field. A condition added to the 0.5.40 binary cannot change
their behavior before they install it. Prerelease markers do not hide releases
from these clients. Merely marking 0.5.40 as GitHub's latest release is also
insufficient because they use the list endpoint.

## Proposed publication arrangement

- Continue development, issues and PRs in `jvxis/brln-os-light`.
- Publish 0.5.40 as the last release in that repository's legacy catalog.
  Keep it available permanently. Never publish a later release there.
- Publish 0.5.41 and later in `jvxis/brln-os-light-updates`, with release
  immutability enabled before the first publication. Mirror the exact source
  tag/commit there so existing source verification also binds the immutable
  release to its source tree. This is not a separately maintained codebase.
- Source tags may also exist in the development repository. A tag alone is
  not an entry in the releases list consumed by old clients.

The distinction between catalogs must be part of every future publication.
The release preparation script derives the destination from the version and
creates drafts only; publishing still requires review of the release contents.

With a clean checkout and an existing reviewed source tag:

```sh
# Read-only destination/source check; no remote mutation.
python3 scripts/prepare-release-draft.py --tag 0.5.41-Beta --notes-file release-notes.md
# After review and repository setup, push that tag and create a draft only.
python3 scripts/prepare-release-draft.py --tag 0.5.41-Beta --notes-file release-notes.md --execute
```

Use the actual release notes path. The script refuses a source-version mismatch
or a destination with release immutability disabled. It never force-pushes tags,
changes a branch or publishes a release.

## Client behavior

| Installed version | Catalog consulted | Offered version |
| --- | --- | --- |
| Published old Manager, e.g. 0.5.33 or 0.5.39 | Legacy repository, unchanged code | 0.5.40, because no newer releases are published there |
| Updated Manager reporting a version below 0.5.40 | Legacy catalog capped at 0.5.40 | Highest published immutable version through the bridge |
| 0.5.40 or later | Modern catalog | Highest published immutable version after 0.5.40 |
| 0.5.40 while modern catalog is absent or empty | Legacy fallback | 0.5.40, with no upgrade available |

Network, authentication and server errors in the modern catalog are surfaced;
they do not silently fall back. Cache entries are scoped to the selected catalog
so an upgrade cannot reuse a previous catalog's discovery result. Upgrade start
resolves the eligible target again and rejects a different requested version.

The upgrade helper chooses one of the two fixed source/attestation repositories
from the validated target version. It retains the immutable-release, exact tag,
commit, source version, root-owned source and helper-digest checks. There is no
caller-supplied repository URL.

The existing first-install bootstrap still enters through the legacy 0.5.40
release; the newly installed Manager then offers modern releases internally.
Users do not rerun `install.sh` or either existing-node installer.

## Publication prerequisites

1. Obtain the owner's choice of catalog organization.
2. Create the modern public release repository and enable immutable releases.
3. Merge and validate the catalog-aware Manager/helper into the final 0.5.40
   integration branch, alongside the other approved release PRs.
4. Publish the reviewed immutable 0.5.40 release in the legacy repository.
5. Confirm with actual old clients that only the bridge is offered.
6. Prepare 0.5.41 in the modern repository, validate both catalogs, then publish
   the reviewed draft. Do not publish 0.5.41 in the legacy catalog.

Local transport fixtures and unit tests validate routing logic. They do not
constitute public release/catalog validation before these steps are performed.
