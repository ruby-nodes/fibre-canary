# Fibre Canary V1 Tasks

Status: **Proposed — 1–2 day v1**  
Architecture: [ARCHITECTURE.md](./ARCHITECTURE.md)

The target is a usable Mocha binary, not a production accounting system.

## M1 — CLI and strict safety configuration

- [x] Confirm target Mocha chain, app, and Fibre versions.
- [x] Pin the compatible `celestia-app` dependency.
- [x] Initialize Go module and CLI with `preflight` and `run`.
- [x] Implement strict versioned TOML parsing.
- [x] Reject unknown fields, unsupported networks, and chain mismatches.
- [x] Require key name, backend, and keyring directory.
- [x] Force auto-funding off.
- [x] Validate payload, payment, timeout, and local daily-run limits.
- [x] Reject secrets and credential-bearing endpoints.
- [x] Test configuration and redaction.

Acceptance criteria:

- CLI builds and command help is useful.
- Only the pinned Mocha tuple is accepted.
- Invalid config fails before network or key operations.
- No private key, mnemonic, or passphrase is supplied in config/CLI.

## M2 — Receipts, locking, and crash acknowledgement

- [x] Define receipt version and statuses.
- [x] Implement atomic JSON replacement with fsync and rename.
- [x] Enforce owner-only data/receipt permissions.
- [x] Add a single-process data-directory lock.
- [x] Write `started` before invoking `Put`.
- [x] Convert stale `started` receipts to `inconclusive`.
- [x] Refuse a new paid run while an inconclusive receipt exists.
- [x] Add `--accept-possible-duplicate-charge`.
- [x] Reference acknowledged prior run IDs.
- [x] Implement local `max_runs_per_day`.
- [x] Test all transitions and crash boundaries.

Acceptance criteria:

- Concurrent local runs are prevented.
- A crash during fake `Put` leaves an inconclusive receipt.
- No automatic retry exists.
- Retry after ambiguity requires explicit acknowledgement.

## M3 — Preflight and official `fibre.Put`

- [x] Construct official Fibre and transaction clients.
- [x] Verify chain identity and pinned compatibility.
- [x] Load key identity and query available Fibre escrow.
- [x] Generate a canonical unique bounded payload.
- [x] Calculate encoded size and payment with the pinned client.
- [x] Enforce payment and payload ceilings before `Put`.
- [x] Implement official `fibre.Put`.
- [x] Store BlobID, tx hash, height, and duration after success; omit validator signatures.
- [x] Classify an ambiguous `Put` error as inconclusive.

Acceptance criteria:

- Preflight reports account, endpoint, version, payment, escrow, limits, vantage.
- Pricing is not copied or hard-coded.
- Over-limit payment fails before `Put`.
- Fake success produces `put_succeeded`; ambiguity produces `inconclusive`.

## M4 — Download verification and release

- [x] Download immediately after successful `Put`.
- [x] Use publication context required by the pinned client.
- [x] Compare exact bytes and SHA-256 hashes.
- [x] Emit `succeeded`, `published_unverified`, and `integrity_failure`.
- [x] Define stable exit codes and sanitized errors.
- [x] Add `config.example.toml` and README setup/run instructions.
- [x] Document operational fee bounding and duplicate-charge crash risk.
- [x] Document the one-vantage/non-global claim.
- [ ] Run one opt-in live Mocha smoke test.
- [x] Publish a sanitized example receipt.
- [x] Build a reproducible Linux binary with checksum.

Acceptance criteria:

- Real Mocha run publishes, settles, downloads, and verifies identical bytes.
- Receipts contain no secret/keyring/credential material.
- Another operator can complete `preflight` from README alone.
- The accepted crash limitation is explicit.

## Optional only after M4

- [ ] Atomic OpenMetrics text-file output.
- [ ] Minimal container image.
- [ ] Cohort application screenshots and receipt fixtures.

## Deferred

- Custom Upload/PFF lifecycle and exact reconciliation.
- Upstream lifecycle API and append-only journal.
- Multi-endpoint comparison and fine-grained stage timings.
- Hard transaction-fee ceiling.
- Automatic retries, auto-funding, scheduler, daemon, or HTTP service.
- Website, Grafana, alerts, Mainnet, and multi-account operation.
- Receipt signing, remote signers, user files, and validator rankings.
