package flavor_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/austinrr/knapsack/internal/flavor"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    flavor.Flavor
		wantErr bool
	}{
		{in: "retail", want: flavor.Retail},
		{in: "classic", want: flavor.Classic},
		{in: "classic_era", want: flavor.ClassicEra},
		{in: "anniversary", want: flavor.Anniversary},
		{in: "forever", want: flavor.Forever},
		// Config values are exact: no case folding, trimming or folder names.
		{in: "Retail", wantErr: true},
		{in: " retail", wantErr: true},
		{in: "_retail_", wantErr: true},
		{in: "classic_beta", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := flavor.Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %q, want error", tt.in, got)
				}
				if !strings.Contains(err.Error(), "retail, classic, classic_era, anniversary, forever") {
					t.Errorf("error %q should list the valid flavors", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Every flavor parses from its own name, so All and Parse can't drift apart.
func TestAllRoundTrip(t *testing.T) {
	t.Parallel()
	want := []flavor.Flavor{flavor.Retail, flavor.Classic, flavor.ClassicEra, flavor.Anniversary, flavor.Forever}
	all := flavor.All()
	if len(all) != len(want) {
		t.Fatalf("All() = %v, want %v", all, want)
	}
	for i, f := range all {
		if f != want[i] {
			t.Errorf("All()[%d] = %q, want %q", i, f, want[i])
		}
		if got, err := flavor.Parse(string(f)); err != nil || got != f {
			t.Errorf("Parse(%q) = %q, %v", f, got, err)
		}
	}
}

func TestClient(t *testing.T) {
	t.Parallel()
	tests := []struct {
		flavor flavor.Flavor
		want   flavor.Client
	}{
		{flavor.Retail, flavor.Client{
			Folder: "_retail_", GameType: "standard", Family: flavor.FamilyMainline,
			Interface: flavor.Range{Min: 100000}, Product: "wow",
		}},
		{flavor.Classic, flavor.Client{
			Folder: "_classic_", GameType: "mists", Family: flavor.FamilyClassic,
			Interface: flavor.Range{Min: 50000, Max: 59999}, Product: "wow_classic",
		}},
		{flavor.ClassicEra, flavor.Client{
			Folder: "_classic_era_", GameType: "vanilla", Family: flavor.FamilyClassic,
			Interface: flavor.Range{Min: 11000, Max: 11999}, Product: "wow_classic_era",
		}},
		{flavor.Anniversary, flavor.Client{
			Folder: "_anniversary_", GameType: "tbc", Family: flavor.FamilyClassic,
			Interface: flavor.Range{Min: 20000, Max: 29999}, Product: "",
		}},
		{flavor.Forever, flavor.Client{
			Folder: "_forever_", GameType: "forever", Aliases: []string{"camelot"},
			Family: flavor.FamilyMainline, Interface: flavor.Range{Min: 16000, Max: 16999},
			Product: "wowf",
		}},
	}
	for _, tt := range tests {
		t.Run(string(tt.flavor), func(t *testing.T) {
			t.Parallel()
			got := tt.flavor.Client()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Client() =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

func TestClientUnknown(t *testing.T) {
	t.Parallel()
	if got := flavor.Flavor("bogus").Client(); got.Folder != "" {
		t.Errorf("unknown flavor Client() = %+v, want zero Client", got)
	}
}

// Client returns a copy, so a caller changing Aliases can't change the table.
func TestClientReturnsCopy(t *testing.T) {
	t.Parallel()
	c := flavor.Forever.Client()
	c.Aliases[0] = "changed"
	if got := flavor.Forever.Client().Aliases[0]; got != "camelot" {
		t.Errorf("table modified through Client(): alias = %q", got)
	}
}

// Each game's own Interface number falls in its range and in no other, so
// the ranges never overlap.
func TestInterfaceRanges(t *testing.T) {
	t.Parallel()
	samples := map[flavor.Flavor]int{
		flavor.Retail:      120105,
		flavor.Classic:     50504,
		flavor.ClassicEra:  11509,
		flavor.Anniversary: 20505,
		flavor.Forever:     16001,
	}
	for _, f := range flavor.All() {
		for g, n := range samples {
			got := f.Client().Interface.Contains(n)
			if want := f == g; got != want {
				t.Errorf("%s range %s Contains(%d) = %v, want %v", f, f.Client().Interface, n, got, want)
			}
		}
	}
}

func TestRange(t *testing.T) {
	t.Parallel()
	bounded := flavor.Range{Min: 16000, Max: 16999}
	open := flavor.Range{Min: 100000}
	tests := []struct {
		r    flavor.Range
		n    int
		want bool
	}{
		{bounded, 15999, false},
		{bounded, 16000, true},
		{bounded, 16999, true},
		{bounded, 17000, false},
		{open, 99999, false},
		{open, 100000, true},
		{open, 999999, true},
	}
	for _, tt := range tests {
		if got := tt.r.Contains(tt.n); got != tt.want {
			t.Errorf("%s Contains(%d) = %v, want %v", tt.r, tt.n, got, tt.want)
		}
	}
	if got := bounded.String(); got != "16000–16999" {
		t.Errorf("String() = %q", got)
	}
	if got := open.String(); got != "100000+" {
		t.Errorf("String() = %q", got)
	}
}

func TestFromPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		path   string
		want   flavor.Flavor
		wantOK bool
	}{
		// Every client folder, Unix paths.
		{"retail unix", "/games/World of Warcraft/_retail_/Interface/AddOns", flavor.Retail, true},
		{"classic unix", "/games/World of Warcraft/_classic_/Interface/AddOns", flavor.Classic, true},
		{"classic_era unix", "/games/World of Warcraft/_classic_era_/Interface/AddOns", flavor.ClassicEra, true},
		{"anniversary unix", "/games/World of Warcraft/_anniversary_/Interface/AddOns", flavor.Anniversary, true},
		{"forever unix", "/home/u/Faugus/battlenet/drive_c/Program Files (x86)/World of Warcraft/_forever_/Interface/AddOns", flavor.Forever, true},

		// Every client folder, Windows paths.
		{"retail windows", `C:\Program Files (x86)\World of Warcraft\_retail_\Interface\AddOns`, flavor.Retail, true},
		{"classic windows", `C:\Program Files (x86)\World of Warcraft\_classic_\Interface\AddOns`, flavor.Classic, true},
		{"classic_era windows", `C:\Program Files (x86)\World of Warcraft\_classic_era_\Interface\AddOns`, flavor.ClassicEra, true},
		{"anniversary windows", `C:\Program Files (x86)\World of Warcraft\_anniversary_\Interface\AddOns`, flavor.Anniversary, true},
		{"forever windows", `D:\Games\World of Warcraft\_forever_\Interface\AddOns`, flavor.Forever, true},

		// Shapes of path that still resolve.
		{"trailing separator", "/wow/_retail_/Interface/AddOns/", flavor.Retail, true},
		{"mixed separators", `C:\wow/_forever_\Interface/AddOns`, flavor.Forever, true},
		{"windows case", `C:\WoW\_Classic_Era_\Interface\AddOns`, flavor.ClassicEra, true},
		{"unc path", `\\nas\games\wow\_classic_\Interface\AddOns`, flavor.Classic, true},
		{"client folder itself", "/wow/_retail_", flavor.Retail, true},
		{"nearest folder wins", "/backup/_retail_/wow/_forever_/Interface/AddOns", flavor.Forever, true},

		// No client folder.
		{"no client folder", "/home/u/addons", "", false},
		{"empty", "", "", false},
		{"substring only", "/wow/my_retail_backup/Interface/AddOns", "", false},
		{"retired beta", "/wow/_classic_beta_/Interface/AddOns", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := flavor.FromPath(tt.path)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("FromPath(%q) = %q, %v; want %q, %v", tt.path, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
