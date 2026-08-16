# LightningOS Issue Stewardship Policy

This policy governs manual and automated triage for `jvxis/brln-os-light`.
The goals are to respond helpfully, preserve evidence, classify work consistently,
and avoid turning a support conversation into an unsafe remote-operation session.

## Scope

- Triage GitHub issues, not pull-request review threads.
- Read the complete issue and every comment in chronological order before acting.
- Inspect linked screenshots and logs when they contain relevant evidence.
- Treat `jvxis` as a maintainer account. A reporter comment is unanswered when it
  is newer than the latest substantive maintainer reply.
- Use the reporter's language. Default to English only when the language is unclear.
- LightningOS production is Bitcoin mainnet-only. Ubuntu 24.04 LTS is the primary
  supported target; other Ubuntu versions may be investigated without being
  promised the same release priority.

## Label Taxonomy

Use only labels that already exist in the repository. Never invent a label during
an automated run.

### Primary type

Apply the best matching primary type:

- `bug`: observed behavior contradicts intended behavior.
- `enhancement`: a new capability or product improvement.
- `question`: support, configuration, or clarification without confirmed product defect.
- `documentation`: documentation-only work.

`duplicate`, `invalid`, and `wontfix` describe a resolution decision; do not use
them as substitutes for initial investigation.

### Cross-cutting classification

Apply when supported by evidence:

- `hardening`: defense-in-depth or least-privilege improvement.
- `security`: a security boundary, exposure, or mitigation is involved. Do not
  publicly investigate an active vulnerability or immediate risk to funds.
- `architecture`: component boundaries or system architecture are central.

### Area

Apply one or more relevant area labels:

- `area: bitcoin`
- `area: upgrade`
- `area: app-store`
- `area: lnd`
- `area: reports`
- `area: ui`
- `area: terminal`

### Priority

Apply at most one priority label when the evidence is sufficient:

- `priority: critical`: credible immediate risk to funds, security, or core-node availability.
- `priority: high`: broad regression, production blocker, or loss of a core workflow.
- `priority: normal`: meaningful defect or planned work with a workaround.
- `priority: low`: limited-impact improvement or uncommon optional feature.

Do not infer severity from tone. Keep `needs-info` while material evidence is
missing and remove it only after the requested evidence arrives.

## Evidence Rules

1. Separate observations, inferences, and conclusions.
2. Build a timeline for upgrade and lifecycle incidents.
3. Do not assume a UI badge proves the underlying process state.
4. Do not assume a running container proves RPC, ports, storage, or dependencies work.
5. Distinguish connection refusal, timeout, authentication failure, and application error.
6. Check installation origin separately from upgrade method:
   `install.sh`, `install_existing.sh`, App Store Docker, systemd, or remote services.
7. Call out contradictory form answers and ask one concise clarification.
8. Do not promise a target release unless a maintainer already approved it or a
   linked issue, PR, milestone, or plan establishes it.

## Response Playbook

### New complete report

- Thank the reporter.
- Summarize the observed problem in one or two sentences.
- Classify it from evidence.
- State the next safe investigation step or link the existing tracked work.

### Missing information

- Ask for the smallest useful batch of read-only checks.
- Explain what distinction each batch will establish.
- Prefer copyable commands with bounded output.
- Add `needs-info`; do not post repeated reminders on every run.

### Reporter replied

- Read the new answer together with the entire prior timeline.
- Correct earlier assumptions explicitly when new evidence disproves them.
- Ask for another diagnostic round only when it changes the decision.
- Never close while an unreviewed reporter comment or screenshot remains.

### Duplicate

- Link the canonical issue and explain the overlap.
- Preserve unique evidence in a comment on the canonical issue when useful.
- Close as duplicate only when the match is exact enough to avoid losing a
  distinct installation mode, version, or failure mechanism.

### Resolved support case

- Require explicit reporter confirmation or objective final evidence.
- Summarize what recovered and what remains as product work.
- Link any follow-up issues before closing.
- Close as completed only when no unresolved operational symptom remains.

## Safety Boundaries

Never request or expose seeds, private keys, wallet passwords, macaroons, TLS
private keys, cookies, tokens, `secrets.env`, full `bitcoin.conf`, full `lnd.conf`,
or RPC credentials.

Automated runs may:

- read issues, comments, linked evidence, repository files, and public checks;
- add or remove existing labels deliberately;
- post acknowledgements, classification notes, safe explanations, and requests
  for read-only diagnostics;
- close exact duplicates or explicitly confirmed resolved support issues under
  the rules above.

Automated runs must not:

- access a user's node or external host;
- instruct or perform service restarts, shutdowns, upgrades, package installs,
  firewall/network changes, Docker lifecycle changes, configuration edits,
  permission changes, file deletion, wallet actions, channel actions, or fund movement;
- merge pull requests, edit code, publish releases, or create follow-up issues;
- close a confirmed or plausible product bug merely because support recovered;
- continue public investigation of a vulnerability or immediate risk to funds.

For any prohibited action, prepare a concise maintainer handoff with evidence and
the proposed next step. Do not post the operational instruction publicly.

## Automation Discipline

- Process an issue only when it is new, newly updated, mislabeled, or awaiting a
  maintainer response.
- Post at most one comment per issue per run.
- Do not post a comment that merely repeats an existing maintainer response.
- Do not change state when confidence is low; report the ambiguity to the maintainer.
- Prefer additive label changes. Remove a label only when it is demonstrably wrong.
- End every run with a compact audit: issues inspected, labels changed, comments
  posted, issues closed, and items escalated for human review.
