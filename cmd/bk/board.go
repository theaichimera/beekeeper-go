package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/board"
)

func newBoardCmd() *cobra.Command {
	var (
		jsonOut   bool
		quiet     bool
		maxDepth  int
		statusOpt string
		leaseGaps bool
		strict    bool
	)
	c := &cobra.Command{
		Use:   "board [path...]",
		Short: "Cross-project work board (ready / in_progress / blocked + lease gaps).",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			r := board.Scan(paths, maxDepth)
			out := cmd.OutOrStdout()
			if jsonOut {
				payload := boardJSONPayload(r)
				b, _ := json.MarshalIndent(payload, "", "  ")
				out.Write(b)
				out.Write([]byte("\n"))
				if strict && r.Totals()["lease_gaps"] > 0 {
					silentExit(1)
				}
				return nil
			}
			if len(r.Projects) == 0 {
				if !quiet {
					fmt.Fprintln(out, "no projects with a .beads/ directory found.")
				}
				return nil
			}
			showReady := (statusOpt == "all" || statusOpt == "ready") && !leaseGaps
			showIP := (statusOpt == "all" || statusOpt == "in_progress") || leaseGaps
			showBlocked := (statusOpt == "all" || statusOpt == "blocked") && !leaseGaps

			for _, pb := range r.Projects {
				if leaseGaps && len(pb.LeaseGaps()) == 0 {
					continue
				}
				isEmpty := len(pb.Ready) == 0 && len(pb.InProgress) == 0 &&
					len(pb.Blocked) == 0 && pb.ClosedCount == 0
				if isEmpty && quiet {
					continue
				}
				fmt.Fprintf(out, "=== %s ===\n", pb.ProjectRoot)
				if showReady && len(pb.Ready) > 0 {
					fmt.Fprintln(out, "READY")
					for _, i := range pb.Ready {
						printBoardRow(cmd, i)
					}
				}
				if showIP {
					issues := pb.InProgress
					header := "IN PROGRESS"
					if leaseGaps {
						issues = pb.LeaseGaps()
						header = "IN PROGRESS (lease gaps only)"
					}
					if len(issues) > 0 {
						fmt.Fprintln(out, header)
						for _, i := range issues {
							printBoardRow(cmd, i)
						}
					}
				}
				if showBlocked && len(pb.Blocked) > 0 {
					fmt.Fprintln(out, "BLOCKED")
					for _, i := range pb.Blocked {
						printBoardRow(cmd, i)
					}
				}
				fmt.Fprintln(out)
			}
			if !quiet {
				t := r.Totals()
				fmt.Fprintf(out,
					"totals: ready=%d  in_progress=%d  blocked=%d  lease_gaps=%d  closed=%d\n",
					t["ready"], t["in_progress"], t["blocked"], t["lease_gaps"], t["closed"],
				)
			}
			if strict && r.Totals()["lease_gaps"] > 0 {
				silentExit(1)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of human text")
	c.Flags().BoolVar(&quiet, "quiet", false, "suppress empty/totals chatter")
	c.Flags().IntVar(&maxDepth, "max-depth", 4, "walk depth")
	c.Flags().StringVar(&statusOpt, "status", "all", "show only one bucket (ready|in_progress|blocked|all)")
	c.Flags().BoolVar(&leaseGaps, "lease-gaps", false, "show only in_progress issues with no assignee")
	c.Flags().BoolVar(&strict, "strict", false, "exit 1 when any lease gaps exist (CI)")
	return c
}

func printBoardRow(cmd *cobra.Command, i board.Issue) {
	pri := "--"
	if i.Priority != nil {
		pri = fmt.Sprintf("P%d", *i.Priority)
	}
	kind := i.IssueType
	if kind == "" {
		kind = "-"
	}
	assignee := i.Assignee
	if assignee == "" {
		assignee = "—"
	}
	line := fmt.Sprintf("  %s %-7s %s  %s  [assignee=%s]", pri, kind, i.ID, i.Title, assignee)
	if i.IsLeaseGap {
		line += "  LEASE-GAP"
	}
	if len(i.UnresolvedBlockers) > 0 {
		line += "  UNRESOLVED-BLOCKERS="
		for j, b := range i.UnresolvedBlockers {
			if j > 0 {
				line += ","
			}
			line += b
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), line)
}

func boardJSONPayload(r board.Report) map[string]any {
	projs := make([]map[string]any, 0, len(r.Projects))
	for _, pb := range r.Projects {
		projs = append(projs, map[string]any{
			"project_root": pb.ProjectRoot,
			"ready":        boardIssuesToJSON(pb.Ready),
			"in_progress":  boardIssuesToJSON(pb.InProgress),
			"blocked":      boardIssuesToJSON(pb.Blocked),
			"lease_gaps":   boardIssuesToJSON(pb.LeaseGaps()),
			"closed_count": pb.ClosedCount,
			"other_count":  pb.OtherCount,
		})
	}
	return map[string]any{
		"projects": projs,
		"totals":   r.Totals(),
	}
}

func boardIssuesToJSON(xs []board.Issue) []map[string]any {
	out := make([]map[string]any, 0, len(xs))
	for _, i := range xs {
		m := map[string]any{
			"id":                  i.ID,
			"title":               i.Title,
			"status":              i.Status,
			"issue_type":          i.IssueType,
			"assignee":            i.Assignee,
			"updated_at":          i.UpdatedAt,
			"bucket":              i.Bucket,
			"is_lease_gap":        i.IsLeaseGap,
			"unresolved_blockers": i.UnresolvedBlockers,
		}
		if i.Priority != nil {
			m["priority"] = *i.Priority
		} else {
			m["priority"] = nil
		}
		out = append(out, m)
	}
	return out
}
