package main

import "testing"

func TestCLI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "version", args: []string{"version"}},
		{name: "version_extra_arg", args: []string{"version", "extra"}},
		{name: "help", args: []string{"--help"}},
		{name: "no_args", args: nil},
		{name: "help_command", args: []string{"help", "version"}},
		{name: "help_unknown_topic", args: []string{"help", "frobnicate"}},
		{name: "unknown_command", args: []string{"frobnicate"}},
		{name: "unknown_command_suggestion", args: []string{"versio"}},
		{name: "unknown_flag", args: []string{"--frobnicate"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkGolden(t, tt.name, runCLI(t, tt.args...))
		})
	}
}
