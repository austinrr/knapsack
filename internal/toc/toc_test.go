package toc_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/austinrr/knapsack/internal/toc"
)

// want is the subset of a File a test checks: list fields plus selected
// headers.
type want struct {
	Interfaces []int
	Allow      []string
	Exclude    []string
	Headers    map[string]string
}

func check(t *testing.T, f toc.File, w want) {
	t.Helper()
	got := want{
		Interfaces: f.Interfaces,
		Allow:      f.AllowLoadGameTypes,
		Exclude:    f.ExcludeLoadGameTypes,
		Headers:    map[string]string{},
	}
	for k := range w.Headers {
		got.Headers[k] = f.Get(k)
	}
	if diff := cmp.Diff(w, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("parsed TOC mismatch (-want +got):\n%s", diff)
	}
}

// Real TOCs from installed addons (headers only), with their CRLF line
// endings kept byte-exact by testdata/.gitattributes.
func TestParseRealWorld(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file string
		want want
	}{
		{"Baganator.toc", want{
			// A multi-Interface TOC: one file for every client.
			Interfaces: []int{120100, 120105, 16001, 50504, 38001, 20506, 11509},
			Exclude:    []string{"wowhack"},
			Headers: map[string]string{
				"Title":        "Baganator",
				"Version":      "853",
				"OptionalDeps": "Syndicator, Masque",
				"IconTexture":  `Interface\AddOns\Baganator\Assets\logo.tga`,
			},
		}},
		{"DialogueUI.toc", want{
			Interfaces: []int{120107, 120105, 50504, 20506, 11509, 16001},
			Headers: map[string]string{
				"Version":    "1.0.7 c",
				"Notes-zhTW": "沉浸式任務與 NPC 對話介面，支援快捷鍵、搖桿與自訂外觀",
				// Repeated keys: the last line wins. Brackets in an X- value
				// are just text.
				"X-GameType": "Mists [AllowLoadGameType mists]",
			},
		}},
		{"Leatrix_Maps.toc", want{
			Interfaces: []int{16001},
			Headers: map[string]string{
				"Title":                "Leatrix Maps",
				"AddonCompartmentFunc": "LeaMapsGlobalMiniBtnClickFunc", // no space after the colon
				"X-Curse-Project-ID":   "298842",
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			r, err := os.Open(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			f, err := toc.Parse(r)
			if err != nil {
				t.Fatal(err)
			}
			check(t, f, tt.want)
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want want
	}{
		{
			name: "bom and crlf",
			in:   "\xef\xbb\xbf## Interface: 16001\r\n## Title: Foo\r\n",
			want: want{Interfaces: []int{16001}, Headers: map[string]string{"Title": "Foo"}},
		},
		{
			name: "bom only at the start",
			in:   "## Title: Foo\n\xef\xbb\xbf## Notes: bar\n",
			want: want{Headers: map[string]string{"Title": "Foo", "Notes": ""}},
		},
		{
			name: "game type lines append",
			in: "## AllowLoadGameType: mainline, Classic\n" +
				"## ExcludeLoadGameType: wowhack\n" +
				"## AllowLoadGameType: Forever\n" +
				"## ExcludeLoadGameType: vanilla tbc\n",
			want: want{
				Allow:   []string{"mainline", "classic", "forever"},
				Exclude: []string{"wowhack", "vanilla", "tbc"},
			},
		},
		{
			name: "interface separators and junk",
			in:   "## Interface: 120105,16001  50504, abc, -5, 0, 11509\n",
			want: want{Interfaces: []int{120105, 16001, 50504, 11509}},
		},
		{
			name: "later interface line replaces",
			in:   "## Interface: 11509\n## Interface: 16001, 120105\n",
			want: want{Interfaces: []int{16001, 120105}},
		},
		{
			name: "keys ignore case and spacing",
			in:   "##title:Foo\n##   VERSION   :   1.2  \n",
			want: want{Headers: map[string]string{"Title": "Foo", "version": "1.2"}},
		},
		{
			name: "value keeps later colons",
			in:   "## Notes: see: https://example.com\n",
			want: want{Headers: map[string]string{"Notes": "see: https://example.com"}},
		},
		{
			name: "non-header lines are skipped",
			in: "# ## Title: commented out\n" +
				"#Title: also a comment\n" +
				"## no colon here\n" +
				"##: no key\n" +
				"Core\\Init.lua\n" +
				"\n" +
				"## Title: Real\n",
			want: want{Headers: map[string]string{"Title": "Real"}},
		},
		{
			name: "file names after headers are ignored",
			in:   "## Title: Foo\nFoo.lua\n## Version: 2\n",
			want: want{Headers: map[string]string{"Title": "Foo", "Version": "2"}},
		},
		{
			name: "empty",
			in:   "",
			want: want{Headers: map[string]string{"Title": ""}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := toc.Parse(strings.NewReader(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			check(t, f, tt.want)
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("disk on fire")
	if _, err := toc.Parse(iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Errorf("read error: got %v, want it to wrap %v", err, boom)
	}
	huge := "## Notes: " + strings.Repeat("x", 2<<20) + "\n"
	if _, err := toc.Parse(strings.NewReader(huge)); err == nil {
		t.Error("an overlong line parsed without error")
	}
}

// The zero File is usable: Get on it returns "".
func TestZeroFile(t *testing.T) {
	t.Parallel()
	var f toc.File
	if got := f.Get("Title"); got != "" {
		t.Errorf("zero File Get = %q", got)
	}
}

// FuzzParse checks that Parse never panics and that what it returns keeps
// its invariants, whatever the input.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"Baganator.toc", "DialogueUI.toc", "Leatrix_Maps.toc"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(data))
	}
	f.Add("\xef\xbb\xbf## Interface: 1, x, 2\r\n## AllowLoadGameType: A B\r\n")
	f.Add("##:\n##\n#\n")
	f.Fuzz(func(t *testing.T, s string) {
		file, err := toc.Parse(strings.NewReader(s))
		if err != nil {
			return
		}
		for _, n := range file.Interfaces {
			if n <= 0 {
				t.Fatalf("Interfaces holds %d", n)
			}
		}
		for _, list := range [][]string{file.AllowLoadGameTypes, file.ExcludeLoadGameTypes} {
			for _, gt := range list {
				if gt == "" || gt != strings.ToLower(gt) || strings.ContainsAny(gt, ", \t") {
					t.Fatalf("game type %q isn't a lower-cased single word", gt)
				}
			}
		}
	})
}
