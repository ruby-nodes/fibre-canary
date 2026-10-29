package receipt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStorePermissionsAtomicReplacementAndTransitions(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	r := testReceipt("run-1", StatusStarted, now)
	if err := store.Create(r); err != nil {
		t.Fatal(err)
	}

	assertMode(t, dataDir, 0o700)
	assertMode(t, filepath.Join(dataDir, "runs"), 0o700)
	path := filepath.Join(dataDir, "runs", "run-1.json")
	assertMode(t, path, 0o600)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("run-1"); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	r.BlobID = "fake-blob"
	r, err = store.Update(r, StatusPutSucceeded, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeStat, beforeOK := before.Sys().(*syscall.Stat_t)
	afterStat, afterOK := after.Sys().(*syscall.Stat_t)
	if beforeOK && afterOK && beforeStat.Ino == afterStat.Ino {
		t.Fatal("receipt update did not atomically replace the file")
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "run-1.json" {
		t.Fatalf("runs directory contains temporary artifacts: %v", entries)
	}
	got, err := store.Read("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPutSucceeded || got.BlobID != "fake-blob" {
		t.Fatalf("stored receipt = %#v", got)
	}
	if _, err := store.Update(got, StatusInconclusive, now.Add(2*time.Second)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid transition error = %v", err)
	}
}

func TestStoreMakesExistingDirectoriesPrivate(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dataDir); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dataDir, 0o700)
	assertMode(t, filepath.Join(dataDir, "runs"), 0o700)
}

func TestValidateDirectoryOwnerRejectsDifferentUser(t *testing.T) {
	t.Parallel()

	current := uint32(os.Geteuid())
	if err := validateDirectoryOwner(current); err != nil {
		t.Fatalf("current owner rejected: %v", err)
	}
	different := current ^ 1
	if err := validateDirectoryOwner(different); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("different owner error = %v, want ownership rejection", err)
	}
}

