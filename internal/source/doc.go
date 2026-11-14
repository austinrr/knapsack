// Package source defines how Knapsack finds an addon's newest release: the
// Source interface each provider implements, the Release it returns, a
// Registry of the configured sources, and the errors every source shares
// (DESIGN.md §6.1). Implementations live in subpackages, one per provider.
package source
