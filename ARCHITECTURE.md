# Fibre Canary Architecture

Status: **Proposed — simplified v1**  
Design packet: v2  
Date: 2026-10-29  
Delivery target: usable Mocha CLI in 1–2 days

## Product

`fibre-canary` is a small Go CLI answering one question:

> Can this publisher, from this vantage point and through this Celestia
> endpoint, publish a real Fibre blob, settle it, download it, and recover the
> exact original bytes?

V1 has two commands:

```text
fibre-canary preflight
fibre-canary run [--accept-possible-duplicate-charge]
```

It verifies one publisher path. It does not claim global Fibre availability or
rank validators.

## V1 decisions

- Use official `fibre.Put` as the protocol boundary.
- Download the returned BlobID and compare exact bytes.
- Publish only a small tool-generated payload; do not accept user files.
- Use a dedicated, low-balance Mocha account.
- Force `Escrow.AutoFund=false`.
- Persist a simple intent receipt before `Put` and update it after results.
- Treat an interrupted or ambiguous `Put` as `inconclusive`.
- Require explicit acknowledgement before retrying after an inconclusive run.
- Enforce payload, Fibre payment, escrow, and local daily-run limits.
- Produce terminal output and one versioned JSON receipt per run.
- Pin one tested Mocha-compatible Celestia/Fibre version and fail closed for an
  unsupported runtime.

## Accepted crash trade-off

Black-box `Put` may crash or fail after a signed promise has escaped without
returning its identity. A later retry creates a new promise, so both attempts may
eventually be charged.

V1 accepts that risk because it runs on Mocha, uses a dedicated low-balance
account, disables auto-funding, never retries automatically, limits local run
frequency, and requires:

```text
--accept-possible-duplicate-charge
```

The flag does not claim the old run was unpaid. It records explicit operator
acceptance of a possible second charge.

## Included scope

- Strict versioned TOML configuration.
- Read-only preflight.
- Single-process file lock.
- Canonical unique payload generation.
- Payment calculation from the pinned Celestia implementation.
- Payment, payload, escrow, and local cadence limits.
- Official `fibre.Put`.
- Download and byte-for-byte verification.
- Atomic JSON receipt updates.
- Stable statuses, error stages, and exit codes.
- One opt-in live Mocha smoke test.

## Deferred from v1

- Custom `Upload` and PFF transaction flow.
- Promise-level reconciliation and a `reconcile` command.
- Upstream before-dispatch API work.
- Append-only event journal and multi-endpoint reconciliation.
- Exact quorum, broadcast, and confirmation timings.
- Per-validator failure attribution and hard transaction-fee ceiling.
- Automatic retry, scheduler, daemon, or HTTP server.
- OpenMetrics, Grafana, alerts, website, and Mainnet.
- Receipt signing, remote signing, and user file uploads.

## Run flow

```text
load strict config
  -> acquire local lock
  -> inspect prior receipts and daily limit
  -> create canonical unique payload
  -> encode representative blob and calculate payment
  -> check chain, version, key, escrow, and limits
  -> atomically write status=started
  -> fibre.Put(...)
       -> ambiguous error/crash: inconclusive on next startup
  -> atomically write status=put_succeeded
  -> Download(BlobID)
  -> compare exact bytes and hashes
       -> equal: succeeded
       -> unavailable: published_unverified
       -> mismatch: integrity_failure
```

V1 measures total `Put` duration. It does not pretend to expose separate stage
timings that the selected public API does not return reliably.

## Project layout

```text
fibre-canary/
├── cmd/fibre-canary/main.go
├── internal/
│   ├── canary/{preflight,run,payload}.go
│   ├── celestia/client.go
│   ├── config/config.go
│   └── receipt/{receipt,store}.go
├── testdata/config.toml
├── config.example.toml
├── Makefile
├── ARCHITECTURE.md
├── TASKS.md
├── README.md
├── LICENSE
├── go.mod
└── go.sum
```

- `cmd/fibre-canary`: arguments, terminal rendering, and exit codes.
- `internal/config`: strict TOML parsing and validation.
- `internal/canary`: payload, preflight, and straightforward run workflow.
- `internal/celestia`: official Fibre/transaction clients and chain queries.
- `internal/receipt`: atomic receipts, stale-run detection, history, and lock.

This is intentionally not a framework. Use only a small network test seam where
needed; do not build a general ports-and-adapters layer.

## Configuration

```toml
config_version = 1
network = "mocha"
chain_id = "mocha-5"
vantage = "ruby-nodes-prague"

app_grpc_address = "127.0.0.1:9090"
data_dir = "/var/lib/fibre-canary"

key_name = "fibre-canary"
keyring_backend = "file"
keyring_dir = "/var/lib/fibre-canary/keyring"

namespace_hex = "0000000000000000000000000000000000000000000000000000cafe"
payload_bytes = 1024
max_payload_bytes = 4096
max_payment_utia = "10000"
max_runs_per_day = 1
operation_timeout = "2m"
```

