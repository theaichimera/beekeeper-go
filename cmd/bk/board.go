package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/board"
	"github.com/theaichimera/beekeeper-go/internal/freshness"
	"github.com/theaichimera/beekeeper-go/internal/stalewip"
)

func newBoardCmd() *cobra.Command {
	var (
		jsonOut   bool
		quiet     bool
		maxDepth  int
		statusOpt string
		leaseGaps bool
		strict    bool
		summary   bool
		staleDays int
	)
	c := &cobra.Command{
		Use:   "board [path...]",
		Short: "Cross-project work board (ready / in_progress / blocked + lease gaps).",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			r := board.Scan(paths, maxDepth)
			staleCount := computeStaleWIPCount(paths, maxDepth, staleDays)
			out := cmd.OutOrStdout()
			if jsonOut {
				payload := boardJSONPayloadWithStale(r, staleCount)
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
				if strict && r.Totals()["lease_gaps"] > 0 {
					silentExit(1)
				}
				return nil
			}
			if summary {
				printBoardSummary(cmd, r, staleCount, staleDays)
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
				// Freshness banner (bkg-ckb): the board is built from the
				// JSONL, which bd exports on a debounce after DB writes.
				// A newer DB means the buckets below may be stale.
				// cmd-level concern, like the stale-WIP count — the board
				// package itself does not know about timestamps.
				if freshness.Probe(pb.ProjectRoot).Stale {
					fmt.Fprintln(out, "  NOTE: bead DB is newer than .beads/issues.jsonl — board may be stale; run `bd sync`.")
				}
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
	c.Flags().BoolVar(&summary, "summary", false, "print rollup only (totals, status / priority breakdowns); no per-bead dump")
	c.Flags().IntVar(&staleDays, "stale-days", 0,
		"stale-WIP threshold for the in_progress count surfaced in --summary / --json; 0 = default 7d, negative = disable")
	return c
}

// computeStaleWIPCount runs the stale-WIP detector to populate the
// `stale_wip_count` field in --summary and --json. Pure read; never
// mutates state. Mirrors the doctor-side default semantics: 0 ->
// default 7d; negative -> disabled (returns 0).
func computeStaleWIPCount(paths []string, maxDepth, staleDays int) int {
	if staleDays < 0 {
		return 0
	}
	threshold := staleDays
	if threshold == 0 {
		threshold = stalewip.DefaultStaleDays
	}
	r := stalewip.Scan(paths, maxDepth, threshold, time.Now())
	return r.Count()
}

// printBoardSummary renders the human-readable rollup. Mirrors the
// JSON aggregate's keys 1:1 so an operator reading the text can map
// straight to fields in `--json`. The stale-WIP count is threaded in
// from the caller (cmd-level concern; the board package itself does
// not know about timestamps).
func printBoardSummary(cmd *cobra.Command, r board.Report, staleCount, staleDays int) {
	out := cmd.OutOrStdout()
	s := r.Aggregate()
	if s.Total == 0 {
		_, _ = fmt.Fprintln(out, "no projects with a .beads/ directory found, or no parseable records.")
		return
	}
	_, _ = fmt.Fprintf(out, "TOTAL %d  closed=%d  in_progress=%d  open=%d  blocked=%d  other=%d  (%.1f%% complete)\n",
		s.Total,
		s.ByStatus["closed"],
		s.ByStatus["in_progress"],
		s.ByStatus["open"],
		s.ByStatus["blocked"],
		s.ByStatus["other"],
		s.PercentComplete,
	)
	keys := make([]int, 0, len(s.ActiveByPriority))
	for k := range s.ActiveByPriority {
		keys = append(keys, k)
	}
	sortInts(keys)
	var parts []string
	for _, k := range keys {
		if k < 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("P%d:%d", k, s.ActiveByPriority[k]))
	}
	if n := s.ActiveByPriority[-1]; n > 0 {
		parts = append(parts, fmt.Sprintf("P?:%d", n))
	}
	_, _ = fmt.Fprintf(out, "ACTIVE BY PRIORITY  %s\n", joinSpaces(parts))
	threshold := staleDays
	if threshold == 0 {
		threshold = stalewip.DefaultStaleDays
	}
	_, _ = fmt.Fprintf(out, "WIP  in_progress=%d  lease_gaps=%d  stale>%dd=%d\n",
		s.InProgress, s.LeaseGaps, threshold, staleCount)
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

func joinSpaces(xs []string) string {
	out := ""
	for i, s := range xs {
		if i > 0 {
			out += "  "
		}
		out += s
	}
	if out == "" {
		out = "(none)"
	}
	return out
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

// boardJSONPayloadWithStale folds a caller-supplied stale-WIP count
// into the aggregate `summary.stale_wip_count` field. Callers that
// can't compute it (e.g. older tests) pass 0.
func boardJSONPayloadWithStale(r board.Report, staleWIP int) map[string]any {
	projs := make([]map[string]any, 0, len(r.Projects))
	for _, pb := range r.Projects {
		projs = append(projs, map[string]any{
			"project_root":       pb.ProjectRoot,
			"ready":              boardIssuesToJSON(pb.Ready),
			"in_progress":        boardIssuesToJSON(pb.InProgress),
			"blocked":            boardIssuesToJSON(pb.Blocked),
			"lease_gaps":         boardIssuesToJSON(pb.LeaseGaps()),
			"closed_count":       pb.ClosedCount,
			"other_count":        pb.OtherCount,
			"total":              pb.Total,
			"by_status":          pb.ByStatus,
			"active_by_priority": activePriorityToJSON(pb.ActiveByPriority),
		})
	}
	s := r.Aggregate()
	return map[string]any{
		"projects": projs,
		"totals":   r.Totals(),
		"summary": map[string]any{
			"total":              s.Total,
			"by_status":          s.ByStatus,
			"active_by_priority": activePriorityToJSON(s.ActiveByPriority),
			"percent_complete":   roundPercent(s.PercentComplete),
			"in_progress_count":  s.InProgress,
			"lease_gaps_count":   s.LeaseGaps,
			"stale_wip_count":    staleWIP,
		},
	}
}

// activePriorityToJSON keys priorities as strings ("P0".."P4", "P?")
// so the JSON object is deterministic and human-readable. The
// internal map keys ints; -1 collapses to "P?".
func activePriorityToJSON(m map[int]int) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		if k < 0 {
			out["P?"] += v
			continue
		}
		out[fmt.Sprintf("P%d", k)] += v
	}
	return out
}

// roundPercent trims to one decimal — keeps `--json` numerically
// stable across runs without leaking floating-point drift to consumers.
func roundPercent(p float64) float64 {
	return float64(int(p*10+0.5)) / 10.0
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
