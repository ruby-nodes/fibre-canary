// Package config loads and validates fibre-canary's fail-closed configuration.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/ruby-nodes/fibre-canary/internal/buildinfo"
	"github.com/ruby-nodes/fibre-canary/internal/redact"
)

const (
	maxPayloadHardLimit = 1 << 20
	maxOperationTimeout = 30 * time.Minute
	maxDailyRuns        = 24
)

// Config is the complete v1 configuration. Auto-funding and credentials are
// intentionally absent, so they cannot be enabled or supplied by configuration.
type Config struct {
	ConfigVersion int    `toml:"config_version"`
	Network       string `toml:"network"`
	ChainID       string `toml:"chain_id"`
	Vantage       string `toml:"vantage"`

	AppGRPCAddress string `toml:"app_grpc_address"`
	DataDir        string `toml:"data_dir"`

	KeyName        string `toml:"key_name"`
	KeyringBackend string `toml:"keyring_backend"`
	KeyringDir     string `toml:"keyring_dir"`

	NamespaceHex     string `toml:"namespace_hex"`
	PayloadBytes     int    `toml:"payload_bytes"`
	MaxPayload       int    `toml:"max_payload_bytes"`
	MaxPaymentUTIA   string `toml:"max_payment_utia"`
	MaxRunsPerDay    int    `toml:"max_runs_per_day"`
	OperationTimeout string `toml:"operation_timeout"`

	paymentLimit uint64
	timeout      time.Duration
}

var forbiddenFields = map[string]struct{}{
	"auto_fund": {}, "auto_funding": {}, "escrow_auto_fund": {},
	"mnemonic": {}, "private_key": {}, "key": {}, "passphrase": {},
	"password": {}, "token": {}, "api_key": {}, "secret": {},
}

// Load reads path, rejects unrecognized or forbidden keys, and validates every
// local safety limit before any network or keyring operation can occur.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var cfg Config
	metadata, err := toml.NewDecoder(f).Decode(&cfg)
	if err != nil {
		return Config{}, fmt.Errorf("parse config: %s", redact.String(err.Error()))
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		key := undecoded[0].String()
		if _, forbidden := forbiddenFields[strings.ToLower(key)]; forbidden {
			return Config{}, fmt.Errorf("forbidden configuration field %q", key)
		}
		return Config{}, fmt.Errorf("unknown configuration field %q", key)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// PaymentLimitUTIA returns the validated integer payment ceiling.
func (c Config) PaymentLimitUTIA() uint64 { return c.paymentLimit }

// Timeout returns the validated operation timeout.
func (c Config) Timeout() time.Duration { return c.timeout }

// AutoFund is deliberately constant and cannot be changed by configuration.
func (Config) AutoFund() bool { return false }

func (c *Config) validate() error {
	if c.ConfigVersion != buildinfo.ConfigVersion {
		return fmt.Errorf("config_version must be %d", buildinfo.ConfigVersion)
	}
	if !buildinfo.SupportsChain(c.Network, c.ChainID) {
		return fmt.Errorf("unsupported network tuple %q/%q; this build supports only %s", c.Network, c.ChainID, buildinfo.SupportedChainsText())
	}
	if err := requiredSafeText("vantage", c.Vantage); err != nil {
		return err
	}
	if err := validateEndpoint(c.AppGRPCAddress); err != nil {
		return err
	}
	if !filepath.IsAbs(c.DataDir) {
		return errors.New("data_dir must be an absolute path")
	}
	if filepath.Clean(c.DataDir) == string(filepath.Separator) {
		return errors.New("data_dir must not be the filesystem root")
	}
	if err := requiredSafeText("key_name", c.KeyName); err != nil {
		return err
	}
	if err := requiredSafeText("keyring_backend", c.KeyringBackend); err != nil {
		return err
	}
	if !filepath.IsAbs(c.KeyringDir) {
		return errors.New("keyring_dir must be an absolute path")
	}
	if filepath.Clean(c.KeyringDir) == string(filepath.Separator) {
		return errors.New("keyring_dir must not be the filesystem root")
	}
	if err := validateNamespace(c.NamespaceHex); err != nil {
		return err
	}
	if c.PayloadBytes <= 0 {
		return errors.New("payload_bytes must be positive")
	}
	if c.MaxPayload <= 0 || c.MaxPayload > maxPayloadHardLimit {
		return fmt.Errorf("max_payload_bytes must be between 1 and %d", maxPayloadHardLimit)
	}
	if c.PayloadBytes > c.MaxPayload {
		return errors.New("payload_bytes exceeds max_payload_bytes")
	}
	payment, err := strconv.ParseUint(c.MaxPaymentUTIA, 10, 64)
	if err != nil || payment == 0 {
		return errors.New("max_payment_utia must be a positive base-10 integer string")
	}
	c.paymentLimit = payment
	if c.MaxRunsPerDay < 1 || c.MaxRunsPerDay > maxDailyRuns {
		return fmt.Errorf("max_runs_per_day must be between 1 and %d", maxDailyRuns)
	}
	timeout, err := time.ParseDuration(c.OperationTimeout)
	if err != nil || timeout <= 0 || timeout > maxOperationTimeout {
		return fmt.Errorf("operation_timeout must be greater than zero and no more than %s", maxOperationTimeout)
	}
	c.timeout = timeout
	return nil
}

func requiredSafeText(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s contains forbidden control characters", name)
	}
	return nil
}

func validateEndpoint(address string) error {
	if strings.Contains(address, "://") || strings.ContainsAny(address, "@/?#\r\n") {
		return errors.New("app_grpc_address must be a credential-free host:port")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("app_grpc_address must be a credential-free host:port")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return errors.New("app_grpc_address must contain a valid TCP port")
	}
	return nil
}

func validateNamespace(value string) error {
	b, err := hex.DecodeString(value)
	if err != nil || len(b) != 28 {
		return errors.New("namespace_hex must encode exactly 28 bytes")
	}
	// A v0 namespace ID has an 18-byte zero prefix followed by a 10-byte
	// user-specified ID. The Celestia blob constructor adds the separate
	// namespace-version byte when M3 integrates it.
	for _, prefixByte := range b[:18] {
		if prefixByte != 0 {
			return errors.New("namespace_hex must be a version-0 user namespace ID")
		}
	}
	// Version-0 IDs up to 0xff are reserved for the Celestia state machine.
	// With the required zero prefix, that is every ID whose first nine
	// user-specified bytes are also zero.
	allReservedPrefix := true
	for _, idByte := range b[18:27] {
		if idByte != 0 {
			allReservedPrefix = false
			break
		}
	}
	if allReservedPrefix {
		return errors.New("namespace_hex must not be a reserved namespace")
	}
	return nil
}
