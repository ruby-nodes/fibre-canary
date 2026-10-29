package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validConfig = `
config_version = 1
network = "mocha"
chain_id = "mocha-5"
vantage = "test-vantage"
app_grpc_address = "127.0.0.1:9090"
data_dir = "/tmp/fibre-canary"
key_name = "fibre-canary"
keyring_backend = "file"
keyring_dir = "/tmp/fibre-canary/keyring"
namespace_hex = "0000000000000000000000000000000000000000000000000000cafe"
payload_bytes = 1024
max_payload_bytes = 4096
max_payment_utia = "10000"
max_runs_per_day = 1
operation_timeout = "2m"
`

func TestLoadValid(t *testing.T) {
	t.Parallel()

	cfg, err := loadText(t, validConfig)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PaymentLimitUTIA() != 10000 {
		t.Fatalf("PaymentLimitUTIA() = %d, want 10000", cfg.PaymentLimitUTIA())
	}
	if cfg.Timeout() != 2*time.Minute {
		t.Fatalf("Timeout() = %s, want 2m", cfg.Timeout())
	}
	if cfg.AutoFund() {
		t.Fatal("AutoFund() = true, want forced false")
	}
}

func TestLoadAcceptsEverySupportedChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		network string
		chainID string
	}{
		{"mocha", "mocha-5"},
		{"mainnet", "celestia"},
	}

	for _, tt := range tests {
		t.Run(tt.network+"/"+tt.chainID, func(t *testing.T) {
			t.Parallel()
			input := strings.Replace(validConfig, `network = "mocha"`, `network = "`+tt.network+`"`, 1)
			input = strings.Replace(input, `chain_id = "mocha-5"`, `chain_id = "`+tt.chainID+`"`, 1)
			if _, err := loadText(t, input); err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
		})
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		old     string
		new     string
		wantErr string
	}{
		{"unknown field", `operation_timeout = "2m"`, "unexpected = 1\noperation_timeout = \"2m\"", "unknown configuration field"},
		{"config version", "config_version = 1", "config_version = 2", "config_version must be 1"},
		{"network/chain mismatch", `network = "mocha"`, `network = "mainnet"`, "unsupported network tuple"},
		{"unsupported chain id", `chain_id = "mocha-5"`, `chain_id = "mocha-4"`, "unsupported network tuple"},
		{"unsupported network entirely", `network = "mocha"` + "\n" + `chain_id = "mocha-5"`, `network = "arabica"` + "\n" + `chain_id = "arabica-11"`, "unsupported network tuple"},
		{"missing key name", `key_name = "fibre-canary"`, `key_name = ""`, "key_name is required"},
		{"missing backend", `keyring_backend = "file"`, `keyring_backend = ""`, "keyring_backend is required"},
		{"root data directory", `data_dir = "/tmp/fibre-canary"`, `data_dir = "/"`, "data_dir must not be the filesystem root"},
		{"relative keyring", `keyring_dir = "/tmp/fibre-canary/keyring"`, `keyring_dir = "relative"`, "keyring_dir must be an absolute path"},
		{"credential endpoint", `app_grpc_address = "127.0.0.1:9090"`, `app_grpc_address = "alice:password@node.example:9090"`, "credential-free host:port"},
		{"payload zero", "payload_bytes = 1024", "payload_bytes = 0", "payload_bytes must be positive"},
		{"payload over configured limit", "payload_bytes = 1024", "payload_bytes = 4097", "payload_bytes exceeds"},
		{"payload hard limit", "max_payload_bytes = 4096", "max_payload_bytes = 1048577", "max_payload_bytes must be between"},
		{"payment decimal", `max_payment_utia = "10000"`, `max_payment_utia = "1.5"`, "positive base-10 integer"},
		{"payment zero", `max_payment_utia = "10000"`, `max_payment_utia = "0"`, "positive base-10 integer"},
		{"daily zero", "max_runs_per_day = 1", "max_runs_per_day = 0", "max_runs_per_day must be between"},
		{"daily excessive", "max_runs_per_day = 1", "max_runs_per_day = 25", "max_runs_per_day must be between"},
		{"timeout zero", `operation_timeout = "2m"`, `operation_timeout = "0s"`, "operation_timeout must be greater"},
		{"timeout excessive", `operation_timeout = "2m"`, `operation_timeout = "31m"`, "operation_timeout must be greater"},
		{"namespace length", `namespace_hex = "0000000000000000000000000000000000000000000000000000cafe"`, `namespace_hex = "00"`, "exactly 28 bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := strings.Replace(validConfig, tt.old, tt.new, 1)
			_, err := loadText(t, input)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateNamespaceMatchesVersionZeroBlobContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{
			name:    "nonzero byte 10 in required prefix",
			value:   strings.Repeat("00", 10) + "01" + strings.Repeat("00", 16) + "01",
			wantErr: "version-0 user namespace ID",
		},
		{
			name:    "nonzero byte 17 in required prefix",
			value:   strings.Repeat("00", 17) + "01" + strings.Repeat("00", 9) + "01",
			wantErr: "version-0 user namespace ID",
		},
		{
			name:  "nonzero byte 18 starts user ID",
			value: strings.Repeat("00", 18) + "01" + strings.Repeat("00", 9),
		},
		{
			name:    "zero namespace is reserved",
			value:   strings.Repeat("00", 28),
			wantErr: "reserved namespace",
		},
		{
			name:    "highest primary reserved namespace",
			value:   strings.Repeat("00", 27) + "ff",
			wantErr: "reserved namespace",
		},
		{
			name:  "first namespace above primary reserved range",
			value: strings.Repeat("00", 26) + "0100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateNamespace(tt.value)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("validateNamespace() error = %v, want nil", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("validateNamespace() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadRejectsForbiddenFieldsWithoutLeakingValue(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"auto_fund", "mnemonic", "private_key", "passphrase", "token"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			const secret = "do-not-leak-this-value"
			_, err := loadText(t, validConfig+field+` = "`+secret+`"`+"\n")
			if err == nil || !strings.Contains(err.Error(), "forbidden configuration field") {
				t.Fatalf("Load() error = %v, want forbidden field", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("Load() leaked secret in error: %v", err)
			}
		})
	}
}

func loadText(t *testing.T, text string) (Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}
