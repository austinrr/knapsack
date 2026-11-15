package app

import (
	"errors"
	"net/http"
	"testing"

	"github.com/austinrr/knapsack/internal/httpx"
	"github.com/austinrr/knapsack/internal/source"
)

// A download the source refuses gets the source error it means, as a
// refused lookup does, so the same hints apply; anything else is left be.
func TestDownloadError(t *testing.T) {
	t.Parallel()
	refused := func(code int, header http.Header) error {
		return &httpx.StatusError{URL: "https://api.github.com/repos/o/r/releases/assets/1", StatusCode: code, Header: header}
	}
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"bad token", refused(http.StatusUnauthorized, nil), source.ErrUnauthorized},
		{"gone", refused(http.StatusNotFound, nil), source.ErrNotFound},
		{"too many", refused(http.StatusTooManyRequests, nil), source.ErrRateLimited},
		{"no requests left", refused(http.StatusForbidden, http.Header{"X-Ratelimit-Remaining": {"0"}}), source.ErrRateLimited},
		{"secondary limit", refused(http.StatusForbidden, http.Header{"Retry-After": {"60"}}), source.ErrRateLimited},
		{"forbidden", refused(http.StatusForbidden, nil), nil},
		{"server error", refused(http.StatusBadGateway, nil), nil},
		{"not a refusal", httpx.ErrTooLarge, nil},
	}
	for _, tt := range tests {
		got := downloadError(tt.err)
		if !errors.Is(got, tt.err) {
			t.Errorf("%s: %v doesn't wrap the download's error", tt.name, got)
		}
		for _, sentinel := range []error{source.ErrUnauthorized, source.ErrNotFound, source.ErrRateLimited} {
			if errors.Is(got, sentinel) != errors.Is(tt.want, sentinel) {
				t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
			}
		}
	}
	if downloadError(nil) != nil {
		t.Error("downloadError(nil) isn't nil")
	}
}
