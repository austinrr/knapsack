package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/ref"
)

// Names shown twice in one profile get their ref's ID; the same name in
// another profile, or an unmanaged folder with no ref, doesn't.
func TestUniqueNames(t *testing.T) {
	t.Parallel()
	a := ref.Ref{Source: ref.GitHub, ID: "a/details"}
	b := ref.Ref{Source: ref.GitHub, ID: "b/details-mirror"}
	c := ref.Ref{Source: ref.GitHub, ID: "c/baganator"}
	got := uniqueNames([]nameRow{
		{"forever", "Details!", a},
		{"forever", "details!", b},
		{"forever", "Baganator", c},
		{"retail", "Baganator", c},
		{"forever", "Loose", ref.Ref{}},
	})
	want := []string{"Details! (a/details)", "details! (b/details-mirror)", "Baganator", "Baganator", "Loose"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("uniqueNames (-want +got):\n%s", diff)
	}
}
