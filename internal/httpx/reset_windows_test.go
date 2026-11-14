package httpx

import "syscall"

// resetErrno is the errno a reset connection gives on this OS.
const resetErrno = syscall.WSAECONNRESET
