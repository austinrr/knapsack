// Package state loads and saves each profile's state file: which release of
// each addon is installed and which folders it owns. Config is what the user
// wants; state is what Knapsack has actually installed (DESIGN.md §5.3).
//
// State is the only data Knapsack can't rebuild, so Load is strict: unknown
// keys, unknown schema versions and anything that could make a later Remove
// delete the wrong folder are errors.
package state
