//go:build !windows

package httpx

import (
	"errors"
	"syscall"
)

// isReset reports whether err is a connection the server reset.
func isReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET)
}
