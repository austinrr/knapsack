package app

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/install"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
)

// explain wraps an error about the addon r from a lower package in a
// UserError with a hint, when there's a known fix. Other errors are
// returned as they are.
func explain(err error, r ref.Ref) error {
	var ue *UserError
	if err == nil || errors.As(err, &ue) {
		return err
	}
	hint := ""
	switch {
	case errors.Is(err, install.ErrConflict):
		hint = "the folders were installed some other way; rerun with --force to replace them"
	case errors.Is(err, install.ErrOwned):
		hint = "another addon in this profile installs the same folders; remove it first"
	case errors.Is(err, install.ErrInUse):
		hint = "close World of Warcraft and retry"
	case errors.Is(err, source.ErrNotFound) && r.Source == ref.Wago:
		hint = "check the ref: use the addon's Wago ID, or its addons.wago.io link"
	case errors.Is(err, source.ErrNotFound) && r.Source == ref.CurseForge:
		hint = "check the ref: use the project's ID or slug, or its curseforge.com link"
	case errors.Is(err, source.ErrNotDistributed):
		hint = "only the source's own app can install it; check whether the author publishes it on GitHub or Wago too"
	case errors.Is(err, source.ErrNotFound) && r.Source == ref.GitHub:
		hint = "check the ref; a repository with no releases yet counts as not found, as does a private one the token can't read. Knapsack uses GITHUB_TOKEN, then GH_TOKEN, then `gh auth token`; `knapsack sources list` shows which"
	case errors.Is(err, source.ErrNotFound):
		hint = "check the ref"
	case errors.Is(err, source.ErrRateLimited) && r.Source == ref.GitHub:
		hint = "set GITHUB_TOKEN, or log in with `gh auth login`, for a higher limit"
	case errors.Is(err, source.ErrRateLimited):
		hint = "wait a few minutes, then retry"
	case errors.Is(err, source.ErrUnauthorized) && r.Source == ref.GitHub:
		hint = "check the token Knapsack is using: GITHUB_TOKEN, GH_TOKEN or `gh auth token`"
	case errors.Is(err, source.ErrUnauthorized):
		hint = fmt.Sprintf("check the key%s, and set it again with `knapsack sources set-key %s`%s",
			keyPage(r.Source, " at "), r.Source, envOverride(r.Source))
	case errors.Is(err, source.ErrSourceNotConfigured):
		// A source that isn't configured is one read through its official
		// API without a key.
		hint = fmt.Sprintf("%s's official API needs a key: get one%s, then run `knapsack sources set-key %s`; or read its website instead, with `knapsack sources set-api %s website`",
			r.Source, keyPage(r.Source, " from "), r.Source, r.Source)
	case errors.Is(err, source.ErrUnexpectedWebsite):
		hint = fmt.Sprintf("Knapsack reads %s's website, which may have changed since this release. `knapsack self-update` may fix it; if not, please report it at %s, or use %s's official API, with a key: `knapsack sources set-api %s official`",
			r.Source, IssuesURL, r.Source, r.Source)
	case errors.Is(err, install.ErrWrongGame):
		hint = "the release isn't built for this profile's flavor"
	case errors.Is(err, source.ErrNoCompatibleRelease) && r.Source == ref.Wago:
		hint = "Knapsack installs only stable releases; check the addon's page on Wago for one for this flavor"
	case errors.Is(err, source.ErrNoCompatibleRelease) && r.Source == ref.CurseForge:
		hint = "Knapsack installs only release files, not betas or alphas; check the project's files on CurseForge for one for this flavor"
	case errors.Is(err, source.ErrNoCompatibleRelease):
		hint = "the release needs exactly one file Knapsack can tell is for this flavor; check its files on the source"
	default:
		return err
	}
	return &UserError{Msg: err.Error(), Hint: hint, Err: err}
}

// IssuesURL is where Knapsack's issues are reported.
const IssuesURL = "https://github.com/austinrr/knapsack/issues"

// ReportURL is a link that opens an issue about err when it's a source's
// website answering unexpectedly (DESIGN.md §8.6), with the error and
// running, the Knapsack running ("v0.2.0 on linux/amd64"), filled in. For
// any other error it's "". The error names the addon and the request, but
// nothing of the user's.
func ReportURL(err error, running string) string {
	if !errors.Is(err, source.ErrUnexpectedWebsite) {
		return ""
	}
	body := "Knapsack couldn't read a source's website:\n\n```\n" + err.Error() + "\n```\n\n" +
		"Knapsack " + running + "\n"
	q := url.Values{"title": {"Unexpected answer from a source's website"}, "body": {body}}
	return IssuesURL + "/new?" + q.Encode()
}

// keyPages are where each keyed source's users manage their API keys.
var keyPages = map[string]string{
	ref.CurseForge: "https://console.curseforge.com",
	ref.Wago:       "https://addons.wago.io/account/apikeys",
}

// envOverride notes the variable that overrides the source's stored key,
// which set-key can't fix while it's set.
func envOverride(name string) string {
	for _, ks := range config.KeyedSources() {
		if ks.Name == name {
			return fmt.Sprintf(" (or fix %s, which overrides it, if that's set)", ks.Env)
		}
	}
	return ""
}

// keyPage is the source's key page after prep, for a hint, or "" when
// there's none.
func keyPage(name, prep string) string {
	if page := keyPages[name]; page != "" {
		return prep + page
	}
	return ""
}

// FolderConflictError is the cause of an install refused because folders
// it would write already exist and no addon owns them, which Force
// replaces: install.ConflictError, for frontends, which don't import
// install. Find it with errors.As.
type FolderConflictError = install.ConflictError
