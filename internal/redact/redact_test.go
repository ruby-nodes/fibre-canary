package redact

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		secret string
	}{
		{"mnemonic", `mnemonic = "glory actual seed words"`, "glory actual seed words"},
		{"private key", `private_key=deadbeef`, "deadbeef"},
		{"passphrase", `passphrase: hunter2`, "hunter2"},
		{"token", `token='abc123'`, "abc123"},
		{"URL user info", `rpc=https://alice:password@example.test:443`, "alice:password"},
		{"keyring directory", `keyring_dir = "/secret/location"`, "/secret/location"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := String(tt.input)
			if strings.Contains(got, tt.secret) {
				t.Fatalf("String() leaked %q in %q", tt.secret, got)
			}
			if !strings.Contains(got, replacement) {
				t.Fatalf("String() = %q, want redaction marker", got)
			}
		})
	}
}
