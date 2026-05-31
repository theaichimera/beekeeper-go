package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/hooks"
)

func newInstallHooksCmd() *cobra.Command {
	var (
		block         bool
		force         bool
		alsoIndicator bool
	)
	c := &cobra.Command{
		Use:   "install-hooks [repo]",
		Short: "Install the bk pre-push hook for `repo`.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := "."
			if len(args) == 1 {
				repo = args[0]
			}
			r := hooks.InstallPrePush(repo, block, force)
			out := cmd.OutOrStdout()
			if r.Written {
				fmt.Fprintf(out, "installed pre-push hook: %s\n", r.Path)
				if block {
					fmt.Fprintln(out, "  (will BLOCK pushes when doctor reports RED)")
				} else {
					fmt.Fprintln(out, "  (will WARN on RED; set BEADKEEPER_BLOCK_ON_RED=1 to block)")
				}
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "skipped: %s (%s)\n", r.SkippedReason, r.Path)
				silentExit(64)
				return nil
			}
			if alsoIndicator {
				fmt.Fprintln(out)
				fmt.Fprint(out, hooks.RenderPromptIndicator())
			}
			return nil
		},
	}
	c.Flags().BoolVar(&block, "block", false, "make the hook BLOCK on RED (default: warn only)")
	c.Flags().BoolVar(&force, "force", false, "overwrite a non-beadkeeper hook if one is present")
	c.Flags().BoolVar(&alsoIndicator, "print-prompt-indicator", false, "also print the sourceable shell indicator")
	return c
}

func newUninstallHooksCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "uninstall-hooks [repo]",
		Short: "Remove the bk-managed pre-push hook from `repo`.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := "."
			if len(args) == 1 {
				repo = args[0]
			}
			r := hooks.UninstallPrePush(repo)
			if r.Written {
				fmt.Fprintf(cmd.OutOrStdout(), "removed pre-push hook: %s\n", r.Path)
				return nil
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "nothing removed: %s (%s)\n", r.SkippedReason, r.Path)
			return nil
		},
	}
	return c
}

func newPromptIndicatorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prompt-indicator",
		Short: "Print the sourceable shell prompt indicator script.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte(hooks.RenderPromptIndicator()))
			return nil
		},
	}
}
