# Contributing to LightningOS

Issues and pull requests are welcome. This file says how to send them and how the maintainers review
pull requests from outside contributors.

> **Em português:** issues e PRs são bem-vindos. Falha de segurança não vai em issue pública: veja o
> [`SECURITY.md`](SECURITY.md). Abaixo está como mandar um PR e como nós revisamos PRs de fora. O
> LightningOS roda como root em nós com fundos, então a revisão é rigorosa de propósito.

## Issues

Use the issue templates (Portuguese or English). Issues are triaged under
[`.github/ISSUE_TRIAGE_POLICY.md`](.github/ISSUE_TRIAGE_POLICY.md).

Never paste seeds, private keys, passwords, macaroons, cookies, tokens, RPC credentials or wallet backups,
in an issue or anywhere else. Vulnerabilities go through
[private vulnerability reporting](SECURITY.md), not public issues.

## Pull requests

- Open an issue first for anything larger than a small fix, so the approach is agreed before you spend
  time on it.
- Fork the repository and open the PR against the **next release branch**, `agent/<version>-release`
  (the newest one on the repository), not `main`. `main` receives the release branch when a version ships.
- Title it with the version prefix used by the other PRs, e.g. `0.5.39-Beta: <what changes>`.
- Say **what changes for someone running a node** and **how you tested it**, including the node type
  (x86 or Raspberry Pi, `install.sh` or `install_existing.sh`). For a bug fix, describe how the bug showed
  up and how you confirmed it is gone.
- Run the checks for what you touched: `go test ./...` and `go vet ./...` in `lightningos-light/`, and
  `npm run build` in `lightningos-light/ui/`. The repository has no CI, so these are not run for you.
- Keep the PR to one subject. Follow [`AGENTS.md`](AGENTS.md) and
  [`lightningos-light/DEVELOPMENT.md`](lightningos-light/DEVELOPMENT.md).

Not accepted: credentials or filled-in secret files of any kind; new third-party services, telemetry
or download sources without an issue discussed first; new dependencies without a stated reason.

## How pull requests from outside contributors are reviewed

This is the maintainers' policy, published so contributors know what to expect. LightningOS runs as
root on machines that hold Lightning and on-chain funds, and a change that reaches a release reaches
every node that updates, so the bar is high. It applies to the maintainers' own PRs as well.

1. **Read the whole diff before running anything.** Extra scrutiny for:
   - the privileged boundary: `lightningos-light/internal/privileged/`, the broker operations, sudo rules
     and anything that runs as root;
   - the installers and bootstrap: `install.sh`, `install_existing.sh`, `install_existing_pi.sh`,
     `lo_bootstrap.sh` and `lightningos-light/scripts/`;
   - the system templates: `lightningos-light/templates/` (systemd units, `lnd.conf`, `secrets.env`,
     tmpfiles);
   - anything that downloads: new URLs, `curl | sh`, and binaries or images without a pinned version and
     checksum or digest, including the app manifests in `lightningos-light/internal/appmanifest/`;
   - anything that touches LND, macaroons, seeds, wallet files, backups or the firewall;
   - dependency changes: `go.mod`/`go.sum` and `ui/package.json`/`ui/package-lock.json`. Check who
     publishes each new package and what changed in the lockfile.
2. **Never run a PR on a node that holds funds.** Build and test it on a disposable test machine only
   (the project's test VM, or a clean VM made for the purpose), never with real keys or real funds.
3. **CI from outside contributors needs a maintainer's approval**, for every PR and not only the first.
   That covers any workflow the repository gains later.
4. **Merges are squashed** with a message written by the maintainer.
5. **Releases are only made by the maintainers**, after the merge and a test on the test machine. Release
   tags are immutable: once published, a tag cannot be moved or deleted. What a node installs never
   changes under it.
