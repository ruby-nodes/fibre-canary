package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ruby-nodes/fibre-canary/internal/canary"
	"github.com/ruby-nodes/fibre-canary/internal/config"
	"github.com/ruby-nodes/fibre-canary/internal/receipt"
)

func TestHelpIsUseful(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--help) code = %d", code)
	}
	for _, want := range []string{"preflight", "run", "No command accepts mnemonics"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestPreflightStopsAfterLocalValidation(t *testing.T) {
	configPath := filepath.Join("..", "..", "testdata", "config.toml")
	installFakeNetwork(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"preflight", "--config", configPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(preflight) code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Available Fibre escrow") {
		t.Fatalf("preflight output missing escrow report:\n%s", stdout.String())
	}
}

func TestPreflightRemainsUsableAfterInconclusiveRun(t *testing.T) {
	installFakeNetwork(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeRunConfig(t, dataDir, 1)
	store, err := receipt.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	abandoned := receipt.Receipt{
		Version:       receipt.Version,
		RunID:         "abandoned-preflight-run",
		Status:        receipt.StatusStarted,
		Network:       "mocha",
		ChainID:       "mocha-5",
		Vantage:       "test-vantage",
		ClientVersion: "test-version",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.Create(abandoned); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"preflight", "--config", configPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("preflight after inconclusive run code = %d, stderr = %s", code, stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("preflight after inconclusive run wrote stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "unresolved inconclusive run") {
		t.Fatalf("preflight output missing unresolved inconclusive notice:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Available Fibre escrow") {
		t.Fatalf("preflight output missing escrow report:\n%s", stdout.String())
	}
}

func TestPreflightRejectsUndersizedCanonicalPayloadBeforeNetwork(t *testing.T) {
	configPath := writeRunConfig(t, filepath.Join(t.TempDir(), "data"), 1)
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents = bytes.Replace(contents, []byte("payload_bytes = 1024"), []byte("payload_bytes = 1"), 1)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	networkCalled := false
	previous := newNetwork
	newNetwork = func(context.Context, config.Config, io.Reader) (commandNetwork, error) {
		networkCalled = true
		return &cliFakeNetwork{}, nil
	}
	t.Cleanup(func() { newNetwork = previous })

	var stdout, stderr bytes.Buffer
	if code := run([]string{"preflight", "--config", configPath}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(preflight) code = %d, stderr = %s", code, stderr.String())
	}
	if networkCalled {
		t.Fatal("network client constructed for an undersized canonical payload")
	}
	if !strings.Contains(stderr.String(), "too small for canonical metadata") {
		t.Fatalf("preflight error = %q", stderr.String())
	}
}

func TestCLIRejectsCredentialFlagWithoutEchoingValue(t *testing.T) {
	t.Parallel()

	const secret = "do-not-echo-this"
	var stdout, stderr bytes.Buffer
	if code := run([]string{"preflight", "--mnemonic", secret}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(secret flag) code = %d, want 2", code)
	}
	if strings.Contains(stderr.String(), secret) {
		t.Fatalf("secret flag value leaked in stderr: %s", stderr.String())
	}
}

func TestRunRecoversAndRequiresExplicitAcknowledgement(t *testing.T) {
	installFakeNetwork(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeRunConfig(t, dataDir, 2)
	store, err := receipt.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	abandoned := receipt.Receipt{
		Version:       receipt.Version,
		RunID:         "abandoned-cli-run",
		Status:        receipt.StatusStarted,
		Network:       "mocha",
		ChainID:       "mocha-5",
		Vantage:       "test-vantage",
		ClientVersion: "test-version",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.Create(abandoned); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"run", "--config", configPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("run without acknowledgement code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "inconclusive prior run") {
		t.Fatalf("run refusal = %q", stderr.String())
	}
	recovered, err := store.Read(abandoned.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != receipt.StatusInconclusive {
		t.Fatalf("recovered status = %q", recovered.Status)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"run", "--config", configPath, "--accept-possible-duplicate-charge"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run with acknowledgement code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "status=succeeded") {
		t.Fatalf("run output = %q", stdout.String())
	}
}

type cliFakeNetwork struct {
	payload []byte
}

func (*cliFakeNetwork) Preflight(context.Context, int) (canary.NetworkInfo, error) {
	return canary.NetworkInfo{Account: "celestia1test", Endpoint: "127.0.0.1:9090", ChainID: "mocha-5", ClientVersion: "v10.4.0-mocha", EncodedBytes: 4096, PaymentUTIA: 10, AvailableEscrowUTIA: 100}, nil
}
func (f *cliFakeNetwork) Put(_ context.Context, payload []byte) (canary.PutResult, error) {
	f.payload = append([]byte(nil), payload...)
	return canary.PutResult{BlobID: "blob", TxHash: "tx", Height: 10}, nil
}
func (f *cliFakeNetwork) Download(context.Context, string, uint64) ([]byte, error) {
	return append([]byte(nil), f.payload...), nil
}
func (*cliFakeNetwork) Close(context.Context) error { return nil }

func installFakeNetwork(t *testing.T) {
	t.Helper()
	previous := newNetwork
	newNetwork = func(context.Context, config.Config, io.Reader) (commandNetwork, error) {
		return &cliFakeNetwork{}, nil
	}
	t.Cleanup(func() { newNetwork = previous })
}

func writeRunConfig(t *testing.T, dataDir string, maxRuns int) string {
	t.Helper()
	contents := `config_version = 1
network = "mocha"
chain_id = "mocha-5"
vantage = "test-vantage"
app_grpc_address = "127.0.0.1:9090"
data_dir = "` + dataDir + `"
key_name = "fibre-canary-test"
keyring_backend = "file"
keyring_dir = "` + filepath.Join(dataDir, "keyring") + `"
namespace_hex = "0000000000000000000000000000000000000000000000000000cafe"
payload_bytes = 1024
max_payload_bytes = 4096
max_payment_utia = "10000"
max_runs_per_day = ` + strconv.Itoa(maxRuns) + `
operation_timeout = "2m"
`
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
