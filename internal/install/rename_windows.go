package install

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Windows error codes package syscall doesn't name.
const (
	errSharingViolation syscall.Errno = 32
	errLockViolation    syscall.Errno = 33
)

// renameTimeout is how long rename keeps retrying (DESIGN.md §6.3).
const renameTimeout = 2 * time.Second

// rename retries os.Rename for about two seconds while the error is one an
// open file can cause: antivirus scanning freshly extracted files, or the
// game holding an addon's files. Go's own toolchain does the same
// (cmd/go/internal/robustio). If it still fails with a sharing or lock
// violation, the error wraps ErrInUse. Access denied is retried too, since
// a pending delete causes it, but a lasting one is a real permission error.
func rename(oldpath, newpath string) error {
	start := time.Now()
	for delay := 10 * time.Millisecond; ; delay = min(2*delay, 250*time.Millisecond) {
		err := os.Rename(oldpath, newpath)
		if err == nil || !isTransient(err) {
			return err
		}
		if time.Since(start) >= renameTimeout {
			if isInUse(err) {
				return fmt.Errorf("%w: %w", ErrInUse, err)
			}
			return err
		}
		time.Sleep(delay)
	}
}

func isTransient(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.ERROR_ACCESS_DENIED || isInUse(err))
}

func isInUse(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == errSharingViolation || errno == errLockViolation)
}
