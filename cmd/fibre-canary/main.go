package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"example.com/fibre-canary/internal/buildinfo"
	"example.com/fibre-canary/internal/canary"
	celestiaclient "example.com/fibre-canary/internal/celestia"
	"example.com/fibre-canary/internal/config"
	"example.com/fibre-canary/internal/receipt"
	"example.com/fibre-canary/internal/redact"
)

const scopeNotice = "This verifies one publisher path from %s.\nIt is not a global Fibre availability measurement.\n"

const (
	exitSuccess          = 0
	exitOperationalError = 1
	exitUsageError       = 2
	exitIntegrityFailure = 3
)

// defaultConfigPath is the --config value used when a command omits the flag.
// It is relative to the current working directory, not the binary location.
const defaultConfigPath = "config.toml"

type commandNetwork interface {
	canary.Network
	Close(context.Context) error
}

var newNetwork = func(ctx context.Context, cfg config.Config, input io.Reader) (commandNetwork, error) {
	return celestiaclient.New(ctx, cfg, input)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRootHelp(stderr)
		return exitUsageError
	}

	switch args[0] {
	case "help", "-h", "--help":
		printRootHelp(stdout)
		return exitSuccess
	case "version", "--version":
		fmt.Fprintf(stdout, "fibre-canary (M4)\nCelestia app: %s (%s)\nSupported chains: %s\n", buildinfo.CelestiaAppVersion, buildinfo.CelestiaAppCommit, buildinfo.SupportedChainsText())
		return exitSuccess
	case "preflight":
		return runCommand("preflight", args[1:], stdout, stderr)
	case "run":
		return runCommand("run", args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printRootHelp(stderr)
		return exitUsageError
	}
}

func runCommand(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", defaultConfigPath, "path to the versioned TOML configuration")
	var acceptDuplicate bool
	if command == "run" {
		flags.BoolVar(&acceptDuplicate, "accept-possible-duplicate-charge", false, "acknowledge possible duplicate charge after an inconclusive run")
	}
	flags.Usage = func() { printCommandHelp(flags.Output(), command, flags) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return exitUsageError
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s does not accept positional arguments\n", command)
		return exitUsageError
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration invalid: %s\n", redact.String(err.Error()))
		return exitUsageError
	}
	if err := canary.ValidatePayloadSize(cfg.PayloadBytes, time.Now(), cfg.Vantage); err != nil {
		fmt.Fprintf(stderr, "configuration invalid: %s\n", redact.String(err.Error()))
		return exitUsageError
	}

	fmt.Fprintf(stdout, scopeNotice, cfg.Vantage)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout())
	defer cancel()

	if command == "preflight" || command == "run" {
		recovered, unresolved, err := prepareLocalRun(cfg, command, acceptDuplicate, time.Now())
		if recovered > 0 {
			fmt.Fprintf(stdout, "Classified %d interrupted run(s) from durable receipts.\n", recovered)
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s refused: %s\n", command, redact.String(err.Error()))
			return exitOperationalError
		}
		if command == "preflight" && unresolved > 0 {
			fmt.Fprintf(stdout, "%d unresolved inconclusive run(s) on record; `run` requires --accept-possible-duplicate-charge until acknowledged.\n", unresolved)
		}
	}
	network, err := newNetwork(ctx, cfg, os.Stdin)
	if err != nil {
		fmt.Fprintf(stderr, "%s failed: %s\n", command, redact.String(err.Error()))
		return exitOperationalError
	}
	defer network.Close(context.Background())

	if command == "preflight" {
		report, err := canary.Preflight(ctx, network, cfg.PayloadBytes, canary.Limits{MaxPayloadBytes: cfg.MaxPayload, MaxPaymentUTIA: cfg.PaymentLimitUTIA()}, cfg.Vantage)
		if err != nil {
			fmt.Fprintf(stderr, "preflight failed: %s\n", redact.String(err.Error()))
			return exitOperationalError
		}
		fmt.Fprintf(stdout, "Account: %s\nEndpoint: %s\nChain: %s\nClient version: %s\nPayload: %d bytes (encoded %d; maximum %d)\nPayment: %d utia (maximum %d)\nAvailable Fibre escrow: %d utia\nVantage: %s\n", report.Account, report.Endpoint, report.ChainID, report.ClientVersion, report.PayloadBytes, report.EncodedBytes, report.MaxPayloadBytes, report.PaymentUTIA, report.MaxPaymentUTIA, report.AvailableEscrowUTIA, report.Vantage)
		fmt.Fprintln(stdout, "Auto-funding: disabled (not configurable).")
		return exitSuccess
	}
	if command == "run" {
		r, err := canary.Run(ctx, canary.RunOptions{DataDir: cfg.DataDir, Network: cfg.Network, ChainID: cfg.ChainID, Vantage: cfg.Vantage, MaxRunsPerDay: cfg.MaxRunsPerDay, PayloadBytes: cfg.PayloadBytes, MaxPayloadBytes: cfg.MaxPayload, MaxPaymentUTIA: cfg.PaymentLimitUTIA(), AcceptPossibleDuplicateCharge: acceptDuplicate, NetworkClient: network})
		if err != nil {
			if r.Status == receipt.StatusPublishedUnverified || r.Status == receipt.StatusIntegrityFailure {
				fmt.Fprintf(stderr, "Run %s: status=%s error_code=%s: %s\n", r.RunID, r.Status, r.ErrorCode, redact.String(err.Error()))
			} else {
				fmt.Fprintf(stderr, "run failed: %s\n", redact.String(err.Error()))
			}
			if r.Status == receipt.StatusIntegrityFailure {
				return exitIntegrityFailure
			}
			return exitOperationalError
		}
		fmt.Fprintf(stdout, "Run succeeded: status=%s run=%s blob_id=%s tx_hash=%s height=%d put_duration_ms=%d download_duration_ms=%d\n", r.Status, r.RunID, r.BlobID, r.TxHash, r.SettlementHeight, r.PutDurationMillis, r.DownloadDurationMillis)
		return exitSuccess
	}
	return exitUsageError
}

