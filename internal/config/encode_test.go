package config

import (
	"testing"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// FuzzQuote checks that quote always produces a TOML string that decodes
// back to the original, so a path or API key can never corrupt the file.
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
		if _, err := toml.Decode("K = "+quote(s), &got); err != nil {
			t.Fatalf("quote(%q) = %s, which isn't valid TOML: %v", s, quote(s), err)
		}
		if got.K != s {
			t.Fatalf("quote(%q) decoded to %q", s, got.K)
		}
	})
}
