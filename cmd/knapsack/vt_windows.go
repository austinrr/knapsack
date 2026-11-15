package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT makes f, a console, understand the escape sequences liveList
// draws with, as Windows 10 and later can, and returns how to undo it.
// It's false for a console that can't, such as before Windows 10.
func enableVT(f *os.File) (restore func(), ok bool) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return nil, false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return func() {}, true
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return nil, false
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }, true
}
