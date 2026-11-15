package main

import "testing"

// knapsack tui draws on a terminal; anywhere else it says what to use
// instead. The TUI itself is tested in internal/tui.
func TestTUICommand(t *testing.T) {
	t.Parallel()
	runCases(t, []cliCase{
		{name: "tui/not_a_terminal", args: []string{"tui"}},
		{name: "tui/extra_argument", args: []string{"tui", "retail"}},
	})
}
