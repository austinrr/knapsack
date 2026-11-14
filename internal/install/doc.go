// Package install puts addon folders into an AddOns directory. It works on
// the filesystem only and never touches the network.
//
// So far it has Validate, which checks an addon folder the way the game
// client would before Knapsack installs it (DESIGN.md §6.4).
package install
