package app

import (
	"errors"
	"fmt"
	"strings"

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
	case errors.Is(err, source.ErrNotFound):
		hint = "check the ref; a repository with no releases yet also counts as not found"
	case errors.Is(err, source.ErrRateLimited):
		hint = "set GITHUB_TOKEN, or log in with `gh auth login`, for a higher limit"
	case errors.Is(err, source.ErrUnauthorized):
		hint = "check the token or API key Knapsack is using"
	case errors.Is(err, source.ErrSourceNotConfigured):
		hint = fmt.Sprintf("%s needs an API key: set sources.%s.api_key in config, or %s",
			r.Source, r.Source, "KNAPSACK_"+strings.ToUpper(r.Source)+"_API_KEY")
	case errors.Is(err, install.ErrWrongGame):
		hint = "the release isn't built for this profile's flavor"
	case errors.Is(err, source.ErrNoCompatibleRelease):
		hint = "the release needs exactly one file Knapsack can tell is for this flavor; check its files on the source"
	default:
		return err
	}
	return &UserError{Msg: err.Error(), Hint: hint, Err: err}
}
