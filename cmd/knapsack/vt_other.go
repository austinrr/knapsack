//go:build !windows

package main

import "os"

// enableVT makes f, a terminal, understand the escape sequences liveList
// draws with. Terminals outside Windows always do.
func enableVT(*os.File) (restore func(), ok bool) { return func() {}, true }
