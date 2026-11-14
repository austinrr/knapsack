// Package tomlenc formats TOML values for Knapsack's hand-written encoders.
// Config and state files are written in a fixed layout that the TOML
// library's encoder can't produce, so they write their tables themselves and
// use these helpers for the values.
package tomlenc
