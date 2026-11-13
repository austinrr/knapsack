package ref_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/austinrr/knapsack/internal/ref"
)

func TestParse(t *testing.T) {
	t.Parallel()
	gh := func(id string) ref.Ref { return ref.Ref{Source: ref.GitHub, ID: id} }
	cf := func(id string) ref.Ref { return ref.Ref{Source: ref.CurseForge, ID: id} }
	wago := func(id string) ref.Ref { return ref.Ref{Source: ref.Wago, ID: id} }

	tests := []struct {
		in   string
		want ref.Ref
	}{
		// GitHub refs.
		{"github:austinrr/baganator-mirror", gh("austinrr/baganator-mirror")},
		{"github:AustinRR/Baganator-Mirror", gh("austinrr/baganator-mirror")},
		{"GitHub:austinrr/baganator-mirror", gh("austinrr/baganator-mirror")},
		{"github:owner/repo.git", gh("owner/repo")},
		{"github:owner/repo/", gh("owner/repo")},
		{"github:owner/my.addon_v2", gh("owner/my.addon_v2")},
		{"github:a/b", gh("a/b")},
		{"  github:owner/repo\n", gh("owner/repo")},

		// GitHub URLs.
		{"https://github.com/austinrr/baganator-mirror", gh("austinrr/baganator-mirror")},
		{"http://github.com/Owner/Repo", gh("owner/repo")},
		{"https://www.github.com/owner/repo", gh("owner/repo")},
		{"https://GitHub.com/owner/repo", gh("owner/repo")},
		{"https://github.com/owner/repo.git", gh("owner/repo")},
		{"https://github.com/owner/repo/", gh("owner/repo")},
		{"https://github.com/owner/repo/releases/tag/v1.2.3", gh("owner/repo")},
		{"https://github.com/owner/repo/releases/latest", gh("owner/repo")},
		{"https://github.com/owner/repo?tab=readme#install", gh("owner/repo")},
		{"github.com/owner/repo", gh("owner/repo")},
		{"www.github.com/owner/repo", gh("owner/repo")},

		// CurseForge refs.
		{"curseforge:12345", cf("12345")},
		{"curseforge:0012345", cf("12345")},
		{"curseforge:deadly-boss-mods", cf("deadly-boss-mods")},
		{"curseforge:Deadly-Boss-Mods", cf("deadly-boss-mods")},
		{"curseforge:my_addon", cf("my_addon")},

		// CurseForge URLs.
		{"https://www.curseforge.com/wow/addons/deadly-boss-mods", cf("deadly-boss-mods")},
		{"https://www.curseforge.com/wow/addons/deadly-boss-mods/files/all", cf("deadly-boss-mods")},
		{"https://curseforge.com/wow/addons/details", cf("details")},
		{"https://legacy.curseforge.com/wow/addons/details", cf("details")},
		{"https://wow.curseforge.com/projects/details", cf("details")},
		{"www.curseforge.com/wow/addons/details", cf("details")},

		// Wago refs keep their case.
		{"wago:VBNBxKx5", wago("VBNBxKx5")},
		{"WAGO:VBNBxKx5", wago("VBNBxKx5")},
		{"wago:baganator", wago("baganator")},

		// Wago URLs.
		{"https://addons.wago.io/addons/baganator", wago("baganator")},
		{"https://addons.wago.io/addons/baganator/versions?stability=stable", wago("baganator")},
		{"addons.wago.io/addons/VBNBxKx5", wago("VBNBxKx5")},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := ref.Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		wantErr string // a substring of the error
	}{
		// Not a ref at all.
		{"owner/repo", "want <source>:<id> or a URL"},
		{"baganator", "want <source>:<id> or a URL"},
		{"gitlab:owner/repo", `unknown source "gitlab"`},
		{":owner/repo", `unknown source ""`},

		// GitHub.
		{"github:", "github id must be owner/repo"},
		{"github:owner", "github id must be owner/repo"},
		{"github:owner/repo/extra", "github id must be owner/repo"},
		{"github:/repo", `invalid github owner ""`},
		{"github:owner/", "github id must be owner/repo"},
		{"github:-owner/repo", `invalid github owner "-owner"`},
		{"github:owner-/repo", `invalid github owner "owner-"`},
		{"github:own--er/repo", `invalid github owner "own--er"`},
		{"github:" + strings.Repeat("a", 40) + "/repo", "invalid github owner"},
		{"github:own_er/repo", `invalid github owner "own_er"`},
		{"github:owner/re po", `invalid github repository "re po"`},
		{"github:owner/..", `invalid github repository ".."`},
		{"github:owner/.", `invalid github repository "."`},
		{"github:owner/repo.git.git", `invalid github repository "repo.git"`},

		// CurseForge.
		{"curseforge:", "invalid curseforge id or slug"},
		{"curseforge:0", "invalid curseforge project id"},
		{"curseforge:99999999999", "invalid curseforge project id"},
		{"curseforge:bad slug", "invalid curseforge id or slug"},
		{"curseforge:a/b", "invalid curseforge id or slug"},

		// Wago.
		{"wago:", "invalid wago id"},
		{"wago:bad id", "invalid wago id"},
		{"wago:a/b", "invalid wago id"},

		// URLs.
		{"https://gitlab.com/owner/repo", `unsupported host "gitlab.com"`},
		{"https://example.com", `unsupported host "example.com"`},
		{"https://github.com/", "not a link to a github addon"},
		{"https://github.com/owner", "not a link to a github addon"},
		{"https://github.com/-bad/repo", `invalid github owner "-bad"`},
		{"https://www.curseforge.com/wow", "not a link to a curseforge addon"},
		{"https://www.curseforge.com/minecraft/mc-mods/jei", "not a link to a curseforge addon"},
		{"https://addons.wago.io/", "not a link to a wago addon"},
		{"https://addons.wago.io/users/someone", "not a link to a wago addon"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := ref.Parse(tt.in)
			if err == nil {
				t.Fatalf("Parse(%q) = %#v, want error containing %q", tt.in, got, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse(%q) error = %q, want it to contain %q", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestParseEmpty(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "   ", "\t\n"} {
		if _, err := ref.Parse(in); !errors.Is(err, ref.ErrEmpty) {
			t.Errorf("Parse(%q) error = %v, want ErrEmpty", in, err)
		}
	}
}

func TestStringAndShortName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ref       ref.Ref
		wantStr   string
		wantShort string
	}{
		{ref.Ref{Source: ref.GitHub, ID: "austinrr/baganator-mirror"}, "github:austinrr/baganator-mirror", "baganator-mirror"},
		{ref.Ref{Source: ref.CurseForge, ID: "12345"}, "curseforge:12345", "12345"},
		{ref.Ref{Source: ref.CurseForge, ID: "deadly-boss-mods"}, "curseforge:deadly-boss-mods", "deadly-boss-mods"},
		{ref.Ref{Source: ref.Wago, ID: "VBNBxKx5"}, "wago:VBNBxKx5", "VBNBxKx5"},
	}
	for _, tt := range tests {
		if got := tt.ref.String(); got != tt.wantStr {
			t.Errorf("%#v.String() = %q, want %q", tt.ref, got, tt.wantStr)
		}
		if got := tt.ref.ShortName(); got != tt.wantShort {
			t.Errorf("%#v.ShortName() = %q, want %q", tt.ref, got, tt.wantShort)
		}
	}
}

// FuzzParse checks that whatever Parse accepts is already normalized:
// parsing its String() gives back the same Ref.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"github:austinrr/baganator-mirror",
		"https://github.com/Owner/Repo.git/releases",
		"github.com/owner/repo",
		"curseforge:0012345",
		"https://www.curseforge.com/wow/addons/deadly-boss-mods/files",
		"wago:VBNBxKx5",
		"https://addons.wago.io/addons/baganator",
		"owner/repo",
		"https://example.com",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ref.Parse(s)
		if err != nil {
			return
		}
		again, err := ref.Parse(r.String())
		if err != nil {
			t.Fatalf("Parse(%q) = %#v, but Parse(%q) failed: %v", s, r, r.String(), err)
		}
		if again != r {
			t.Fatalf("Parse(%q) = %#v, but Parse(%q) = %#v", s, r, r.String(), again)
		}
		if r.ShortName() == "" {
			t.Fatalf("Parse(%q) = %#v has an empty ShortName", s, r)
		}
	})
}
