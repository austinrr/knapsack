// Package toc parses the .toc files that describe a WoW addon: its
// "## Key: Value" headers, the Interface numbers it was built for, and the
// game types it allows or excludes. It follows the format documented at
// warcraft.wiki.gg/wiki/TOC_format. Choosing which TOC the client loads and
// validating it against the client happen elsewhere.
package toc
