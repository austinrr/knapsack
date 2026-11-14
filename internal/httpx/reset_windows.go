package httpx

import (
	"errors"
	"syscall"
)

// isReset reports whether err is a connection the server reset. Windows
// reports it as WSAECONNRESET; syscall.ECONNRESET there is an invented
// value that the network stack never returns.
func isReset(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET)
}
