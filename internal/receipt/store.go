package receipt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	directoryMode = 0o700
	fileMode      = 0o600
)

type Store struct {
	dataDir string
	runsDir string
}

func Open(dataDir string) (*Store, error) {
	if err := ensurePrivateDirectory(dataDir); err != nil {
		return nil, fmt.Errorf("prepare data directory: %w", err)
	}
	runsDir := filepath.Join(dataDir, "runs")
	if err := ensurePrivateDirectory(runsDir); err != nil {
		return nil, fmt.Errorf("prepare runs directory: %w", err)
	}
	return &Store{dataDir: dataDir, runsDir: runsDir}, nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, directoryMode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("path is not a directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("directory ownership is unavailable")
	}
	if err := validateDirectoryOwner(stat.Uid); err != nil {
		return err
	}
	return os.Chmod(path, directoryMode)
}

func validateDirectoryOwner(uid uint32) error {
	if uid != uint32(os.Geteuid()) {
		return errors.New("directory is not owned by the current user")
	}
	return nil
}

type Lock struct {
	file *os.File
}

func (s *Store) Lock() (*Lock, error) {
	path := filepath.Join(s.dataDir, "canary.lock")
	f, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("open data-directory lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("acquire data-directory lock: %w", err)
	}
	return &Lock{file: f}, nil
}

func openLockFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, fileMode)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		syscall.Close(fd)
		return nil, errors.New("create lock file handle")
	}
	closeWithError := func(err error) (*os.File, error) {
		return nil, errors.Join(err, f.Close())
	}

	var opened syscall.Stat_t
	if err := syscall.Fstat(fd, &opened); err != nil {
		return closeWithError(fmt.Errorf("inspect opened lock: %w", err))
	}
	if opened.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return closeWithError(errors.New("lock is not a regular file"))
	}
	if opened.Uid != uint32(os.Geteuid()) {
		return closeWithError(errors.New("lock is not owned by the current user"))
	}
	if opened.Nlink != 1 {
		return closeWithError(errors.New("lock has multiple filesystem links"))
	}
	if err := f.Chmod(fileMode); err != nil {
		return closeWithError(fmt.Errorf("set lock permissions: %w", err))
	}

	var current syscall.Stat_t
	if err := syscall.Lstat(path, &current); err != nil {
		return closeWithError(fmt.Errorf("inspect lock path: %w", err))
	}
	if current.Mode&syscall.S_IFMT != syscall.S_IFREG || current.Dev != opened.Dev || current.Ino != opened.Ino {
		return closeWithError(errors.New("lock path changed while opening"))
	}
	return f, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	errUnlock := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	errClose := l.file.Close()
	l.file = nil
	return errors.Join(errUnlock, errClose)
}