Rules:

- `config_version` is mandatory and unknown fields fail.
- Only a chain in the build's compiled-in allow-list is accepted (see
  "Pinned compatibility" in README.md).
- Auto-funding is off and not configurable.
- Mnemonics, keys, passphrases, tokens, and credential-bearing URLs are rejected.
- Monetary values use integers.
- A keyring passphrase is never accepted as a CLI argument.

## Payload

The canonical JSON payload contains schema, run ID, creation time, named
vantage, and a secure random nonce. Serialization is deterministic after fields
are selected, with deterministic padding to the configured size.

The receipt stores the payload hash and nonsecret fields needed to reconstruct
the expected bytes. Arbitrary user files are not supported.

## Preflight

`preflight` performs no signing, funding, upload, or transaction broadcast. It:

1. validates configuration and local permissions;
2. verifies chain identity and pinned compatibility;
3. loads the configured public account identity;
4. queries Fibre escrow;
5. encodes a representative payload and obtains actual upload size;
6. calculates payment using the pinned implementation;
7. enforces payload/payment ceilings and the local daily limit;
8. reports inconclusive attempts; and
9. confirms auto-funding is disabled.

Output names the account, endpoint, version, and vantage to which it applies.

## Receipts and states

Each run has `<data_dir>/runs/<run-id>.json`. Updates write and fsync a temporary
file, rename it atomically, then fsync the runs directory. The directory and
receipts are owner-writable only.

Statuses:

- `started`: `Put` is about to run or may be running.
- `put_succeeded`: `Put` returned BlobID, tx hash, and settlement height.
- `succeeded`: downloaded bytes equal the original payload.
- `published_unverified`: publication succeeded but download did not complete.
- `integrity_failure`: download returned different bytes.
- `inconclusive`: the tool cannot determine whether a prior `Put` was charged.

On startup:

1. acquire `<data_dir>/canary.lock`;
2. convert stale `started` receipts to `inconclusive`;
3. refuse a new paid run while any inconclusive receipt exists;
4. allow override only with `--accept-possible-duplicate-charge`; and
5. reference the acknowledged prior run from the new receipt.

Receipts include version, run ID, status, network, chain, vantage, account,
client version, timestamps, payload/download hashes and sizes, expected payment,
Put/download durations, successful BlobID/tx hash/height, stable error code, and
any acknowledged prior run.

Raw errors may be shown locally but are sanitized before receipt storage. Never
persist key material, passphrases, keyring paths, credential-bearing endpoints,
or signed promises.

## Financial controls

V1 enforces maximum original payload, actual encoded upload size where exposed,
maximum Fibre payment, sufficient Fibre escrow, maximum local runs per day, one
local process, dedicated low-balance account, and auto-funding disabled.

Never hard-code pricing. Use the pinned compatible Celestia calculation and
reject unsupported runtime versions. The daily limit is only local and is not
authoritative if another host uses the account or local history is deleted.

The selected `Put` API does not cleanly expose a caller-controlled transaction
fee ceiling. V1 must not claim one. Bound fee exposure with the small balance in
the dedicated Mocha account.

## Crash semantics

| Crash point | Stored state | Next start |
|---|---|---|
| Before intent | None | Safe to run |
| After intent, before/during `Put` | `started` | Inconclusive; acknowledgement required |
| After `Put`, before result write | `started` | Inconclusive; acknowledgement required |
| After result, before/during download | `put_succeeded` | Published but unverified |
| After terminal write | Terminal | No ambiguity |

This deliberately favors a simple false-positive inconclusive result over unsafe
automatic retry logic.

## Output

V1 produces concise terminal progress and a JSON receipt. It always prints:

```text
This verifies one publisher path from <vantage>.
It is not a global Fibre availability measurement.
```

Atomic OpenMetrics text output is optional only after the usable binary works.

## Tests

Required coverage:

- strict config, version rejection, and redaction;
- canonical payload generation and hashing;
- payment, payload, escrow, and daily-run limits;
- receipt transitions, atomic persistence, and locking;
- stale intent detection and duplicate-charge acknowledgement;
- fake Put success and ambiguous failure;
- download success, failure, and byte mismatch;
- one opt-in live Mocha smoke test.

## Release criteria

- A real Mocha run publishes, settles, downloads, and verifies exact bytes.
- Ambiguous attempts cannot trigger an automatic retry.
- Limits and receipt transitions are tested.
- README documents account setup, escrow, cost limits, crash risk, and scope.
- A reproducible binary and sanitized sample receipt are published.

There is no architecture blocker. Day one must confirm the exact Mocha
chain/app/Fibre version and matching exported payment/client APIs. If live Mocha
differs from the inspected development revision, pin the compatible release;
never bypass version checks or hard-code pricing.

