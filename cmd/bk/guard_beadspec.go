package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/beadlint"
)

// newGuardBeadspecCmd implements `bk guard beadspec` — the hard gate
// the pre-push hook calls (bkg-4zi.6). It validates the working tree's
// beads against their type schemas (pkg/beadspec, via internal/beadlint
// — the same validation `bk doctor` uses) and exits non-zero when an
// EPIC violates its schema. Progression (and other non-epic) findings
// are advisory: printed, but they do not block the push.
func newGuardBeadspecCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		all     bool
	)
	c := &cobra.Command{
		Use:   "beadspec [path]",
		Short: "Validate beads against their type schemas; block epic violations.",
		Long: `Validates each non-closed bead against the schema for its issue_type
(pkg/beadspec). By default, EPIC-schema violations (missing rationale /
malformed ## Decisions) cause a non-zero exit so the pre-push hook blocks
the push; progression and other findings are advisory (printed, non-blocking).

--all makes ANY schema violation blocking.

Exit codes:
  0  no blocking violations
  2  one or more blocking violations`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := firstArgOr(args, repo)
			findings := beadlint.ScanRepo(root)

			out := cmd.OutOrStdout()
			if jsonOut {
				b, _ := json.MarshalIndent(findings, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
			}

			blocking := beadlint.FilterByType(findings, "epic")
			if all {
				blocking = findings
			}

			if !jsonOut {
				errOut := cmd.ErrOrStderr()
				for _, f := range findings {
					tag := "warn"
					if f.Type == "epic" || all {
						tag = "BLOCK"
					}
					fmt.Fprintf(errOut, "[%s] %s (%s): %s\n", tag, f.BeadID, f.Rule, f.Message)
				}
			}

			if len(blocking) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"\nbk: %d blocking bead-schema violation(s). Fix the bead(s) above, "+
						"or bypass this push with BEADKEEPER_SKIP_HOOK=1.\n", len(blocking))
				silentExit(2)
			}
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "repo root")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit findings as JSON")
	c.Flags().BoolVar(&all, "all", false, "block on ANY schema violation, not just epics")
	return c
}
