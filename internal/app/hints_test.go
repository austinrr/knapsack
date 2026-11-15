package app_test

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/source"
)

func TestReportURL(t *testing.T) {
	t.Parallel()
	if got := app.ReportURL(errors.New("rate limited"), "v1.0.0 on linux/amd64"); got != "" {
		t.Errorf("ReportURL for another error = %q, want empty", got)
	}
	err := fmt.Errorf("wago plater: %w: no data-page", source.ErrUnexpectedWebsite)
	got := app.ReportURL(err, "v1.0.0 on linux/amd64")
	issue, ok := strings.CutPrefix(got, app.IssuesURL+"/new?")
	if !ok {
		t.Fatalf("ReportURL = %q, want a new issue at %s", got, app.IssuesURL)
	}
	q, perr := url.ParseQuery(issue)
	if perr != nil {
		t.Fatal(perr)
	}
	if body := q.Get("body"); !strings.Contains(body, err.Error()) || !strings.Contains(body, "v1.0.0 on linux/amd64") {
		t.Errorf("issue body = %q, want the error and the version", body)
	}
}
