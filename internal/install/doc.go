// Package install puts addon folders into an AddOns directory. It works on
// the filesystem only and never touches the network.
//
// Install extracts an archive into a staging folder, validates each addon
// folder the way the game client would (Validate, DESIGN.md §6.4), and
// swaps the folders into place, rolling back on failure (§6.3). Remove
// deletes folders the same way, and Cleanup resolves anything an
// interrupted run left behind.
package install
