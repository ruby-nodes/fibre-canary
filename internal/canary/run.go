// Package canary contains the straight-line canary workflow.
package canary

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ruby-nodes/fibre-canary/internal/receipt"
)

type RunOptions struct {
	DataDir                       string
	Network                       string
	ChainID                       string
	Vantage                       string
	ClientVersion                 string
	MaxRunsPerDay                 int
	PayloadBytes                  int
	MaxPayloadBytes               int
	MaxPaymentUTIA                uint64
	AcceptPossibleDuplicateCharge bool
	Now                           func() time.Time
	NetworkClient                 Network
}

func Run(ctx context.Context, options RunOptions) (receipt.Receipt, error) {
	if options.NetworkClient == nil {
		return receipt.Receipt{}, fmt.Errorf("network client is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	store, err := receipt.Open(options.DataDir)
	if err != nil {
		return receipt.Receipt{}, err
	}
	lock, err := store.Lock()
	if err != nil {
		return receipt.Receipt{}, err
	}
	defer lock.Close()

	now := options.Now().UTC()
	acknowledged, _, err := Prepare(store, now, options.MaxRunsPerDay, options.AcceptPossibleDuplicateCharge)
	if err != nil {
		return receipt.Receipt{}, err
	}
	runID, err := newRunID(now)
	if err != nil {
		return receipt.Receipt{}, fmt.Errorf("generate run ID: %w", err)
	}
	payload, payloadHash, err := NewPayload(options.PayloadBytes, runID, now, options.Vantage)
	if err != nil {
		return receipt.Receipt{}, err
	}
	report, err := Preflight(ctx, options.NetworkClient, len(payload), Limits{MaxPayloadBytes: options.MaxPayloadBytes, MaxPaymentUTIA: options.MaxPaymentUTIA}, options.Vantage)
	if err != nil {
		return receipt.Receipt{}, err
	}
	r := receipt.Receipt{
		Version:                 receipt.Version,
		RunID:                   runID,
		Status:                  receipt.StatusStarted,
		Network:                 options.Network,
		ChainID:                 options.ChainID,
		Vantage:                 options.Vantage,
		Account:                 report.Account,
		ClientVersion:           report.ClientVersion,
		CreatedAt:               now,
		UpdatedAt:               now,
		AcknowledgedPriorRunIDs: acknowledged,
		PayloadSHA256:           payloadHash,
		PayloadBytes:            len(payload),
		ExpectedPaymentUTIA:     fmt.Sprint(report.PaymentUTIA),
	}

	// This durable intent is intentionally the last operation before Put.
	if err := store.Create(r); err != nil {
		return receipt.Receipt{}, fmt.Errorf("write started receipt: %w", err)
	}
	putStarted := options.Now()
	result, putErr := options.NetworkClient.Put(ctx, payload)
	if putErr != nil {
		r.ErrorCode = "ambiguous_put"
		r, updateErr := store.Update(r, receipt.StatusInconclusive, options.Now())
		if updateErr != nil {
			return r, fmt.Errorf("put failed ambiguously (%v) and receipt update failed: %w", putErr, updateErr)
		}
		return r, fmt.Errorf("put result is inconclusive: %w", putErr)
	}
	r.PutDurationMillis = options.Now().Sub(putStarted).Milliseconds()
	r.BlobID = result.BlobID
	r.TxHash = result.TxHash
	r.SettlementHeight = int64(result.Height)
	r, err = store.Update(r, receipt.StatusPutSucceeded, options.Now())
	if err != nil {
		return r, fmt.Errorf("write put result: %w", err)
	}

	downloadStarted := options.Now()
	downloaded, downloadErr := options.NetworkClient.Download(ctx, result.BlobID, result.Height)
	r.DownloadDurationMillis = options.Now().Sub(downloadStarted).Milliseconds()
	if downloadErr != nil {
		r.ErrorCode = "download_failed"
		r, updateErr := store.Update(r, receipt.StatusPublishedUnverified, options.Now())
		if updateErr != nil {
			return r, fmt.Errorf("download failed (%v) and receipt update failed: %w", downloadErr, updateErr)
		}
		return r, fmt.Errorf("publication could not be verified: %w", downloadErr)
	}
	downloadHash := sha256.Sum256(downloaded)
	r.DownloadSHA256 = hex.EncodeToString(downloadHash[:])
	r.DownloadBytes = len(downloaded)
	if !bytes.Equal(downloaded, payload) || r.DownloadSHA256 != r.PayloadSHA256 {
		r.ErrorCode = "payload_mismatch"
		r, err = store.Update(r, receipt.StatusIntegrityFailure, options.Now())
		if err != nil {
			return r, fmt.Errorf("write integrity failure: %w", err)
		}
		return r, fmt.Errorf("downloaded payload failed integrity verification")
	}
	r, err = store.Update(r, receipt.StatusSucceeded, options.Now())
	if err != nil {
		return r, fmt.Errorf("write successful verification: %w", err)
	}
	return r, nil
}

// Prepare performs recovery and admission checks while the caller holds the
// store lock. The returned IDs must be copied into any new paid-run receipt.
func Prepare(store *receipt.Store, now time.Time, maxRuns int, accept bool) (acknowledged []string, recovered []receipt.Receipt, err error) {
	recovered, err = store.RecoverInterrupted(now)
	if err != nil {
		return nil, nil, err
	}
	receipts, err := store.List()
	if err != nil {
		return nil, recovered, err
	}
	acknowledged, err = receipt.CheckAdmission(receipts, now, maxRuns, accept)
	return acknowledged, recovered, err
}

func newRunID(now time.Time) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return formatRunID(now, hex.EncodeToString(random[:])), nil
}

func formatRunID(now time.Time, suffix string) string {
	return now.UTC().Format("20060102T150405.000000000Z") + "-" + suffix
}
