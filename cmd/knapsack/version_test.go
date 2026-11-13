package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	buildInfo := func(version string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: version}}, true
		}
	}
	noBuildInfo := func() (*debug.BuildInfo, bool) { return nil, false }

	tests := []struct {
		name          string
		injected      string
		readBuildInfo func() (*debug.BuildInfo, bool)
		want          string
	}{
		{name: "injected wins", injected: "v1.2.3", readBuildInfo: buildInfo("v9.9.9"), want: "v1.2.3"},
		{name: "go install version", readBuildInfo: buildInfo("v0.1.0"), want: "v0.1.0"},
		{name: "devel build", readBuildInfo: buildInfo("(devel)"), want: "dev"},
		{name: "empty module version", readBuildInfo: buildInfo(""), want: "dev"},
		{name: "no build info", readBuildInfo: noBuildInfo, want: "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveVersion(tt.injected, tt.readBuildInfo); got != tt.want {
				t.Errorf("resolveVersion(%q) = %q, want %q", tt.injected, got, tt.want)
			}
		})
	}
}
