// Package config loads, validates and saves config.toml: the profiles, the
// addons each one wants, the current profile and source API keys. Config is
// what the user wants; package state records what is actually installed.
//
// Saving rewrites the whole file in a fixed layout, so comments are lost and
// diffs stay small under version control.
package config