func (s *Store) Create(r Receipt) error {
	if err := r.validate(); err != nil {
		return err
	}
	if _, err := os.Stat(s.path(r.RunID)); err == nil {
		return fmt.Errorf("receipt %q already exists", r.RunID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect receipt: %w", err)
	}
	return s.write(r)
}

func (s *Store) Update(r Receipt, next Status, at time.Time) (Receipt, error) {
	current, err := s.Read(r.RunID)
	if err != nil {
		return Receipt{}, err
	}
	if current.Status != r.Status || !canTransition(current.Status, next) {
		return Receipt{}, fmt.Errorf("%w: %s to %s", ErrInvalidTransition, current.Status, next)
	}
	r.Status = next
	r.UpdatedAt = at.UTC()
	if err := s.write(r); err != nil {
		return Receipt{}, err
	}
	return r, nil
}

func (s *Store) Read(runID string) (Receipt, error) {
	if err := validateRunID(runID); err != nil {
		return Receipt{}, err
	}
	path := s.path(runID)
	info, err := os.Lstat(path)
	if err != nil {
		return Receipt{}, fmt.Errorf("inspect receipt %q: %w", runID, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Receipt{}, fmt.Errorf("receipt %q is not a regular file", runID)
	}
	if err := os.Chmod(path, fileMode); err != nil {
		return Receipt{}, fmt.Errorf("set receipt %q permissions: %w", runID, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return Receipt{}, fmt.Errorf("open receipt %q: %w", runID, err)
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	var r Receipt
	if err := decoder.Decode(&r); err != nil {
		return Receipt{}, fmt.Errorf("decode receipt %q: %w", runID, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Receipt{}, fmt.Errorf("decode receipt %q: %w", runID, err)
	}
	if err := r.validate(); err != nil {
		return Receipt{}, fmt.Errorf("validate receipt %q: %w", runID, err)
	}
	if r.RunID != runID {
		return Receipt{}, fmt.Errorf("receipt filename does not match run_id")
	}
	return r, nil
}

func (s *Store) List() ([]Receipt, error) {
	entries, err := os.ReadDir(s.runsDir)
	if err != nil {
		return nil, fmt.Errorf("list receipts: %w", err)
	}
	receipts := make([]Receipt, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		r, err := s.Read(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, r)
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].CreatedAt.Before(receipts[j].CreatedAt) })
	return receipts, nil
}

// RecoverInterrupted classifies non-terminal receipts left by interrupted runs.
// Callers hold the data-directory lock while running recovery.
func (s *Store) RecoverInterrupted(at time.Time) ([]Receipt, error) {
	receipts, err := s.List()
	if err != nil {
		return nil, err
	}
	var recovered []Receipt
	for _, r := range receipts {
		var next Status
		switch r.Status {
		case StatusStarted:
			r.ErrorCode = "interrupted_put"
			next = StatusInconclusive
		case StatusPutSucceeded:
			r.ErrorCode = "interrupted_download"
			next = StatusPublishedUnverified
		default:
			continue
		}
		r, err = s.Update(r, next, at)
		if err != nil {
			return nil, fmt.Errorf("recover run %q: %w", r.RunID, err)
		}
		recovered = append(recovered, r)
	}
	return recovered, nil
}

// Inconclusive returns inconclusive receipts that have not yet been formally
// acknowledged by a later receipt's AcknowledgedPriorRunIDs. An inconclusive
// receipt is never mutated and remains a permanent audit record, but once a
// subsequent run has acknowledged it, it must stop blocking admission for
// future runs; otherwise a single incident would block every future run
// forever.
func Inconclusive(receipts []Receipt) []Receipt {
	acknowledged := make(map[string]bool, len(receipts))
	for _, r := range receipts {
		for _, id := range r.AcknowledgedPriorRunIDs {
			acknowledged[id] = true
		}
	}
	var found []Receipt
	for _, r := range receipts {
		if r.Status == StatusInconclusive && !acknowledged[r.RunID] {
			found = append(found, r)
		}
	}
	return found
}

func CheckAdmission(receipts []Receipt, now time.Time, maxRuns int, acceptPossibleDuplicate bool) ([]string, error) {
	prior := Inconclusive(receipts)
	if len(prior) > 0 && !acceptPossibleDuplicate {
		return nil, ErrInconclusive
	}
	day := now.UTC().Format("2006-01-02")
	count := 0
	for _, r := range receipts {
		if r.CreatedAt.UTC().Format("2006-01-02") == day {
			count++
		}
	}
	if count >= maxRuns {
		return nil, fmt.Errorf("%w: %d of %d UTC", ErrDailyLimit, count, maxRuns)
	}
	ids := make([]string, len(prior))
	for i := range prior {
		ids[i] = prior[i].RunID
	}
	return ids, nil
}

func (s *Store) write(r Receipt) error {
	if err := r.validate(); err != nil {
		return err
	}
	if err := validateRunID(r.RunID); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.runsDir, "."+r.RunID+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary receipt: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return fmt.Errorf("set temporary receipt permissions: %w", err)
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r); err != nil {
		tmp.Close()
		return fmt.Errorf("encode receipt: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary receipt: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary receipt: %w", err)
	}
	if err := os.Rename(tmpPath, s.path(r.RunID)); err != nil {
		return fmt.Errorf("replace receipt: %w", err)
	}
	removeTemp = false
	dir, err := os.Open(s.runsDir)
	if err != nil {
		return fmt.Errorf("open runs directory for sync: %w", err)
	}
	errSync := dir.Sync()
	errClose := dir.Close()
	if err := errors.Join(errSync, errClose); err != nil {
		return fmt.Errorf("sync runs directory: %w", err)
	}
	return nil
}

func (s *Store) path(runID string) string { return filepath.Join(s.runsDir, runID+".json") }

func validateRunID(runID string) error {
	if runID == "" || filepath.Base(runID) != runID || strings.ContainsAny(runID, "/\\\x00") {
		return errors.New("invalid receipt run_id")
	}
	return nil
}
