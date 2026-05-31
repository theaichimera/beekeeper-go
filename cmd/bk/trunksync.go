package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
	"github.com/theaichimera/beekeeper-go/internal/trunksync"
)

func newTrunkSyncCmd() *cobra.Command {
	var (
		jsonOut  bool
		quiet    bool
		strict   bool
		maxDepth int
		apply    bool
		yes      bool
	)
	c := &cobra.Command{
		Use:   "trunk-sync [path...]",
		Short: "Detect or replay bead JSONL drift between trunk and the sync branch.",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			out := cmd.OutOrStdout()

			if apply {
				projects := bkproject.FindProjects(paths, maxDepth)
				if len(projects) == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "no projects with a .beads/ directory found.")
					silentExit(64)
					return nil
				}
				worstRC := 0
				for _, p := range projects {
					res, err := trunksync.ApplyOne(p, !yes)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "REFUSE: %s: %s\n", p.Root, err)
						if worstRC < 3 {
							worstRC = 3
						}
						continue
					}
					tag := "NOOP"
					switch {
					case res.Applied:
						tag = "APPLIED"
					case res.DryRun && res.WouldApply:
						tag = "DRY-RUN"
					}
					fmt.Fprintf(out, "[%s] %s  trunk=%s sync=%s\n", tag, p.Root, res.Trunk, res.SyncBranch)
					fmt.Fprintf(out, "        sync-only commits: %d\n", res.SyncOnlyCount)
					if res.Message != "" {
						fmt.Fprintf(out, "        %s\n", res.Message)
					}
					if res.CommitSHA != "" {
						fmt.Fprintf(out, "        new commit: %s\n", res.CommitSHA)
					}
				}
				if worstRC != 0 {
					silentExit(worstRC)
				}
				return nil
			}

			r := trunksync.Scan(paths, maxDepth)
			if jsonOut {
				payload := map[string]any{
					"worst":    string(r.Worst()),
					"findings": trunksyncFindingsJSON(r.Findings),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
			} else if len(r.Findings) == 0 {
				if !quiet {
					fmt.Fprintln(out, "OK — bead JSONL on every project's sync branch is reachable from its trunk branch.")
				}
			} else {
				for _, f := range r.Findings {
					tag := upper(string(f.Severity))
					fmt.Fprintf(out, "[%s] %s  %s  trunk=%s sync=%s\n", tag, f.ProjectRoot, f.Kind, f.Trunk, f.SyncBranch)
					fmt.Fprintf(out, "        sync-only=%d trunk-only=%d\n", f.SyncOnlyCount, f.TrunkOnlyCount)
					fmt.Fprintf(out, "        %s\n", f.Message)
					for _, line := range splitLines(f.Remediation) {
						fmt.Fprintf(out, "        %s\n", line)
					}
				}
			}
			switch r.Worst() {
			case trunksync.RED:
				silentExit(2)
			case trunksync.YELLOW:
				if strict {
					silentExit(1)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	c.Flags().BoolVar(&quiet, "quiet", false, "")
	c.Flags().BoolVar(&strict, "strict", false, "exit 1 on YELLOW as well as RED (scan only)")
	c.Flags().IntVar(&maxDepth, "max-depth", 4, "")
	c.Flags().BoolVar(&apply, "apply", false, "plan a fast-forward replay (dry-run unless --yes)")
	c.Flags().BoolVar(&yes, "yes", false, "actually commit the replay onto trunk (with --apply)")
	return c
}

func trunksyncFindingsJSON(fs []trunksync.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{
			"project_root":     f.ProjectRoot,
			"kind":             f.Kind,
			"severity":         string(f.Severity),
			"trunk":            f.Trunk,
			"sync_branch":      f.SyncBranch,
			"sync_only_count":  f.SyncOnlyCount,
			"trunk_only_count": f.TrunkOnlyCount,
			"message":          f.Message,
			"remediation":      f.Remediation,
		})
	}
	return out
}
