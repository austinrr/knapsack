package flavor

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Flavor identifies a WoW client. Names match the client folder: _retail_ is
// Retail, _classic_era_ is ClassicEra, and so on.
type Flavor string

// The flavors Knapsack supports.
const (
	Retail      Flavor = "retail"      // _retail_
	Classic     Flavor = "classic"     // _classic_ (currently Mists)
	ClassicEra  Flavor = "classic_era" // _classic_era_
	Anniversary Flavor = "anniversary" // _anniversary_ (TBC Anniversary)
	Forever     Flavor = "forever"     // _forever_
)

// Family groups game types the way TOC suffixes do: _Mainline or _Classic.
type Family string

// The two families the client recognizes.
const (
	FamilyMainline Family = "mainline"
	FamilyClassic  Family = "classic"
)

// Client is one row of the client data table.
type Client struct {
	// Folder is the client folder under the WoW install, e.g. "_forever_".
	Folder string
	// GameType is the client's own name for what it runs, used in TOC
	// suffixes and AllowLoadGameType, e.g. "forever".
	GameType string
	// Aliases are other names the client accepts for GameType, in TOC
	// suffixes and load rules: "camelot", Forever's internal name, which
	// addons such as Details use (Details_Camelot.toc).
	Aliases []string
	// Family is the TOC family the game type belongs to.
	Family Family
	// Interface is the range of ## Interface values built for this game.
	Interface Range
	// Product is the product code in .build.info, or "" if not yet known,
	// in which case the client's exact Interface number can't be read.
	Product string
	// Tests are the client's test versions, such as the PTR. Each has its
	// own client folder and AddOns, and runs the same flavor of addons, so
	// it's the same flavor here.
	Tests []TestClient
}

// TestClient is a test version of a client: a PTR or beta.
type TestClient struct {
	// Name is what it's called, e.g. "ptr", for a default profile name:
	// retail-ptr.
	Name string
	// Folder is its client folder, e.g. "_ptr_".
	Folder string
	// Product is its product code in .build.info, as Client.Product.
	Product string
}

// GameTypes returns the game type, then its aliases: every name the client
// answers to in TOC suffixes and load rules.
func (c Client) GameTypes() []string {
	return append([]string{c.GameType}, c.Aliases...)
}

// Range is an inclusive range of Interface numbers. Use NoMax for a range
// with no upper bound.
type Range struct {
	Min, Max int
}

// NoMax is the Max of a range with no upper bound. Making it explicit keeps
// the zero Range from accepting real Interface numbers: it holds only 0.
const NoMax = math.MaxInt

// Contains reports whether n is in the range.
func (r Range) Contains(n int) bool {
	return n >= r.Min && n <= r.Max
}

// String formats the range for messages: "16000–16999", or "100000+" when
// there is no upper bound.
func (r Range) String() string {
	if r.Max == NoMax {
		return fmt.Sprintf("%d+", r.Min)
	}
	return fmt.Sprintf("%d–%d", r.Min, r.Max)
}

// table is the client data table (DESIGN.md §6.4), in display order.
var table = []struct {
	flavor Flavor
	client Client
	// markers are the suffixes release assets use for this client, as in
	// Addon-1.0-forever.zip. "classic" means Classic Era, as in the BigWigs
	// packager's file names, not the _classic_ client.
	markers []string
}{
	{Retail, Client{
		Folder:    "_retail_",
		GameType:  "standard",
		Family:    FamilyMainline,
		Interface: Range{Min: 100000, Max: NoMax},
		Product:   "wow",
		// The test clients' product codes come from WowUp, unconfirmed
		// (DESIGN.md §13), as are the folders of the two below.
		Tests: []TestClient{{"ptr", "_ptr_", "wowt"}, {"xptr", "_xptr_", "wowxptr"}, {"beta", "_beta_", "wow_beta"}},
	}, []string{"mainline", "retail"}},
	{Classic, Client{
		Folder:    "_classic_",
		GameType:  "mists",
		Family:    FamilyClassic,
		Interface: Range{Min: 50000, Max: 59999},
		Product:   "wow_classic",
		// _classic_beta_ has also held betas of other Classic lines, so
		// it's only a default: --flavor can say otherwise (Locate).
		Tests: []TestClient{{"ptr", "_classic_ptr_", "wow_classic_ptr"}, {"beta", "_classic_beta_", "wow_classic_beta"}},
	}, []string{"mists", "mop"}},
	{ClassicEra, Client{
		Folder:    "_classic_era_",
		GameType:  "vanilla",
		Family:    FamilyClassic,
		Interface: Range{Min: 11000, Max: 11999},
		Product:   "wow_classic_era",
		Tests:     []TestClient{{"ptr", "_classic_era_ptr_", "wow_classic_era_ptr"}},
	}, []string{"vanilla", "classic", "era", "classic_era"}},
	{Anniversary, Client{
		Folder:    "_anniversary_",
		GameType:  "tbc",
		Family:    FamilyClassic,
		Interface: Range{Min: 20000, Max: 29999},
		Product:   "", // unconfirmed, see DESIGN.md §13
	}, []string{"tbc", "bcc"}},
	{Forever, Client{
		Folder:    "_forever_",
		GameType:  "forever",
		Aliases:   []string{"camelot"},
		Family:    FamilyMainline,
		Interface: Range{Min: 16000, Max: 16999},
		Product:   "wowf",
	}, []string{"forever"}},
}