// prepareLocalRun classifies interrupted receipts under the data-directory
// lock for both commands, but only enforces the paid-run admission gate
// (inconclusive-run and daily-limit refusal) for "run". "preflight" is
// read-only: it reports unresolvedInconclusive informationally instead of
// refusing, so it stays usable for diagnosis even while an inconclusive
// run awaits acknowledgement.
func prepareLocalRun(cfg config.Config, command string, accept bool, now time.Time) (recoveredCount, unresolvedInconclusive int, err error) {
	store, err := receipt.Open(cfg.DataDir)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare receipt store: %w", err)
	}
	lock, err := store.Lock()
	if err != nil {
		return 0, 0, err
	}
	defer lock.Close()

	recovered, err := store.RecoverInterrupted(now)
	if err != nil {
		return 0, 0, err
	}
	receipts, err := store.List()
	if err != nil {
		return len(recovered), 0, err
	}
	unresolvedInconclusive = len(receipt.Inconclusive(receipts))
	if command != "run" {
		return len(recovered), unresolvedInconclusive, nil
	}
	if _, err := receipt.CheckAdmission(receipts, now, cfg.MaxRunsPerDay, accept); err != nil {
		return len(recovered), unresolvedInconclusive, err
	}
	return len(recovered), unresolvedInconclusive, nil
}

func printRootHelp(w io.Writer) {
	fmt.Fprintf(w, `fibre-canary checks one publisher path through Celestia Fibre: it publishes a
small canonical payload, downloads it back by its publication height, and
verifies the exact bytes and SHA-256 hash. It does not prove global Fibre
availability or validator-wide behavior.

This build only accepts these chains: %s (Celestia app %s). It refuses to
run against any other network.

Usage:
  fibre-canary preflight [--config PATH]
  fibre-canary run [--config PATH] [--accept-possible-duplicate-charge]
  fibre-canary version
  fibre-canary help

Commands:
  preflight  Verify identity, escrow, pricing, compatibility, and safety limits
  run        Publish, settle, download, and verify one bounded canonical Fibre payload
  version    Print the pinned network/chain/Celestia-app compatibility tuple
  help       Print this message

Flags (preflight, run):
  --config PATH   Path to the versioned TOML configuration.
                  Default: %q, resolved relative to the current directory.

Exit codes: 0 success, 1 operational/unverified, 2 usage/configuration, 3 integrity failure.

No command accepts mnemonics, private keys, passphrases, tokens, or keyring passwords.
`, buildinfo.SupportedChainsText(), buildinfo.CelestiaAppVersion, defaultConfigPath)
}

func printCommandHelp(w io.Writer, command string, flags *flag.FlagSet) {
	fmt.Fprintf(w, "Usage: fibre-canary %s [options]\n\n%s\n\nOptions:\n", command, commandDescription(command))
	flags.PrintDefaults()
	fmt.Fprintln(w, "\nConfiguration is validated before any future network or keyring operation.")
}

func commandDescription(command string) string {
	switch command {
	case "preflight":
		return "Read-only check: verifies account identity, Fibre escrow, calculated\npayment, pinned-version compatibility, and configured safety limits.\nIt does not publish, pay, or write any receipt."
	case "run":
		return "Publishes one bounded canonical payload with official fibre.Put,\ndownloads it back, and verifies the exact bytes and SHA-256 hash.\nThis spends real funds on the configured network."
	default:
		return ""
	}
}
