// Package lock provides the per-profile and config locks that stop two
// Knapsack processes from changing the same files at once. It wraps
// gofrs/flock, which uses flock(2) on Unix and LockFileEx on Windows.
//
// The locks are advisory, and the OS drops them when the process exits, so
// a crash never leaves a stale lock behind. The lock file itself stays on
// disk; only the lock on it matters.
package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// ErrLocked means another process holds the lock. Callers turn it into
// user-facing text, such as which profile is in use.
var ErrLocked = errors.New("locked by another process")

// Lock is a held lock. Release it when the command finishes.
type Lock struct {
	f *flock.Flock
}

// Acquire takes the lock at path without waiting: if another process holds
// it, Acquire fails at once with an error wrapping ErrLocked. It creates the
// parent directory if needed.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	f := flock.New(path)
	ok, err := f.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !ok {
		return nil, fmt.Errorf("lock %s: %w", path, ErrLocked)
	}
	return &Lock{f: f}, nil
}

// Release drops the lock. Calling it again does nothing.
func (l *Lock) Release() error {
	if err := l.f.Unlock(); err != nil {
		return fmt.Errorf("unlock %s: %w", l.f.Path(), err)
	}
	return nil
}
