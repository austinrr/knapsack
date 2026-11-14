package tomlenc_test

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/austinrr/knapsack/internal/tomlenc"
)

// FuzzQuote checks that Quote always produces a TOML string that decodes
// back to the original, so a path, key or folder name can never corrupt a file.
func FuzzQuote(f *testing.F) {
	for _, seed := range []string{
		"", "plain", `C:\Program Files (x86)\World of Warcraft`, `"quoted"`,
		"tab\tnew\nline\r", "\x00\x1f\x7f", "ünïcödé ✓", `\u0041`, "'single'",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			t.Skip("TOML strings are UTF-8; config values come from decoded TOML or Go strings built from it")
		}
		var got struct{ K string }
		if _, err := toml.Decode("K = "+tomlenc.Quote(s), &got); err != nil {
			t.Fatalf("Quote(%q) = %s, which isn't valid TOML: %v", s, tomlenc.Quote(s), err)
		}
		if got.K != s {
			t.Fatalf("Quote(%q) decoded to %q", s, got.K)
		}
	})
}

func TestTime(t *testing.T) {
	t.Parallel()
	in := time.Date(2026, 11, 13, 13, 4, 5, 999, time.FixedZone("EST", -5*60*60))
	if got, want := tomlenc.Time(in), "2026-11-13T18:04:05Z"; got != want {
		t.Errorf("Time = %s, want %s", got, want)
	}
}

func TestStrings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   []string
		want string
	}{
		{nil, "[]"},
		{[]string{"Details"}, `["Details"]`},
		{[]string{"A", `B"C`}, `["A", "B\"C"]`},
	}
	for _, tt := range tests {
		if got := tomlenc.Strings(tt.in); got != tt.want {
			t.Errorf("Strings(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
