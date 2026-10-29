// Package receipt persists the local safety history for paid canary attempts.
package receipt

import (
	"errors"
	"fmt"
	"time"
)

const Version = 1

type Status string

const (
	StatusStarted             Status = "started"
	StatusPutSucceeded        Status = "put_succeeded"
	StatusSucceeded           Status = "succeeded"
	StatusPublishedUnverified Status = "published_unverified"
	StatusIntegrityFailure    Status = "integrity_failure"
	StatusInconclusive        Status = "inconclusive"
)

var (
	ErrLocked            = errors.New("another fibre-canary process holds the data-directory lock")
	ErrInconclusive      = errors.New("an inconclusive prior run requires explicit duplicate-charge acknowledgement")
	ErrDailyLimit        = errors.New("local maximum runs per day reached")
	ErrInvalidTransition = errors.New("invalid receipt status transition")
)

// Receipt deliberately contains no endpoint, keyring path, credentials, raw
// errors, or signed protocol material.
type Receipt struct {
	Version                 int       `json:"version"`
	RunID                   string    `json:"run_id"`
	Status                  Status    `json:"status"`
	Network                 string    `json:"network"`
	ChainID                 string    `json:"chain_id"`
	Vantage                 string    `json:"vantage"`
	Account                 string    `json:"account,omitempty"`
	ClientVersion           string    `json:"client_version"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
	AcknowledgedPriorRunIDs []string  `json:"acknowledged_prior_run_ids,omitempty"`

	PayloadSHA256          string `json:"payload_sha256,omitempty"`
	PayloadBytes           int    `json:"payload_bytes,omitempty"`
	DownloadSHA256         string `json:"download_sha256,omitempty"`
	DownloadBytes          int    `json:"download_bytes,omitempty"`
	ExpectedPaymentUTIA    string `json:"expected_payment_utia,omitempty"`
	PutDurationMillis      int64  `json:"put_duration_millis,omitempty"`
	DownloadDurationMillis int64  `json:"download_duration_millis,omitempty"`
	BlobID                 string `json:"blob_id,omitempty"`
	TxHash                 string `json:"tx_hash,omitempty"`
	SettlementHeight       int64  `json:"settlement_height,omitempty"`
	ErrorCode              string `json:"error_code,omitempty"`
}

func (r Receipt) validate() error {
	if r.Version != Version {
		return fmt.Errorf("receipt version %d is unsupported", r.Version)
	}
	if r.RunID == "" {
		return errors.New("receipt run_id is required")
	}
	if !validStatus(r.Status) {
		return fmt.Errorf("receipt status %q is invalid", r.Status)
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return errors.New("receipt timestamps are required")
	}
	return nil
}

func validStatus(status Status) bool {
	switch status {
	case StatusStarted, StatusPutSucceeded, StatusSucceeded,
		StatusPublishedUnverified, StatusIntegrityFailure, StatusInconclusive:
		return true
	default:
		return false
	}
}

func canTransition(from, to Status) bool {
	switch from {
	case StatusStarted:
		return to == StatusPutSucceeded || to == StatusInconclusive
	case StatusPutSucceeded:
		return to == StatusSucceeded || to == StatusPublishedUnverified || to == StatusIntegrityFailure
	default:
		return false
	}
}
