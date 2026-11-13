package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is the release version, set at build time with
//
//	-ldflags "-X main.version=v1.2.3"
//
// It's the one package-level variable in the program: the linker can only
// inject values into package-level string variables.
var version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the knapsack version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "knapsack %s\n", version)
			if err != nil {
				return fmt.Errorf("write version: %w", err)
			}
			return nil
		},
	}
}