func TestAllowedTerminalTransitions(t *testing.T) {
	t.Parallel()

	for _, terminal := range []Status{StatusSucceeded, StatusPublishedUnverified, StatusIntegrityFailure} {
		terminal := terminal
		t.Run(string(terminal), func(t *testing.T) {
			t.Parallel()
			store, err := Open(filepath.Join(t.TempDir(), "data"))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
			r := testReceipt("run-terminal", StatusStarted, now)
			if err := store.Create(r); err != nil {
				t.Fatal(err)
			}
			r, err = store.Update(r, StatusPutSucceeded, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			r, err = store.Update(r, terminal, now.Add(2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != terminal {
				t.Fatalf("status = %q, want %q", r.Status, terminal)
			}
			if _, err := store.Update(r, StatusPutSucceeded, now.Add(3*time.Second)); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("transition from terminal error = %v", err)
			}
		})
	}
}

func TestLockPreventsConcurrentLocalRuns(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := store.Lock(); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Lock() error = %v, want ErrLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := store.Lock()
	if err != nil {
		t.Fatalf("Lock() after release error = %v", err)
	}
	defer second.Close()
	assertMode(t, filepath.Join(store.dataDir, "canary.lock"), 0o600)
}

func TestLockRejectsSymlink(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(store.dataDir, "canary.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lock(); err == nil {
		t.Fatal("Lock() accepted a symlink")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "unchanged" {
		t.Fatalf("symlink target contents = %q", got)
	}
	assertMode(t, target, 0o644)
}

func TestLockRejectsNonRegularFile(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dataDir, "canary.lock")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lock(); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Lock() error = %v, want non-regular-file rejection", err)
	}
}

func TestLockPreventsConcurrentProcess(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestLockProcessHelper$")
	cmd.Env = append(os.Environ(), "FIBRE_CANARY_LOCK_HELPER="+store.dataDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lock helper failed: %v\n%s", err, output)
	}
}

func TestLockProcessHelper(t *testing.T) {
	dataDir := os.Getenv("FIBRE_CANARY_LOCK_HELPER")
	if dataDir == "" {
		t.Skip("subprocess helper")
	}
	store, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lock(); !errors.Is(err, ErrLocked) {
		t.Fatalf("Lock() error = %v, want ErrLocked", err)
	}
}

func TestRecoveryAcknowledgementAndDailyLimit(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	if err := store.Create(testReceipt("abandoned", StatusStarted, now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverInterrupted(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].Status != StatusInconclusive || recovered[0].ErrorCode != "interrupted_put" {
		t.Fatalf("recovered = %#v", recovered)
	}
	history, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckAdmission(history, now, 2, false); !errors.Is(err, ErrInconclusive) {
		t.Fatalf("CheckAdmission without acknowledgement error = %v", err)
	}
	ids, err := CheckAdmission(history, now, 2, true)
	if err != nil || len(ids) != 1 || ids[0] != "abandoned" {
		t.Fatalf("CheckAdmission acknowledgement = %v, %v", ids, err)
	}
	if _, err := CheckAdmission(history, now, 1, true); !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("CheckAdmission at daily limit error = %v", err)
	}
	if _, err := CheckAdmission(history, now.Add(24*time.Hour), 1, true); err != nil {
		t.Fatalf("CheckAdmission on next UTC day error = %v", err)
	}
}

func TestAcknowledgedInconclusiveRunNoLongerBlocksFutureAdmission(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	if err := store.Create(testReceipt("abandoned", StatusStarted, now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverInterrupted(now); err != nil {
		t.Fatal(err)
	}
	history, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	ids, err := CheckAdmission(history, now, 10, true)
	if err != nil || len(ids) != 1 || ids[0] != "abandoned" {
		t.Fatalf("CheckAdmission acknowledgement = %v, %v", ids, err)
	}

	// Simulate the next run referencing the acknowledged ID in its own
	// receipt, exactly as canary.Run does before writing status=started.
	acknowledging := testReceipt("acknowledging", StatusStarted, now)
	acknowledging.AcknowledgedPriorRunIDs = ids
	if err := store.Create(acknowledging); err != nil {
		t.Fatal(err)
	}
	acknowledging, err = store.Update(acknowledging, StatusPutSucceeded, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(acknowledging, StatusSucceeded, now); err != nil {
		t.Fatal(err)
	}

	history, err = store.List()
	if err != nil {
		t.Fatal(err)
	}
	// "abandoned" was formally acknowledged by "acknowledging"; it must
	// remain on record as inconclusive forever (audit trail) but must not
	// keep requiring --accept-possible-duplicate-charge on every later run.
	if _, err := CheckAdmission(history, now, 10, false); err != nil {
		t.Fatalf("CheckAdmission after acknowledgement error = %v, want nil", err)
	}
	abandoned, err := store.Read("abandoned")
	if err != nil {
		t.Fatal(err)
	}
	if abandoned.Status != StatusInconclusive {
		t.Fatalf("acknowledged receipt status = %q, want unchanged %q", abandoned.Status, StatusInconclusive)
	}
}

func TestRecoveryMarksInterruptedDownloadPublishedUnverified(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	r := testReceipt("interrupted-download", StatusStarted, now.Add(-time.Hour))
	if err := store.Create(r); err != nil {
		t.Fatal(err)
	}
	r, err = store.Update(r, StatusPutSucceeded, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	recovered, err := store.RecoverInterrupted(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].Status != StatusPublishedUnverified || recovered[0].ErrorCode != "interrupted_download" {
		t.Fatalf("recovered = %#v", recovered)
	}
}

func testReceipt(id string, status Status, now time.Time) Receipt {
	return Receipt{
		Version:       Version,
		RunID:         id,
		Status:        status,
		Network:       "mocha",
		ChainID:       "mocha-5",
		Vantage:       "test",
		ClientVersion: "test-version",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
