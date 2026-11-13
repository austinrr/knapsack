package lock_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/austinrr/knapsack/internal/lock"
)

// Locks are per open file, so a second Acquire in the same process contends
// just as a second Knapsack process would.
func TestSecondAcquireFailsFast(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "forever.lock")
	first, err := lock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()

	done := make(chan error, 1)
	go func() {
		second, err := lock.Acquire(path)
		if err == nil {
			_ = second.Release()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, lock.ErrLocked) {
			t.Errorf("second Acquire error = %v, want ErrLocked", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second Acquire blocked instead of failing fast")
	}
}

func TestReleaseAllowsReacquire(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "forever.lock")
	first, err := lock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	// Releasing twice is harmless, so a deferred Release after an explicit
	// one is fine.
	if err := first.Release(); err != nil {
		t.Errorf("second Release: %v", err)
	}
	second, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

// Different profiles lock independently.
func TestIndependentLocks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := lock.Acquire(filepath.Join(dir, "forever.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Release() }()
	b, err := lock.Acquire(filepath.Join(dir, "retail.lock"))
	if err != nil {
		t.Fatalf("Acquire of a different lock: %v", err)
	}
	_ = b.Release()
}

func TestAcquireCreatesDir(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config", "state", "forever.lock")
	l, err := lock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Release()
}
