# Regtest display and operation clarity — validation, 2026-10-29

## Changes and preserved behavior

- Add Charlie's missing SSH forward to the Portuguese README.
- Select regtest login/sidebar/mobile-brand wording only when the dedicated lab
  deployer has inserted its static notice. This marker is presentation metadata,
  never an authorization or network-policy switch. Ordinary HTML retains the
  original mainnet wording.
- Filter LND's alias-lookup placeholder in native activity and historical
  PostgreSQL activity. Native activity falls back to the existing short public
  key; historical rows with no valid alias omit the optional alias. Amounts,
  identifiers and classification are unchanged. Valid aliases up to 32 bytes,
  including names resembling an error, remain intact.
- Explain the server's disabled manual-target decision and the preview's fee
  budget. Button eligibility, source protections, default fees and custom-job
  overrides are unchanged. No automatic increase from 1 to 100 ppm is introduced.
- Name cooperative/force closes in financial confirmation, preserving the
  existing payment labels, snapshot, fee information and password flow.
- Identify the last peer error as LND history and explain that an earlier
  negotiation can remain recorded after success. Keep its original text,
  timestamp and recent-error emphasis; do not discard diagnostic evidence.

## Executed checks

Ubuntu 24.04 amd64, Core 31.2 and native LND 0.21.5-beta, in a private-network
Linux environment inside a disposable VM. No mainnet node or real funds were
used. Runtime candidates were `37d40dd89f29694aeabcfd136814616686350e87` and the
final alias-preservation refinement `04f4094de9c9ff5f9ec75fb83fb709514bc32497`.
The refinement has identical frontend files to the first candidate.

- Original-code baseline and final candidate: `go test ./...` and `go vet ./...`
  passed. Root was used for ownership tests, with fixed fixture service accounts
  and disk-backed Go temporary storage. An initial unprivileged run and a small
  tmpfs were unsuitable; those setup failures were retained and corrected before
  repeating validation. No test assertion was relaxed.
- The new activity regression test failed on original code at the reported
  lookup placeholder and passed after correction. It also preserves valid names,
  channel identity and amount, including a legitimate error-like 32-byte alias.
- Actual authenticated API/PostgreSQL checks reproduced the historical alias
  leak before deployment, then verified filtering and unchanged control aliases
  and amounts. Final API checks preserved the error-like valid alias as well.
- `npm ci`, `npm run build` and
  `node scripts/tests/test_regtest_presentation.mjs` passed. Actual React browser
  checks covered English/Portuguese lab and mainnet labels; cooperative, force
  and payment summaries; cancellation without execution; disabled-rebalance
  explanation; the explicit 1-to-100 ppm option and historical peer-error text.
  Browser component checks used API fixtures, separately from native tests.
- Native API previews produced 1 sat at 1 ppm and 10 sats at 100 ppm for a
  100,000-sat request, without changing global fee settings. A balanced fixture
  initially had no eligible source and correctly skipped the job. After
  preparing source liquidity, an explicit 100-ppm circular rebalance moved funds;
  successful attempts respected the requested limit. Temporary fixture settings
  were restored.
- A separate extra native channel closed cooperatively after fresh financial
  intent/reauthentication. An identical retry returned the original result;
  mining confirmed the close. The original channel triangle was preserved.
- Deployment preserved native LND PIDs, identities, existing channel sets,
  configuration, passwords and rebalance defaults. It replaced only Manager/UI
  artifacts, without rerunning native provisioning or reopening closed channels.

## Scope limits

Production installers, release catalogs, authentication, financial preparation
and execution, rebalance algorithms and peer RPC error retrieval were unchanged.
Live mainnet, native ARM/WSL2 and a new native force-close experiment were not used
as fixtures. Force-close wording was exercised in the browser; its existing
backend tests ran in the full Go suite. This is regression evidence for the
affected flows, not a claim of exhaustive production testing.
