package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/doctor"
	"github.com/theaichimera/beekeeper-go/internal/drift"
)

func newDoctorCmd() *cobra.Command {
	var (
		jsonOut         bool
		noColor         bool
		strict          bool
		maxDepth        int
		staleDays       int
		gate            bool
		gateBase        string
		gateBehindLimit int
	)
	c := &cobra.Command{
		Use:   "doctor [path...]",
		Short: "Cross-project bead health scan.",
		Long: `Walks each path looking for .beads/ directories and reports
RED / YELLOW / GREEN per project plus an overall worst severity.

Exit codes (mirrors the Python tool):
  RED                -> 2
  YELLOW + --strict  -> 1
  otherwise          -> 0

--gate runs the cheap drift-only scan instead (bkg-59b): branch-behind-base,
unset sync.branch, and bd needs_manual_sync. Exit codes:
  GREEN              -> 0
  YELLOW             -> 1   (warn-only by default; agent-friendly)
  YELLOW + --strict  -> 2   (or set BK_DRIFT_BLOCK=1 for the same effect)
  RED                -> 2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if gate {
				return runDoctorGate(cmd, firstArgOr(args, "."), gateBase, gateBehindLimit, strict, jsonOut)
			}
			paths := defaultPaths(args)
			r := doctor.RunWithOpts(paths, doctor.Opts{
				MaxDepth:  maxDepth,
				StaleDays: staleDays,
			})
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
	c.Flags().IntVar(&staleDays, "stale-days", 0,
		"stale-WIP threshold (in_progress beads untouched > N days); 0 = default 7d, negative = disable")
	c.Flags().BoolVar(&gate, "gate", false,
		"cheap drift-only scan (branch-behind / sync.branch / needs_manual_sync); meant for the pre-commit hook + agent repo-entry checks")
	c.Flags().StringVar(&gateBase, "base", "",
		"with --gate: base ref for behind-count (default: origin/<default> -> origin/main -> main)")
	c.Flags().IntVar(&gateBehindLimit, "behind-threshold", 0,
		"with --gate: warn when branch is more than N commits behind base (default 50)")
	return c
}

// runDoctorGate runs the drift-only scan and prints a compact report.
// Output is intentionally short — this runs on every commit via the
// pre-commit hook, so verbose noise is a tax on every developer
// action. JSON mode emits the full Report for downstream agents.
func runDoctorGate(cmd *cobra.Command, repo, base string, behindThreshold int, strict bool, jsonOut bool) error {
	r, err := drift.Scan(drift.Opts{
		Repo:            repo,
		Base:            base,
		BehindThreshold: behindThreshold,
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
		silentExit(1)
		return nil
	}
	out := cmd.OutOrStdout()
	if jsonOut {
		payload := map[string]any{
			"repo":         r.Repo,
			"branch":       r.Branch,
			"base":         r.Base,
			"behind_count": r.BehindCount,
			"threshold":    r.Threshold,
			"worst":        string(r.Worst()),
			"findings":     driftFindingsToJSON(r.Findings),
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		_, _ = out.Write(b)
		_, _ = out.Write([]byte("\n"))
	} else if len(r.Findings) == 0 {
		// Silent on GREEN: pre-commit hook fires every commit.
	} else {
		for _, f := range r.Findings {
			tag := upper(string(f.Severity))
			fmt.Fprintf(cmd.ErrOrStderr(), "[%s] %s: %s\n", tag, f.Kind, f.Message)
			if f.Remediation != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "        %s\n", f.Remediation)
			}
		}
	}
	if code := drift.ExitCode(r.Worst(), strict); code != 0 {
		silentExit(code)
	}
	return nil
}

func driftFindingsToJSON(fs []drift.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		row := map[string]any{
			"kind":        f.Kind,
			"severity":    string(f.Severity),
			"message":     f.Message,
			"remediation": f.Remediation,
		}
		if f.Branch != "" {
			row["branch"] = f.Branch
		}
		if f.Base != "" {
			row["base"] = f.Base
		}
		if f.BehindCount > 0 {
			row["behind_count"] = f.BehindCount
			row["threshold"] = f.Threshold
		}
		out = append(out, row)
	}
	return out
}

func firstArgOr(args []string, dflt string) string {
	if len(args) > 0 {
		return args[0]
	}
	return dflt
}
