package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/source"
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
		renderError(&buf, err, tt.verbose, env{})
		if got := buf.String(); got != tt.want {
			t.Errorf("verbose=%v:\ngot  %q\nwant %q", tt.verbose, got, tt.want)
		}
	}
}

// A website that answers unexpectedly gets a link to report it, with the
// error, the version and the platform filled in.
func TestRenderErrorReport(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("wago plater: %w: no data-page", source.ErrUnexpectedWebsite)
	var buf bytes.Buffer
	renderError(&buf, err, false, env{version: "v1.2.3", platform: "linux/amd64"})
	report, ok := strings.CutPrefix(strings.Split(buf.String(), "\n")[1], "report: ")
	if !ok {
		t.Fatalf("rendered %q, want a report line", buf.String())
	}
	u, perr := url.Parse(report)
	if perr != nil || u.Host != "github.com" || u.Path != "/austinrr/knapsack/issues/new" {
		t.Fatalf("report link = %q, %v", report, perr)
	}
	body := u.Query().Get("body")
	for _, want := range []string{err.Error(), "Knapsack v1.2.3 on linux/amd64"} {
		if !strings.Contains(body, want) {
			t.Errorf("issue body = %q, want it to have %q", body, want)
		}
	}
	if u.Query().Get("title") == "" {
		t.Error("issue has no title")
	}
}
