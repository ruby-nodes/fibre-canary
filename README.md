# Fibre Canary

## The problem

Celestia's [Fibre](https://docs.celestia.org/build/post-retrieve-blob/fibre/)
feature lets you store and fetch much bigger blobs (up to 128 MiB) than a
regular `PayForBlob` transaction allows. Your client uploads shards straight
to validators' Fibre servers, and settles a payment promise once enough
validators (two-thirds of voting power) have signed it.

Celestia's own docs call out the catch:
["network activation alone does not establish that enough Fibre servers are
ready to accept uploads"](https://docs.celestia.org/build/post-retrieve-blob/fibre/when-to-use/).
In other words, the chain can be fully live and upgraded while Fibre itself is
quietly broken for your account, your endpoint, or your location. Normally you
only find that out when a real upload fails.

`fibre-canary` checks for exactly that, end to end: it deposits escrow,
publishes one real Fibre blob, settles it, downloads it back, and confirms the
bytes match. Run it by hand after setting up a new publisher, drop it into an
upgrade pipeline so an app-version bump can't silently break Fibre, or just
run it again whenever you change Fibre-related configuration to make sure
it's still working.

It's a small, standalone Go CLI, not a dashboard or a service that watches the
whole network. See [Scope and receipts](#scope-and-receipts) for exactly what
one run does and doesn't prove.

## Supported networks

This build works with two chains, and nothing else:

- `mocha` / `mocha-5` — Celestia's public testnet
- `mainnet` / `celestia` — real TIA, real money

Any other network is rejected before any network or keyring work happens.
That's deliberate: it means this tool can never end up quietly talking to a
chain nobody has checked it against.

Celestia doesn't ship a new `celestia-app` version every week, so you won't
need to update `fibre-canary` every week either. A new version is only
needed when Mocha and Mainnet themselves move to a newer `celestia-app`
release — watch this project's releases for that.

## Get fibre-canary

The simplest option: grab the latest release binary and its checksum for your
platform (`linux-amd64` or `linux-arm64`) from this project's Releases page,
verify it, and you're done — no Go toolchain needed.

```sh
# Run this from the folder you downloaded both files into.
sha256sum -c fibre-canary-linux-amd64.sha256
```

If you'd rather build it yourself (to inspect the code first, or because
there's no release for your platform), see [Build and run](#build-and-run)
below — all you need is Go 1.26.6, or a Go that can fetch it as a toolchain.

## Set up an account

1. **`celestia-appd`**: if this machine already runs a Celestia validator or
   Fibre server, you already have it. Otherwise, install the pinned version
   (see [Supported networks](#supported-networks)).
2. **An endpoint**: a trusted Celestia app gRPC address, `host:port`, no
   credentials in the URL.
3. **A dedicated key**, so this canary never shares a wallet with anything
   else:

   ```sh
   install -d -m 700 /var/lib/fibre-canary/keyring
   celestia-appd keys add fibre-canary \
     --keyring-backend file \
     --keyring-dir /var/lib/fibre-canary/keyring
   celestia-appd keys show fibre-canary --address \
     --keyring-backend file \
     --keyring-dir /var/lib/fibre-canary/keyring
   ```

   Save the mnemonic the way you normally handle secrets. Never put it in
   this repository or in `config.toml`.
4. **Some TIA for that key**:
   - On Mocha, grab testnet TIA from the faucet in
     [Celestia's Discord](https://discord.com/invite/YsnTPcSfWQ).
   - On Mainnet, it's real TIA — fund the key the way you normally would.

   Then deposit into Fibre escrow:

   ```sh
   celestia-appd tx fibre deposit-to-escrow 1000000utia \
     --from fibre-canary \
     --keyring-backend file \
     --keyring-dir /var/lib/fibre-canary/keyring \
     --chain-id mocha-5 \
     --node http://127.0.0.1:26657 \
     --gas auto --gas-adjustment 1.3 --gas-prices 0.004utia
   ```

   The `--gas-prices` value must meet or exceed the target node's configured
   `minimum-gas-prices` (0.004utia is the chain default); `--gas auto`
   estimates the gas needed instead of relying on the CLI's low default gas
   limit, which otherwise produces an "insufficient fee" or "out of gas"
   error.

   Fibre charges a protocol-fixed minimum of ~695000 utia per blob regardless
   of payload size, so the deposit must clear that floor even though
   `fibre-canary` publishes tiny payloads; `preflight` will tell you if the
   escrow ever runs low.

Never put a mnemonic, private key, passphrase, token, or credential-bearing
endpoint into `config.toml` or on the command line. Keyring unlock input is
always interactive.

## Build and run

```sh
make build
cp config.example.toml config.toml
```

Edit `config.toml` for your setup. Here it is in full, with comments:

```toml
# fibre-canary configuration schema v1. Copy this file to config.toml.
# Never put a mnemonic, private key, passphrase, token, or endpoint credentials here.
config_version = 1
# This build also accepts network = "mainnet", chain_id = "celestia". On
# mainnet max_payment_utia and max_runs_per_day bound real TIA, not testnet
# funds, so size them deliberately.
network = "mocha"
chain_id = "mocha-5"
# A short label for where this canary runs from, e.g. a city or node name.
# It's only used in output and receipts, not for anything functional.
vantage = "ruby-nodes-prague"

# A trusted Celestia app gRPC endpoint: host:port only, no scheme, no credentials.
app_grpc_address = "127.0.0.1:9090"
# Keep this directory on persistent local storage; receipts enforce crash safety.
data_dir = "/var/lib/fibre-canary"

# Create this key in the selected Cosmos SDK keyring before running preflight.
key_name = "fibre-canary"
keyring_backend = "file"
keyring_dir = "/var/lib/fibre-canary/keyring"

# A 28-byte (56 hex characters) namespace reserved for this canary's test
# payloads. The default below is fine to reuse as-is.
namespace_hex = "0000000000000000000000000000000000000000000000000000cafe"
# Size of the payload this canary publishes and verifies, in bytes.
payload_bytes = 1024
# Safety ceiling for payload_bytes. The tool itself also caps this at 1 MiB.
max_payload_bytes = 4096
# This bounds Fibre payment only. It is not a transaction-fee ceiling.
# Fibre charges a protocol-fixed minimum of 695000 utia per blob (650000 fixed
# + 45000 per 256 KiB chunk, at least one chunk) regardless of payload size,
# so this must stay comfortably above that floor or every run will fail here.
max_payment_utia = "750000"
# Local safety limit: refuses more than this many paid runs per UTC day.
max_runs_per_day = 1
# Maximum time allowed for one preflight or run call.
operation_timeout = "2m"
```

Then:

```sh
./fibre-canary preflight --config config.toml
./fibre-canary run --config config.toml
```

`preflight` is read-only: it checks your account, escrow, pricing, and limits,
and spends nothing. Always run it first, and don't run `run` until it's happy.

`run` does the real thing: publishes one small payload, downloads it back, and
tells you whether the bytes matched.

A few things worth knowing before you run it for real:

- Exit codes are stable, so scripts and pipelines can rely on them: `0`
  success, `1` operational failure, `2` bad configuration, `3` integrity
  failure (the bytes didn't match).
- If `run` gets interrupted at the worst possible moment, it marks that
  attempt `inconclusive` instead of guessing, and won't try again until you
  pass `--accept-possible-duplicate-charge`. It never retries on its own.
- The CLI never takes a mnemonic, private key, passphrase, or token as a
  flag — only a key name and keyring location. Everything else happens
  inside the keyring.

For the full technical detail — every receipt status, the exact
crash-recovery rules, and so on — see [ARCHITECTURE.md](./ARCHITECTURE.md).

## Scope and receipts

This is one observation from one configured publisher vantage. A successful run
does not prove global Fibre availability, validator-wide behavior, or
availability from other networks or locations.

Receipts are owner-only JSON files under `<data_dir>/runs`. They omit endpoints,
keyring paths, credentials, raw errors, validator signatures, and signed
protocol material. See `testdata/example-receipt.json` for a sanitized example.

## Development

Run `make help` to list every target. The common ones:

```sh
make build        # compile ./fibre-canary
make test         # run the unit test suite
make vet          # go vet ./...
make fmt          # gofmt -l -w .
make fmt-check    # fail if any file needs gofmt
make check        # fmt-check + vet + test, the pre-commit gate
make tidy         # go mod tidy
make clean        # remove ./fibre-canary and dist/
```

None of these targets touch the network, a keyring, or a config.toml; the
unit test suite uses only in-process fakes.

## Reproducible Linux release

```sh
make release          # both linux/amd64 and linux/arm64
sha256sum -c dist/fibre-canary-linux-amd64.sha256
sha256sum -c dist/fibre-canary-linux-arm64.sha256
```

Each target uses a fixed target platform, disables CGO and VCS stamping,
trims host paths, clears the Go build ID, and writes a SHA-256 checksum. Run
`go version -m dist/fibre-canary-linux-amd64` to inspect pinned modules.

## Release process

A new version is a new git tag, not just a merged change. Cut one whenever a
change should reach people running the binary:

- **Mocha or Mainnet moves to a newer `celestia-app`.** Bump the `celestia-app`
  dependency in `go.mod` and the pinned `CelestiaAppVersion`/`CelestiaAppCommit`
  (and `SupportedChains` if a chain ID changed) in
  [`internal/buildinfo/compat.go`](./internal/buildinfo/compat.go), confirm
  `make check` still passes, then tag. This is the case to treat as urgent:
  until it's released, operators are silently running a binary pinned to a
  stale `celestia-app`.
- **A bug or security fix** in fibre-canary's own logic.
- **A deliberate feature change** (new config field, command, or exit code).

Pure refactors, internal test changes, or docs-only edits don't need a tag of
their own; let them accumulate until the next real release.

Tags follow `vX.Y.Z`: bump **minor** for a `celestia-app` pin bump or feature,
**patch** for a bug fix, **major** for a breaking config/CLI change.

To publish, push a tag:

```sh
git tag v0.1.0
git push origin v0.1.0
```

[`.github/workflows/release.yml`](./.github/workflows/release.yml) picks up
any `v*` tag push, runs `make check`, builds and checksums every platform with
`make release`, and publishes a GitHub Release named after the tag with those
files attached via `gh release create` — using the workflow's own
`GITHUB_TOKEN`, so no manual build, upload, or extra credential is needed.

## Opt-in live Mocha smoke test

The smoke test spends testnet funds and is skipped by default:

```sh
FIBRE_CANARY_LIVE_MOCHA=1 \
FIBRE_CANARY_LIVE_CONFIG=/absolute/path/to/config.toml \
make test-live
```

Use a config that has already passed `preflight`. The test succeeds only when
the paid run settles, downloads, and verifies identical bytes.

Licensed under Apache-2.0.
