package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/paths"
	"github.com/austinrr/knapsack/internal/ref"
)

var baganator = ref.Ref{Source: "github", ID: "austinrr/baganator-mirror"}

func TestNameAddon(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		res  app.Result
		want string
	}{
		{"prefixed", app.Result{Ref: baganator, Err: errors.New("Interface 120105")},
			"github:austinrr/baganator-mirror: Interface 120105"},
		{"source error already named", app.Result{Ref: baganator, Err: errors.New("github austinrr/baganator-mirror: not found")},
			"github austinrr/baganator-mirror: not found"},
		// The ID appearing later in the message doesn't count.
		{"id mid-message", app.Result{Ref: ref.Ref{Source: "curseforge", ID: "1201"}, Err: errors.New("Interface 120105")},
			"curseforge:1201: Interface 120105"},
		{"lookup error already named", app.Result{Name: "nope", Err: errors.New(`"nope" isn't in profile "forever"`)},
			`"nope" isn't in profile "forever"`},
		{"name prefixed", app.Result{Name: "nope", Err: errors.New("boom")}, "nope: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := nameAddon(tt.res.Name, tt.res.Ref, tt.res.Err).Error(); got != tt.want {
				t.Errorf("nameAddon() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFinish(t *testing.T) {
	t.Parallel()
	ok := app.Result{Ref: baganator, Action: app.ActionInstalled}
	if err := finish(&printer{w: io.Discard}, failures([]app.Result{ok}), nil); err != nil {
		t.Fatalf("finish() with no failures = %v, want nil", err)
	}

	// Every error is kept, in order: the addon's, the one that stopped the
	// command, then the stdout write failure.
	failed := app.Result{Ref: baganator, Action: app.ActionFailed, Err: errors.New("not found")}
	stopped, written := errors.New("config is in use"), errors.New("broken pipe")
	err := finish(&printer{w: io.Discard, err: written}, failures([]app.Result{ok, failed}), stopped)
	var errs addonErrors
	if !errors.As(err, &errs) {
		t.Fatalf("finish() = %T, want addonErrors", err)
	}
	got := make([]string, len(errs))
	for i, e := range errs {
		got[i] = e.Error()
	}
	want := []string{"github:austinrr/baganator-mirror: not found", "config is in use", "broken pipe"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("finish() errors (-want +got):\n%s", diff)
	}
}

func TestLineReporter(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	r := &lineReporter{w: &buf, verbose: true}
	r.Event(app.Event{Kind: app.EventWarned, Ref: baganator, Message: "Core is out of date"})
	// Skips are printed by the command, so not here too.
	r.Event(app.Event{Kind: app.EventSkipped, Ref: baganator, Message: "already added"})
	want := "warning: github:austinrr/baganator-mirror: Core is out of date\n"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// A bad ref is a usage error, caught before any work: gh is never asked
// for a token.
func TestAddBadRefNeverAsksGH(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"add", "github:a/b", "notaref"}, env{
		stdout: io.Discard, stderr: &stderr,
		getenv: func(k string) string {
			if k == paths.HomeEnv {
				return home
			}
			return ""
		},
		ghAuthToken: func(context.Context) string {
			t.Error("gh auth token was run")
			return ""
		},
	})
	if code != exitError {
		t.Errorf("exit = %d, want %d; stderr:\n%s", code, exitError, &stderr)
	}
}
