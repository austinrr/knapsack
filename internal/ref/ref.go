// Package ref parses and normalizes addon refs: the `source:id` names that
// identify an addon in config, such as github:austinrr/baganator-mirror.
//
// Parse also accepts each source's web URLs and turns them into a ref, so an
// addon has exactly one spelling no matter how the user typed it.
package ref

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// The sources a ref can name.
const (
	GitHub     = "github"
	CurseForge = "curseforge"
	Wago       = "wago"
)

// Ref identifies one addon at one source. Construct it with Parse, which
// normalizes ID so equal addons compare equal with ==.
type Ref struct {
	Source string // GitHub, CurseForge or Wago
	ID     string // "owner/repo", "12345" or a slug, "VBNBxKx5"; normalized
}

// String returns the ref in `source:id` form, which Parse accepts.
func (r Ref) String() string {
	return r.Source + ":" + r.ID
}

// ShortName is the last element of the ID: the repo name for GitHub
// ("baganator-mirror"), and the ID itself for the other sources. Commands
// that act on installed addons accept it in place of the full ref.
func (r Ref) ShortName() string {
	if i := strings.LastIndexByte(r.ID, '/'); i >= 0 {
		return r.ID[i+1:]
	}
	return r.ID
}

// ErrEmpty is returned by Parse for an empty or all-whitespace string.
var ErrEmpty = errors.New("empty addon ref")

// Parse reads a ref in `source:id` form or a GitHub, CurseForge or Wago URL,
// and returns it normalized. Surrounding whitespace is ignored.
func Parse(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, ErrEmpty
	}
	if host, path, ok := splitURL(s); ok {
		return parseURL(s, host, path)
	}

	source, id, ok := strings.Cut(s, ":")
	if !ok {
		return Ref{}, fmt.Errorf("invalid addon ref %q: want <source>:<id> or a URL, e.g. github:owner/repo", s)
	}
	return normalize(strings.ToLower(source), id, s)
}

// normalize validates id for source and returns the canonical Ref. input is
// the original text, for error messages.
func normalize(source, id, input string) (Ref, error) {
	var (
		norm string
		err  error
	)
	switch source {
	case GitHub:
		norm, err = normalizeGitHub(id)
	case CurseForge:
		norm, err = normalizeCurseForge(id)
	case Wago:
		norm, err = normalizeWago(id)
	default:
		return Ref{}, fmt.Errorf("invalid addon ref %q: unknown source %q (want github, curseforge or wago)", input, source)
	}
	if err != nil {
		return Ref{}, fmt.Errorf("invalid addon ref %q: %w", input, err)
	}
	return Ref{Source: source, ID: norm}, nil
}

var (
	// GitHub user and org names: letters, digits and single inner hyphens,
	// at most 39 characters.
	githubOwner = regexp.MustCompile(`^[a-z0-9](?:-?[a-z0-9]){0,38}$`)
	// GitHub repository names: letters, digits, '.', '-' and '_'.
	githubRepo = regexp.MustCompile(`^[a-z0-9._-]{1,100}$`)
	// CurseForge slugs, e.g. "deadly-boss-mods".
	curseSlug = regexp.MustCompile(`^[a-z0-9_-]{1,100}$`)
	// Wago IDs and slugs. IDs are case-sensitive ("VBNBxKx5").
	wagoID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
)

// normalizeGitHub lower-cases owner/repo, because GitHub treats it
// case-insensitively, and drops a trailing ".git" or "/".
func normalizeGitHub(id string) (string, error) {
	id = strings.ToLower(strings.TrimSuffix(id, "/"))
	id = strings.TrimSuffix(id, ".git")
	owner, repo, ok := strings.Cut(id, "/")
	if !ok || strings.Contains(repo, "/") {
		return "", errors.New("github id must be owner/repo")
	}
	if !githubOwner.MatchString(owner) {
		return "", fmt.Errorf("invalid github owner %q", owner)
	}
	// GitHub doesn't allow a repo name ending in ".git", so a second suffix
	// left after trimming one is an error rather than something to strip.
	if !githubRepo.MatchString(repo) || repo == "." || repo == ".." || strings.HasSuffix(repo, ".git") {
		return "", fmt.Errorf("invalid github repository %q", repo)
	}
	return owner + "/" + repo, nil
}

// normalizeCurseForge accepts a numeric project ID, without leading zeros so
// each project has one spelling, or a lower-cased slug.
func normalizeCurseForge(id string) (string, error) {
	if id != "" && strings.Trim(id, "0123456789") == "" {
		n, err := strconv.ParseUint(id, 10, 32)
		if err != nil || n == 0 {
			return "", fmt.Errorf("invalid curseforge project id %q", id)
		}
		return strconv.FormatUint(n, 10), nil
	}
	id = strings.ToLower(id)
	if !curseSlug.MatchString(id) {
		return "", fmt.Errorf("invalid curseforge id or slug %q", id)
	}
	return id, nil
}

// normalizeWago keeps the ID's case, because Wago IDs are case-sensitive.
func normalizeWago(id string) (string, error) {
	if !wagoID.MatchString(id) {
		return "", fmt.Errorf("invalid wago id %q", id)
	}
	return id, nil
}

// splitURL reports whether s looks like a URL, with or without a scheme
// ("https://github.com/o/r" or "github.com/o/r"), and returns its lower-cased
// host (without "www.") and its path.
func splitURL(s string) (host, path string, ok bool) {
	raw := s
	if !strings.Contains(s, "://") {
		// Only treat a scheme-less string as a URL when it starts with a
		// known host, so "github:owner/repo" stays a ref.
		first, _, _ := strings.Cut(s, "/")
		if _, known := urlHosts[strings.TrimPrefix(strings.ToLower(first), "www.")]; !known {
			return "", "", false
		}
		raw = "https://" + s
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."), u.Path, true
}

// urlHosts maps each known host to the source whose URLs it serves.
var urlHosts = map[string]string{
	"github.com":            GitHub,
	"curseforge.com":        CurseForge,
	"wow.curseforge.com":    CurseForge,
	"legacy.curseforge.com": CurseForge,
	"addons.wago.io":        Wago,
}

// parseURL turns a known source URL into a Ref. Anything after the addon's
// own path (releases, files, tabs, query strings) is ignored.
func parseURL(input, host, path string) (Ref, error) {
	source, ok := urlHosts[host]
	if !ok {
		return Ref{}, fmt.Errorf("invalid addon URL %q: unsupported host %q (want github.com, curseforge.com or addons.wago.io)", input, host)
	}
	segs := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })

	var id string
	switch source {
	case GitHub:
		// github.com/<owner>/<repo>[/...]
		if len(segs) >= 2 {
			id = segs[0] + "/" + segs[1]
		}
	case CurseForge:
		// curseforge.com/wow/addons/<slug>[/...]
		// wow.curseforge.com/projects/<slug>[/...]
		switch {
		case len(segs) >= 3 && segs[0] == "wow" && segs[1] == "addons":
			id = segs[2]
		case len(segs) >= 2 && segs[0] == "projects":
			id = segs[1]
		}
	case Wago:
		// addons.wago.io/addons/<slug>[/...]
		if len(segs) >= 2 && segs[0] == "addons" {
			id = segs[1]
		}
	}
	if id == "" {
		return Ref{}, fmt.Errorf("invalid addon URL %q: not a link to a %s addon", input, source)
	}
	return normalize(source, id, input)
}
