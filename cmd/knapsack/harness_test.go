package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files in testdata/")

// result is everything observable about one CLI invocation.
type result struct {
	Stdout string
	Stderr string
	Code   int
}

// testVersion is the version every golden test runs with, so golden files
// don't depend on how the test binary was built.
const testVersion = "v0.0.0-test"

// runCLI runs the CLI in-process with args and returns its output and exit
// code.
func runCLI(t *testing.T, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, env{stdout: &stdout, stderr: &stderr, version: testVersion})
	return result{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}
}

// String renders a result in the golden file format: the exit code, then
// stdout and stderr in labelled sections.
func (r result) String() string {
	return fmt.Sprintf("# exit: %d\n# stdout:\n%s# stderr:\n%s", r.Code, r.Stdout, r.Stderr)
}

// checkGolden compares r with testdata/<name>.golden. Run
// `go test ./cmd/... -update` to rewrite the golden files after an
// intended change, then review the diff.
func checkGolden(t *testing.T, name string, r result) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")
	got := r.String()

	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("update golden file: %v", err)
		}
		return
	}

	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	// .gitattributes checks golden files out with LF on every OS.
	want := string(wantBytes)

	if got != want {
		t.Errorf("output differs from %s (run with -update to accept):\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}
