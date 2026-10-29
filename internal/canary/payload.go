package canary

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type payloadDocument struct {
	Schema    string `json:"schema"`
	RunID     string `json:"run_id"`
	CreatedAt string `json:"created_at"`
	Vantage   string `json:"vantage"`
	Nonce     string `json:"nonce"`
	Padding   string `json:"padding"`
}

// NewPayload returns a canonical JSON payload of exactly size bytes. All
// variable fields are selected before encoding; padding is deterministic.
func NewPayload(size int, runID string, createdAt time.Time, vantage string) ([]byte, string, error) {
	return newPayload(size, runID, createdAt, vantage, rand.Reader)
}

// ValidatePayloadSize verifies that size can hold the canonical metadata used
// by a production run without consuming randomness.
func ValidatePayloadSize(size int, createdAt time.Time, vantage string) error {
	runID := formatRunID(createdAt, "0000000000000000")
	_, _, err := newPayload(size, runID, createdAt, vantage, bytes.NewReader(make([]byte, 16)))
	return err
}

func newPayload(size int, runID string, createdAt time.Time, vantage string, random io.Reader) ([]byte, string, error) {
	var nonce [16]byte
	if _, err := io.ReadFull(random, nonce[:]); err != nil {
		return nil, "", fmt.Errorf("generate payload nonce: %w", err)
	}
	doc := payloadDocument{
		Schema:    "fibre-canary-payload-v1",
		RunID:     runID,
		CreatedAt: createdAt.UTC().Format(time.RFC3339Nano),
		Vantage:   vantage,
		Nonce:     hex.EncodeToString(nonce[:]),
	}
	base, err := json.Marshal(doc)
	if err != nil {
		return nil, "", fmt.Errorf("encode payload: %w", err)
	}
	if len(base) > size {
		return nil, "", fmt.Errorf("payload_bytes %d is too small for canonical metadata (%d bytes)", size, len(base))
	}
	doc.Padding = strings.Repeat("0", size-len(base))
	payload, err := json.Marshal(doc)
	if err != nil {
		return nil, "", fmt.Errorf("encode padded payload: %w", err)
	}
	if len(payload) != size {
		return nil, "", fmt.Errorf("internal payload size mismatch: got %d, want %d", len(payload), size)
	}
	sum := sha256.Sum256(payload)
	return payload, hex.EncodeToString(sum[:]), nil
}
