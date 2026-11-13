// Package flavor names the WoW clients Knapsack manages and holds the client
// data table: the one place that knows about specific game versions.
//
// When Blizzard changes what a client folder runs (_classic_ has been
// Cataclysm and is now Mists), only the table in this file changes.
package flavor

import (
	"fmt"
	"math"
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
	// Aliases are other names the client accepts for GameType, e.g.
	// "camelot" for Forever.
	Aliases []string
	// Family is the TOC family the game type belongs to.
	Family Family
	// Interface is the range of ## Interface values built for this game.
	Interface Range
	// Product is the product code in .build.info, or "" if not yet known,
	// in which case the client's exact Interface number can't be read.
	Product string
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
}{
	{Retail, Client{
		Folder:    "_retail_",
		GameType:  "standard",
		Family:    FamilyMainline,
		Interface: Range{Min: 100000, Max: NoMax},
		Product:   "wow",
	}},
	{Classic, Client{
		Folder:    "_classic_",
		GameType:  "mists",
		Family:    FamilyClassic,
		Interface: Range{Min: 50000, Max: 59999},
		Product:   "wow_classic",
	}},
	{ClassicEra, Client{
		Folder:    "_classic_era_",
		GameType:  "vanilla",
		Family:    FamilyClassic,
		Interface: Range{Min: 11000, Max: 11999},
		Product:   "wow_classic_era",
	}},
	{Anniversary, Client{
		Folder:    "_anniversary_",
		GameType:  "tbc",
		Family:    FamilyClassic,
		Interface: Range{Min: 20000, Max: 29999},
		Product:   "", // unconfirmed, see DESIGN.md §13
	}},
	{Forever, Client{
		Folder:    "_forever_",
		GameType:  "forever",
		Aliases:   []string{"camelot"},
		Family:    FamilyMainline,
		Interface: Range{Min: 16000, Max: 16999},
		Product:   "wowf",
	}},
}

// All returns every supported flavor in display order.
func All() []Flavor {
	all := make([]Flavor, len(table))
	for i, row := range table {
		all[i] = row.flavor
	}
	return all
}

// Parse returns the flavor named s. Unknown names are an error that lists
// the valid ones.
func Parse(s string) (Flavor, error) {
	for _, row := range table {
		if string(row.flavor) == s {
			return row.flavor, nil
		}
	}
	var names []string
	for _, f := range All() {
		names = append(names, string(f))
	}
	return "", fmt.Errorf("unknown flavor %q (want one of %s)", s, strings.Join(names, ", "))
}

// Client returns the flavor's row of the client data table. It reports false
// for a Flavor that isn't in the table, so callers can't validate against
// empty data by mistake.
func (f Flavor) Client() (Client, bool) {
	for _, row := range table {
		if row.flavor == f {
			c := row.client
			// Copy the slice so callers can't modify the table.
			c.Aliases = append([]string(nil), c.Aliases...)
			return c, true
		}
	}
	return Client{}, false
}

// FromPath detects the flavor from an AddOns directory by finding the client
// folder in it: …/_forever_/Interface/AddOns is Forever. It understands both
// / and \ separators on every OS, and matches folder names without regard to
// case, as Windows does. When several path elements are client folders, the
// one nearest the end wins.
func FromPath(addonsDir string) (Flavor, bool) {
	parts := strings.FieldsFunc(addonsDir, func(r rune) bool { return r == '/' || r == '\\' })
	for i := len(parts) - 1; i >= 0; i-- {
		for _, row := range table {
			if strings.EqualFold(parts[i], row.client.Folder) {
				return row.flavor, true
			}
		}
	}
	return "", false
}
