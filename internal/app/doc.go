// Package app holds Knapsack's use cases: everything a command does, in a
// form both the CLI and the future TUI call. It never prints. Problems the
// user can fix are returned as *UserError, with a hint; frontends render
// them.
package app