// gameTypes are the names the client recognizes in AllowLoadGameType and
// ExcludeLoadGameType: every game type, including those Knapsack has no
// flavor for, and both families. From warcraft.wiki.gg/wiki/TOC_format.
var gameTypes = map[string]bool{
	"standard": true, "forever": true, "camelot": true, "plunderstorm": true, "wowhack": true,
	"vanilla": true, "tbc": true, "wrath": true, "cata": true, "mists": true,
	string(FamilyMainline): true, string(FamilyClassic): true,
}

// KnownGameType reports whether the client recognizes name, lower-cased, as
// a game type or family. The client ignores names it doesn't recognize in
// AllowLoadGameType, so validation needs to tell them apart.
func KnownGameType(name string) bool {
	return gameTypes[name]
}

// otherMarkers are asset suffixes for game types Knapsack has no flavor
// for. An asset with one is for some other client, so it never counts as
// unmarked.
// Season of Discovery and Hardcore run on the Classic Era client but get
// their own builds, so they're here too.
var otherMarkers = []string{"wrath", "wotlk", "wotlkc", "cata", "cataclysm", "sod", "hardcore"}

// AssetMarkers returns the suffixes that mark a release asset as built for
// this flavor, such as "forever" in Addon-1.0-forever.zip.
func (f Flavor) AssetMarkers() []string {
	for _, row := range table {
		if row.flavor == f {
			return slices.Clone(row.markers)
		}
	}
	return nil
}

// AllAssetMarkers returns every known asset suffix, for every flavor and
// for game types Knapsack doesn't support, so an asset without one can be
// recognized as built for any client.
func AllAssetMarkers() []string {
	all := slices.Clone(otherMarkers)
	for _, row := range table {
		all = append(all, row.markers...)
	}
	return all
}

// All returns every supported flavor in display order.
func All() []Flavor {
	all := make([]Flavor, len(table))
	for i, row := range table {
		all[i] = row.flavor
	}
	return all
}

// Names returns every flavor's name in display order, for messages and
// shell completion.
func Names() []string {
	names := make([]string, len(table))
	for i, row := range table {
		names[i] = string(row.flavor)
	}
	return names
}

// Parse returns the flavor named s. Unknown names are an error that lists
// the valid ones.
func Parse(s string) (Flavor, error) {
	for _, row := range table {
		if string(row.flavor) == s {
			return row.flavor, nil
		}
	}
	return "", fmt.Errorf("unknown flavor %q (want one of %s)", s, strings.Join(Names(), ", "))
}

// Client returns the flavor's row of the client data table. It reports false
// for a Flavor that isn't in the table, so callers can't validate against
// empty data by mistake.
func (f Flavor) Client() (Client, bool) {
	for _, row := range table {
		if row.flavor == f {
			// Returning the struct copies it, but Aliases would still
			// share the table's array, so clone it too.
			c := row.client
			c.Aliases = slices.Clone(c.Aliases)
			c.Tests = slices.Clone(c.Tests)
			return c, true
		}
	}
	return Client{}, false
}

// FromPath detects the flavor from an AddOns directory by finding the client
// folder in it, as Locate does: …/_forever_/Interface/AddOns is Forever,
// and …/_ptr_/Interface/AddOns is Retail.
func FromPath(addonsDir string) (Flavor, bool) {
	l, ok := Locate(addonsDir)
	return l.Flavor, ok
}

// ClientFolders returns every client folder, as the table spells them:
// each flavor's live client, then its test clients, in display order.
func ClientFolders() []string {
	var out []string
	for _, row := range table {
		out = append(out, row.client.Folder)
		for _, t := range row.client.Tests {
			out = append(out, t.Folder)
		}
	}
	return out
}

// IsClientFolder reports whether name is a client folder, live or test,
// matched without regard to case: "_retail_", "_PTR_".
func IsClientFolder(name string) bool {
	_, ok := Locate(name)
	return ok && !strings.ContainsAny(name, "/\\")
}

// Location is the client folder found in an AddOns directory.
type Location struct {
	Flavor Flavor
	// Folder is the client folder as the table spells it: "_ptr_".
	Folder string
	// Product is its .build.info product code, "" if not known.
	Product string
	// Test is the test client's name ("ptr", "beta"), or "" for a live
	// client. A test folder's flavor is a default, not a fact: Blizzard
	// has used some for more than one game, so --flavor may override it.
	Test string
}

// Locate finds the client folder in an AddOns directory: a live client's
// or a test client's, which has its live client's flavor. It understands
// both / and \ separators on every OS, and matches folder names without
// regard to case, as Windows does. When several path elements are client
// folders, the one nearest the end wins.
func Locate(addonsDir string) (Location, bool) {
	parts := strings.FieldsFunc(addonsDir, func(r rune) bool { return r == '/' || r == '\\' })
	for i := len(parts) - 1; i >= 0; i-- {
		for _, row := range table {
			c := row.client
			if strings.EqualFold(parts[i], c.Folder) {
				return Location{Flavor: row.flavor, Folder: c.Folder, Product: c.Product}, true
			}
			for _, t := range c.Tests {
				if strings.EqualFold(parts[i], t.Folder) {
					return Location{Flavor: row.flavor, Folder: t.Folder, Product: t.Product, Test: t.Name}, true
				}
			}
		}
	}
	return Location{}, false
}
