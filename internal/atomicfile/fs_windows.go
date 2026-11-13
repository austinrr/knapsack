package atomicfile

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// errSharingViolation is ERROR_SHARING_VIOLATION, which package syscall
// doesn't name.
const errSharingViolation syscall.Errno = 32

// rename retries os.Rename for about a quarter of a second while the error is
// one that another process holding the file open causes, such as an
// antivirus scan or a reader that didn't open it with FILE_SHARE_DELETE. Go's
// own toolchain does the same (cmd/go/internal/robustio).
func rename(oldpath, newpath string) error {
	for delay := time.Millisecond; ; delay *= 2 {
		err := os.Rename(oldpath, newpath)
		if err == nil || !isTransient(err) || delay > 128*time.Millisecond {
			return err
		}
		time.Sleep(delay)
	}
}

func isTransient(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) &&
		(errno == syscall.ERROR_ACCESS_DENIED || errno == errSharingViolation)
}

// syncDir does nothing on Windows. Directories can't be opened for syncing
// there, and NTFS records the rename in its own journal.
func syncDir(string) error { return nil }
