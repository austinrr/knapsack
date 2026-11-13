package atomicfile

// syncDir does nothing on Windows. Directories can't be opened for syncing
// there, and NTFS records the rename in its own journal.
func syncDir(string) error { return nil }
