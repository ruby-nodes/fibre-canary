package canary

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"example.com/fibre-canary/internal/receipt"
)

func TestRunWritesStartedImmediatelyBeforePut(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	called := false
	r, err := Run(context.Background(), baseOptions(dataDir, clock, &fakeNetwork{put: func(context.Context, []byte) (PutResult, error) {
		called = true
		store, openErr := receipt.Open(dataDir)
		if openErr != nil {
			return PutResult{}, openErr
		}
		history, listErr := store.List()
		if listErr != nil {
			return PutResult{}, listErr
		}
		if len(history) != 1 || history[0].Status != receipt.StatusStarted {
			return PutResult{}, errors.New("started receipt was not durable before Put")
		}
		return PutResult{BlobID: "blob", TxHash: "tx", Height: 12}, nil
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if !called || r.Status != receipt.StatusSucceeded {
		t.Fatalf("called = %v, receipt = %#v", called, r)
	}
}

func TestRunUsesPublicationHeightAndVerifiesExactPayload(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	network := &fakeNetwork{
		put: func(context.Context, []byte) (PutResult, error) {
			return PutResult{BlobID: "blob", TxHash: "tx", Height: 42}, nil
		},
		download: func(_ context.Context, blobID string, height uint64, payload []byte) ([]byte, error) {
			if blobID != "blob" || height != 42 {
				t.Fatalf("Download(%q, %d), want blob, 42", blobID, height)
			}
			return append([]byte(nil), payload...), nil
		},
	}
	r, err := Run(context.Background(), baseOptions(dataDir, clock, network))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != receipt.StatusSucceeded || r.DownloadSHA256 != r.PayloadSHA256 || r.DownloadBytes != r.PayloadBytes {
		t.Fatalf("verified receipt = %#v", r)
	}
}

func TestRunRecordsPublishedUnverifiedAfterDownloadFailure(t *testing.T) {
	t.Parallel()

	network := &fakeNetwork{
		put: func(context.Context, []byte) (PutResult, error) {
			return PutResult{BlobID: "blob", TxHash: "tx", Height: 42}, nil
		},
		download: func(context.Context, string, uint64, []byte) ([]byte, error) {
			return nil, errors.New("unsafe download detail")
		},
	}
	r, err := Run(context.Background(), baseOptions(filepath.Join(t.TempDir(), "data"), time.Now, network))
	if err == nil {
		t.Fatal("Run() error = nil, want download failure")
	}
	if r.Status != receipt.StatusPublishedUnverified || r.ErrorCode != "download_failed" {
		t.Fatalf("receipt = %#v", r)
	}
}

func TestRunRecordsIntegrityFailureForByteMismatch(t *testing.T) {
	t.Parallel()

	network := &fakeNetwork{
		put: func(context.Context, []byte) (PutResult, error) {
			return PutResult{BlobID: "blob", TxHash: "tx", Height: 42}, nil
		},
		download: func(context.Context, string, uint64, []byte) ([]byte, error) {
			return []byte("different"), nil
		},
	}
	r, err := Run(context.Background(), baseOptions(filepath.Join(t.TempDir(), "data"), time.Now, network))
	if err == nil {
		t.Fatal("Run() error = nil, want integrity failure")
	}
	if r.Status != receipt.StatusIntegrityFailure || r.ErrorCode != "payload_mismatch" {
		t.Fatalf("receipt = %#v", r)
	}
	if r.DownloadSHA256 == "" || r.DownloadSHA256 == r.PayloadSHA256 {
		t.Fatalf("download hash = %q, payload hash = %q", r.DownloadSHA256, r.PayloadSHA256)
	}
}

func TestAbandonedPutRequiresAcknowledgementAndPersistsReference(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	abandoned, err := Run(context.Background(), baseOptions(dataDir, clock, &fakeNetwork{put: func(context.Context, []byte) (PutResult, error) {
		return PutResult{}, errors.New("unsafe remote detail")
	}}))
	if err == nil {
		t.Fatal("Run() error = nil, want abandoned Put error")
	}

	putCalls := 0
	options := baseOptions(dataDir, clock, &fakeNetwork{put: func(context.Context, []byte) (PutResult, error) {
		putCalls++
		return PutResult{BlobID: "blob", TxHash: "tx", Height: 12}, nil
	}})
	options.MaxRunsPerDay = 2
	if _, err := Run(context.Background(), options); !errors.Is(err, receipt.ErrInconclusive) {
		t.Fatalf("Run() without acknowledgement error = %v", err)
	}
	if putCalls != 0 {
		t.Fatalf("Put called %d times after admission refusal, want 0", putCalls)
	}
	options.AcceptPossibleDuplicateCharge = true
	next, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.AcknowledgedPriorRunIDs) != 1 || next.AcknowledgedPriorRunIDs[0] != abandoned.RunID {
		t.Fatalf("acknowledged IDs = %v, want %q", next.AcknowledgedPriorRunIDs, abandoned.RunID)
	}
	store, err := receipt.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.Read(abandoned.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != receipt.StatusInconclusive || old.ErrorCode != "ambiguous_put" {
		t.Fatalf("abandoned receipt = %#v", old)
	}
	storedNext, err := store.Read(next.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedNext.AcknowledgedPriorRunIDs) != 1 || storedNext.AcknowledgedPriorRunIDs[0] != abandoned.RunID {
		t.Fatalf("persisted acknowledged IDs = %v", storedNext.AcknowledgedPriorRunIDs)
	}

	// Once formally acknowledged, the same resolved incident must not force
	// --accept-possible-duplicate-charge on every later run forever.
	options.MaxRunsPerDay = 3
	options.AcceptPossibleDuplicateCharge = false
	putCalls = 0
	third, err := Run(context.Background(), options)
	if err != nil {
		t.Fatalf("Run() after acknowledgement without the flag error = %v, want nil", err)
	}
	if putCalls != 1 {
		t.Fatalf("Put called %d times for the unblocked run, want 1", putCalls)
	}
	if len(third.AcknowledgedPriorRunIDs) != 0 {
		t.Fatalf("acknowledged IDs on an already-resolved run = %v, want none", third.AcknowledgedPriorRunIDs)
	}
}

func TestProcessCrashDuringPutLeavesRecoverableIntent(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashDuringPutHelper$")
	cmd.Env = append(os.Environ(), "FIBRE_CANARY_CRASH_DATA_DIR="+dataDir)
	if err := cmd.Run(); err == nil {
		t.Fatal("crashing helper exited successfully")
	}

	store, err := receipt.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Status != receipt.StatusStarted {
		t.Fatalf("history after crash = %#v, want one started receipt", history)
	}
	recovered, err := store.RecoverInterrupted(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].Status != receipt.StatusInconclusive {
		t.Fatalf("recovered history = %#v, want one inconclusive receipt", recovered)
	}
}

func TestCrashDuringPutHelper(t *testing.T) {
	dataDir := os.Getenv("FIBRE_CANARY_CRASH_DATA_DIR")
	if dataDir == "" {
		t.Skip("subprocess helper")
	}
	options := baseOptions(dataDir, time.Now, &fakeNetwork{put: func(context.Context, []byte) (PutResult, error) {
		os.Exit(23)
		return PutResult{}, nil
	}})
	_, _ = Run(context.Background(), options)
	t.Fatal("Run returned after crash simulation")
}

func TestProcessCrashDuringDownloadLeavesRecoverablePublication(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashDuringDownloadHelper$")
	cmd.Env = append(os.Environ(), "FIBRE_CANARY_DOWNLOAD_CRASH_DATA_DIR="+dataDir)
	if err := cmd.Run(); err == nil {
		t.Fatal("crashing helper exited successfully")
	}

	store, err := receipt.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Status != receipt.StatusPutSucceeded {
		t.Fatalf("history after crash = %#v, want one put_succeeded receipt", history)
	}
	recovered, err := store.RecoverInterrupted(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].Status != receipt.StatusPublishedUnverified || recovered[0].ErrorCode != "interrupted_download" {
		t.Fatalf("recovered history = %#v", recovered)
	}
}

func TestCrashDuringDownloadHelper(t *testing.T) {
	dataDir := os.Getenv("FIBRE_CANARY_DOWNLOAD_CRASH_DATA_DIR")
	if dataDir == "" {
		t.Skip("subprocess helper")
	}
	network := &fakeNetwork{
		put: func(context.Context, []byte) (PutResult, error) {
			return PutResult{BlobID: "blob", TxHash: "tx", Height: 12}, nil
		},
		download: func(context.Context, string, uint64, []byte) ([]byte, error) {
			os.Exit(24)
			return nil, nil
		},
	}
	_, _ = Run(context.Background(), baseOptions(dataDir, time.Now, network))
	t.Fatal("Run returned after crash simulation")
}

func baseOptions(dataDir string, now func() time.Time, network Network) RunOptions {
	return RunOptions{
		DataDir:         dataDir,
		Network:         "mocha",
		ChainID:         "mocha-5",
		Vantage:         "test",
		MaxRunsPerDay:   1,
		PayloadBytes:    256,
		MaxPayloadBytes: 4096,
		MaxPaymentUTIA:  10000,
		Now:             now,
		NetworkClient:   network,
	}
}

type fakeNetwork struct {
	info           NetworkInfo
	put            func(context.Context, []byte) (PutResult, error)
	download       func(context.Context, string, uint64, []byte) ([]byte, error)
	payload        []byte
	preflightCalls int
}

func (f *fakeNetwork) Preflight(context.Context, int) (NetworkInfo, error) {
	f.preflightCalls++
	if f.info.Account == "" {
		return NetworkInfo{Account: "celestia1test", Endpoint: "test", ChainID: "mocha-5", ClientVersion: "test-version", EncodedBytes: 512, PaymentUTIA: 10, AvailableEscrowUTIA: 100}, nil
	}
	return f.info, nil
}

func (f *fakeNetwork) Put(ctx context.Context, payload []byte) (PutResult, error) {
	f.payload = append([]byte(nil), payload...)
	return f.put(ctx, payload)
}

func (f *fakeNetwork) Download(ctx context.Context, blobID string, height uint64) ([]byte, error) {
	if f.download != nil {
		return f.download(ctx, blobID, height, f.payload)
	}
	return append([]byte(nil), f.payload...), nil
}
