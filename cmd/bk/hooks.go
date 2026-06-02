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
		skipPreCommit bool
	)
	c := &cobra.Command{
		Use:   "install-hooks [repo]",
		Short: "Install bk's pre-push + pre-commit drift hooks for `repo`.",
		Long: `Installs both:
  - pre-push:   runs ` + "`bk doctor`" + ` (and optionally pr-beads) before pushes.
  - pre-commit: runs ` + "`bk doctor --gate`" + ` (the cheap drift gate).

Idempotent: re-running rewrites the bk-managed hooks in place. If a
non-bk pre-commit already exists (typical: bd's flush-only hook), it
is preserved by being moved to ` + "`pre-commit.bk-chained`" + ` and
the bk wrapper exec's it after the drift gate so both keep running
on every commit.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := "."
			if len(args) == 1 {
				repo = args[0]
			}
			out := cmd.OutOrStdout()

			pp := hooks.InstallPrePush(repo, block, force)
			if pp.Written {
				fmt.Fprintf(out, "installed pre-push hook: %s\n", pp.Path)
				if block {
					fmt.Fprintln(out, "  (will BLOCK pushes when doctor reports RED)")
				} else {
					fmt.Fprintln(out, "  (will WARN on RED; set BEADKEEPER_BLOCK_ON_RED=1 to block)")
				}
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "skipped pre-push: %s (%s)\n", pp.SkippedReason, pp.Path)
				silentExit(64)
				return nil
			}

			if !skipPreCommit {
				pc := hooks.InstallPreCommit(repo, force)
				if pc.Written {
					fmt.Fprintf(out, "installed pre-commit hook: %s\n", pc.Path)
					fmt.Fprintln(out, "  (drift gate: warns on YELLOW; set BK_DRIFT_BLOCK=1 to block)")
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "skipped pre-commit: %s (%s)\n", pc.SkippedReason, pc.Path)
					silentExit(64)
					return nil
				}
			}

			if alsoIndicator {
				fmt.Fprintln(out)
				fmt.Fprint(out, hooks.RenderPromptIndicator())
			}
			return nil
		},
	}
	c.Flags().BoolVar(&block, "block", false, "make the pre-push hook BLOCK on RED (default: warn only)")
	c.Flags().BoolVar(&force, "force", false, "overwrite a non-bk hook if one is present")
	c.Flags().BoolVar(&alsoIndicator, "print-prompt-indicator", false, "also print the sourceable shell indicator")
	c.Flags().BoolVar(&skipPreCommit, "skip-pre-commit", false, "skip installing the pre-commit drift gate (install only pre-push)")
	return c
}

func newUninstallHooksCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "uninstall-hooks [repo]",
		Short: "Remove the bk-managed pre-push + pre-commit hooks from `repo`.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := "."
			if len(args) == 1 {
				repo = args[0]
			}
			out := cmd.OutOrStdout()
			any := false
			if r := hooks.UninstallPrePush(repo); r.Written {
				fmt.Fprintf(out, "removed pre-push hook: %s\n", r.Path)
				any = true
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "pre-push: %s (%s)\n", r.SkippedReason, r.Path)
			}
			if r := hooks.UninstallPreCommit(repo); r.Written {
				fmt.Fprintf(out, "removed pre-commit hook: %s\n", r.Path)
				any = true
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "pre-commit: %s (%s)\n", r.SkippedReason, r.Path)
			}
			if !any {
				fmt.Fprintln(cmd.ErrOrStderr(), "nothing removed.")
			}
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
