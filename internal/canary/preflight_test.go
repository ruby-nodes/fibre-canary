package canary

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCanonicalPayloadIsExactAndDeterministic(t *testing.T) {
	now := time.Date(2026, 10, 29, 1, 2, 3, 4, time.UTC)
	random := bytes.NewReader(make([]byte, 16))
	a, hashA, err := newPayload(300, "run", now, "prague", random)
	if err != nil {
		t.Fatal(err)
	}
	b, hashB, err := newPayload(300, "run", now, "prague", bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 300 || !bytes.Equal(a, b) || hashA != hashB {
		t.Fatalf("canonical payload mismatch: len=%d equal=%v hashes=%q/%q", len(a), bytes.Equal(a, b), hashA, hashB)
	}
}

func TestOverPaymentLimitBlocksPutBeforeReceipt(t *testing.T) {
	putCalls := 0
	network := &fakeNetwork{
		info: NetworkInfo{Account: "celestia1test", ChainID: "mocha-5", ClientVersion: "test", PaymentUTIA: 101, AvailableEscrowUTIA: 1000},
		put:  func(context.Context, []byte) (PutResult, error) { putCalls++; return PutResult{}, nil },
	}
	options := baseOptions(filepath.Join(t.TempDir(), "data"), time.Now, network)
	options.MaxPaymentUTIA = 100
	_, err := Run(context.Background(), options)
	if !errors.Is(err, ErrPaymentLimit) {
		t.Fatalf("Run() error = %v, want payment limit", err)
	}
	if putCalls != 0 {
		t.Fatalf("Put called %d times, want 0", putCalls)
	}
}

func TestPreflightRejectsPayloadOverCeilingWithoutQueryingNetwork(t *testing.T) {
	t.Parallel()
	network := &fakeNetwork{info: NetworkInfo{Account: "celestia1test", ChainID: "mocha-5", ClientVersion: "test"}}
	_, err := Preflight(context.Background(), network, 200, Limits{MaxPayloadBytes: 100, MaxPaymentUTIA: 10000}, "test")
	if !errors.Is(err, ErrPayloadLimit) {
		t.Fatalf("Preflight() error = %v, want payload limit", err)
	}
	if network.preflightCalls != 0 {
		t.Fatalf("network.Preflight called %d times, want 0", network.preflightCalls)
	}
}

func TestPreflightRejectsInsufficientEscrow(t *testing.T) {
	t.Parallel()
	network := &fakeNetwork{info: NetworkInfo{Account: "celestia1test", ChainID: "mocha-5", ClientVersion: "test", PaymentUTIA: 50, AvailableEscrowUTIA: 10}}
	_, err := Preflight(context.Background(), network, 100, Limits{MaxPayloadBytes: 4096, MaxPaymentUTIA: 10000}, "test")
	if !errors.Is(err, ErrEscrow) {
		t.Fatalf("Preflight() error = %v, want escrow error", err)
	}
}

func TestSuccessfulPutPersistsOfficialResultFields(t *testing.T) {
	network := &fakeNetwork{put: func(context.Context, []byte) (PutResult, error) {
		return PutResult{BlobID: "blob-id", TxHash: "ABC123", Height: 42}, nil
	}}
	r, err := Run(context.Background(), baseOptions(filepath.Join(t.TempDir(), "data"), time.Now, network))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "succeeded" || r.BlobID != "blob-id" || r.TxHash != "ABC123" || r.SettlementHeight != 42 {
		t.Fatalf("receipt = %#v", r)
	}
}
