package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// buildVersion is the release version, set at build time with
//
//	-ldflags "-X main.buildVersion=v1.2.3"
//
// It's the one package-level variable in the program, because the linker can
// only inject values into package-level string variables. Only main reads
// it; everything else receives the resolved version as a parameter.
var buildVersion string

// resolveVersion picks the version to report: the linker-injected value if
// there is one, else the module version Go embeds in the binary (set by
// `go install …@v1.2.3`), else "dev".
func resolveVersion(injected string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if injected != "" {
		return injected
	}
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func newVersionCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the knapsack version",
		Long: `Print the knapsack version. It also checks for a newer release, and if
there is one, says so on stderr with how to update. Set ` + noNotifierEnv + `
to skip the check.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "knapsack %s\n", c.env.version)
			if err != nil {
				return fmt.Errorf("write version: %w", err)
			}
			checkFromVersion(cmd, c)
			return nil
		},
	}
}
