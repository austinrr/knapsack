package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/austinrr/knapsack/internal/app"
)

// Context wrapped around a UserError stays on the error line, and the hint
// and (with --verbose) cause follow.
func TestRenderErrorWrapped(t *testing.T) {
	t.Parallel()
	ue := &app.UserError{Msg: "no current profile", Hint: "choose one", Err: errors.New("the cause")}
	err := fmt.Errorf("profile forever: %w", ue)

	tests := []struct {
		verbose bool
		want    string
	}{
		{false, "error: profile forever: no current profile\nhint: choose one\n"},
		{true, "error: profile forever: no current profile\nhint: choose one\ncause: the cause\n"},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		renderError(&buf, err, tt.verbose)
		if got := buf.String(); got != tt.want {
			t.Errorf("verbose=%v:\ngot  %q\nwant %q", tt.verbose, got, tt.want)
		}
	}
}
