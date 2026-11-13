// Package lock provides the per-profile and config locks that stop two
// Knapsack processes from changing the same files at once. It wraps
// gofrs/flock, which uses flock(2) on Unix and LockFileEx on Windows.
//
// The locks are advisory, and the OS drops them when the process exits, so
// a crash never leaves a stale lock behind. The lock file itself stays on
// disk; only the lock on it matters.
package lock
