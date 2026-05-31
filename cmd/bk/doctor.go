package main

import (
	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/doctor"
)

func newDoctorCmd() *cobra.Command {
	var (
		jsonOut  bool
		noColor  bool
		strict   bool
		maxDepth int
	)
	c := &cobra.Command{
		Use:   "doctor [path...]",
		Short: "Cross-project bead health scan.",
		Long: `Walks each path looking for .beads/ directories and reports
RED / YELLOW / GREEN per project plus an overall worst severity.

Exit codes (mirrors the Python tool):
  RED                -> 2
  YELLOW + --strict  -> 1
  otherwise          -> 0`,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			r := doctor.Run(paths, maxDepth)
			out := cmd.OutOrStdout()
			if jsonOut {
				_, _ = out.Write([]byte(doctor.RenderJSON(r)))
				_, _ = out.Write([]byte("\n"))
			} else {
				useColor := !noColor && isTTY(out)
				_, _ = out.Write([]byte(doctor.RenderText(r, useColor)))
				_, _ = out.Write([]byte("\n"))
			}
			code := doctor.ExitCode(r.Worst, strict)
			if code != 0 {
				// Cobra's normal return-error path prints "Error: ..."
				// which we don't want; signal exit via a custom helper.
				silentExit(code)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of human text")
	c.Flags().BoolVar(&noColor, "no-color", false, "disable ANSI color")
	c.Flags().BoolVar(&strict, "strict", false, "exit nonzero on YELLOW as well as RED")
	c.Flags().IntVar(&maxDepth, "max-depth", 4, "walk depth under each root looking for .beads/ dirs")
	return c
}
