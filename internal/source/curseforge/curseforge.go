// Package curseforge resolves addons published on CurseForge (DESIGN.md
// §7): the newest release file for a flavor, through CurseForge's official
// API, which needs an API key, or from its website, which needs none.
package curseforge

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/source"
)

// gameVersionTypes are CurseForge's IDs for the game each flavor runs, as
// its files are tagged. Classic is the _classic_ client's current line,
// Mists of Pandaria; Anniversary is The Burning Crusade's.
var gameVersionTypes = map[flavor.Flavor]int{
	flavor.Retail:      517,
	flavor.Classic:     79434,
	flavor.ClassicEra:  67408,
	flavor.Anniversary: 73246,
	flavor.Forever:     88568,
}

// wowGameID is World of Warcraft's game ID on CurseForge.
const wowGameID = 1

// releaseFile is a file's releaseType for a release, as opposed to a beta
// (2) or an alpha (3). Knapsack installs only releases, as it installs only
// GitHub's latest release, never a pre-release.
const releaseFile = 1

// Files are listed a page at a time, the most either API gives at once. A
// project's release for a flavor is normally among its newest few files,
// but one that publishes alphas often, or a file per flavor, can have many
// above it.
const (
	filesPerPage = 50
	maxPages     = 4
)

// maxResponse bounds a response: a page of 50 files, or a project's page
// on the website. Both are well under this.
const maxResponse = 4 << 20

// isID reports whether id is a numeric project ID rather than a slug. Refs
// are normalized, so an ID has no leading zeros.
func isID(id string) bool {
	return id != "" && strings.Trim(id, "0123456789") == ""
}

// readJSON decodes resp's body, at most maxResponse bytes, into v.
func readJSON(resp *http.Response, v any) error {
	body, err := source.ReadBody(resp.Body, maxResponse)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
