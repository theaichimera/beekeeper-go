package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/identity"
)

func newIdentityCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "identity",
		Short: "Canonical actor identity check / normalize.",
		Long: `Identity is OPT-IN: a workspace without .beadkeeper/identity.toml
emits "identity not configured" and exits 0. M2 ships scan + dry-run
only; the actual JSONL rewrite lands in M3.`,
	}
	c.AddCommand(newIdentityCheckCmd())
	c.AddCommand(newIdentityNormalizeCmd())
	return c
}

func newIdentityCheckCmd() *cobra.Command {
	var (
		jsonOut bool
		strict  bool
	)
	c := &cobra.Command{
		Use:   "check [repo]",
		Short: "Report identity drift in bead JSONL.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := "."
			if len(args) == 1 {
				repo = args[0]
			}
			cfg, _ := identity.LoadConfig(repo)
			out := cmd.OutOrStdout()
			if cfg == nil {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"identity not configured (%s/.beadkeeper/identity.toml missing). "+
						"Create the file to opt into canonical-identity enforcement.\n", repo)
				return nil
			}
			r := identity.ScanProject(repo)
			if jsonOut {
				payload := map[string]any{
					"project_root":         r.ProjectRoot,
					"canonical":            sortedKeys(r.Canonical),
					"aliased_handles":      r.AliasedHandles,
					"unmapped_handles":     r.SortedUnmapped(),
					"aliased_occurrences":  r.AliasedOccurrences,
					"unmapped_occurrences": r.UnmappedOccurrences,
					"has_drift":            r.HasDrift(),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
				if r.HasDrift() && strict {
					silentExit(1)
				}
				return nil
			}
			fmt.Fprintf(out, "canonical handles: %v\n", sortedKeys(r.Canonical))
			if len(r.AliasedHandles) > 0 {
				fmt.Fprintf(out, "aliased handles in use (%d occurrence(s)):\n", r.AliasedOccurrences)
				for _, k := range r.SortedAliased() {
					fmt.Fprintf(out, "  - %s -> %s\n", k, r.AliasedHandles[k])
				}
			}
			if len(r.UnmappedHandles) > 0 {
				fmt.Fprintf(out, "unmapped handles in use (%d occurrence(s)):\n", r.UnmappedOccurrences)
				for _, k := range r.SortedUnmapped() {
					fmt.Fprintf(out, "  - %s\n", k)
				}
			}
			if !r.HasDrift() {
				fmt.Fprintln(out, "OK — all actor handles in bead JSONL are canonical.")
			}
			if r.HasDrift() && strict {
				silentExit(1)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	c.Flags().BoolVar(&strict, "strict", false, "exit 1 when drift is present")
	return c
}

// `identity normalize` — dry-run by default; --yes applies the rewrite.
// Refuses while the bd daemon is alive (M3 mutation rule).
func newIdentityNormalizeCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "normalize [repo]",
		Short: "Rewrite aliased handles to canonical form (dry-run unless --yes).",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := "."
			if len(args) == 1 {
				repo = args[0]
			}
			cfg, _ := identity.LoadConfig(repo)
			if cfg == nil {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"identity not configured (%s/.beadkeeper/identity.toml missing).\n", repo)
				silentExit(64)
				return nil
			}
			r, err := identity.Normalize(repo, !yes)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "REFUSE: %s\n", err)
				silentExit(3)
				return nil
			}
			out := cmd.OutOrStdout()
			if r.DryRun {
				fmt.Fprintf(out, "DRY-RUN — would rewrite %d actor slot(s).\n", r.WouldRewriteCount)
			} else {
				fmt.Fprintf(out, "rewrote %d actor slot(s) in %s.\n", r.RewroteCount, r.JSONLPath)
			}
			for _, k := range sortedKeysString(r.MappedHandles) {
				fmt.Fprintf(out, "  %s -> %s\n", k, r.MappedHandles[k])
			}
			if len(r.SkippedUnmapped) > 0 {
				fmt.Fprintln(out, "unmapped handles left untouched:")
				for _, k := range sortedKeysSet(r.SkippedUnmapped) {
					fmt.Fprintf(out, "  - %s\n", k)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "apply the rewrite (otherwise dry-run); refused if daemon is alive")
	return c
}
