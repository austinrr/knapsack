package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// addOutputFlag registers -o on a command that prints data, accepting only
// the formats it supports. The default, "", is a human-readable table.
// Commands that change things don't take -o, so it's never silently ignored.
func addOutputFlag(cmd *cobra.Command, dst *string, formats ...string) {
	cmd.Flags().VarP(&outputFormat{dst: dst, formats: formats}, "output", "o",
		"output format: "+strings.Join(formats, " or "))
	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(formats, cobra.ShellCompDirectiveNoFileComp))
}

// outputFormat is a flag value that rejects unsupported formats while flags
// are parsed, before the command runs. It implements pflag.Value.
type outputFormat struct {
	dst     *string
	formats []string
}

func (o *outputFormat) String() string { return *o.dst }

func (o *outputFormat) Set(s string) error {
	if !slices.Contains(o.formats, s) {
		return fmt.Errorf("unknown output format (want %s)", strings.Join(o.formats, " or "))
	}
	*o.dst = s
	return nil
}

func (o *outputFormat) Type() string { return "format" }

// writeJSON writes v as indented JSON.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// newTable returns a writer that aligns tab-separated columns, kubectl
// style. Call Flush when done.
func newTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// printer writes command output and remembers the first write error, so
// commands check it once at the end instead of after every line. This is
// the errWriter pattern from "Errors are values" (go.dev/blog/errors-are-values).
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, args...)
	}
}
