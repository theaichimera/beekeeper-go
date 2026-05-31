// Beekeeper Go — bk: single-binary Go rewrite of beadkeeper.
//
// Subcommand wiring is built on cobra. Behavioral contract is the Python
// beadkeeper repo (see docs/handoff/EPIC.md). When Go and Python disagree,
// Python wins unless we can prove Python is wrong.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is overwritten via -ldflags '-X main.Version=...' by goreleaser.
// On a fresh `go build`, the binary reports "dev" so behavior is identical
// whether you brew-installed it or built it locally.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "bk",
		Short:         "Operations layer for beads (bd).",
		Long:          "Beekeeper Go — single-binary operations layer for the bd issue tracker.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the bk version.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if Commit == "" && Date == "" {
				fmt.Fprintf(out, "bk %s\n", Version)
				return nil
			}
			fmt.Fprintf(out, "bk %s (commit %s, built %s)\n", Version, Commit, Date)
			return nil
		},
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
