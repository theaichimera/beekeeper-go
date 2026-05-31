package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/daemons"
	"github.com/theaichimera/beekeeper-go/internal/syncbranch"
)

func newGuardCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "guard",
		Short: "Config + runtime guardrails (detection-only in M2).",
	}
	c.AddCommand(newGuardSyncBranchCmd())
	c.AddCommand(newGuardDaemonCmd())
	return c
}

func newGuardSyncBranchCmd() *cobra.Command {
	var (
		jsonOut  bool
		quiet    bool
		maxDepth int
		strict   bool
	)
	c := &cobra.Command{
		Use:   "sync-branch [path...]",
		Short: "Detect empty sync.branch + bead commits stranded off the sync branch.",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			r := syncbranch.Scan(paths, maxDepth)
			out := cmd.OutOrStdout()
			if jsonOut {
				payload := map[string]any{
					"worst":    string(r.Worst()),
					"findings": syncbranchFindingsToJSON(r.Findings),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				out.Write(b)
				out.Write([]byte("\n"))
			} else if len(r.Findings) == 0 {
				if !quiet {
					fmt.Fprintln(out, "OK — sync.branch is configured and no bead-data commits are stranded on non-sync branches.")
				}
			} else {
				for _, f := range r.Findings {
					tag := string(f.Severity)
					label := f.Kind
					if f.Branch != "" {
						label += fmt.Sprintf(" branch=%s commits=%d", f.Branch, f.CommitCount)
					}
					fmt.Fprintf(out, "[%s] %s  %s\n", upper(tag), f.ProjectRoot, label)
					fmt.Fprintf(out, "        %s\n", f.Message)
					for _, line := range splitLines(f.Remediation) {
						fmt.Fprintf(out, "        %s\n", line)
					}
				}
			}
			switch r.Worst() {
			case syncbranch.RED:
				silentExit(2)
			case syncbranch.YELLOW:
				if strict {
					silentExit(1)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	c.Flags().BoolVar(&quiet, "quiet", false, "")
	c.Flags().IntVar(&maxDepth, "max-depth", 4, "")
	c.Flags().BoolVar(&strict, "strict", false, "exit 1 on YELLOW as well as RED")
	return c
}

func newGuardDaemonCmd() *cobra.Command {
	var (
		jsonOut  bool
		quiet    bool
		maxDepth int
		strict   bool
	)
	c := &cobra.Command{
		Use:   "daemon [path...]",
		Short: "Detect duplicate daemons + silent remote-helper failures.",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			r := daemons.Scan(paths, maxDepth)
			out := cmd.OutOrStdout()
			if jsonOut {
				payload := map[string]any{
					"worst":    string(r.Worst()),
					"findings": daemonFindingsToJSON(r.Findings),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				out.Write(b)
				out.Write([]byte("\n"))
			} else if len(r.Findings) == 0 {
				if !quiet {
					fmt.Fprintln(out, "OK — no duplicate daemons, no recent remote-helper failures.")
				}
			} else {
				for _, f := range r.Findings {
					extra := ""
					if len(f.PIDs) > 0 {
						extra = " pids=" + intsCSV(f.PIDs)
					}
					fmt.Fprintf(out, "[%s] %s  %s%s\n", upper(string(f.Severity)), f.ProjectRoot, f.Kind, extra)
					fmt.Fprintf(out, "        %s\n", f.Message)
					for _, line := range splitLines(f.Remediation) {
						fmt.Fprintf(out, "        %s\n", line)
					}
				}
			}
			switch r.Worst() {
			case daemons.RED:
				silentExit(2)
			case daemons.YELLOW:
				if strict {
					silentExit(1)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	c.Flags().BoolVar(&quiet, "quiet", false, "")
	c.Flags().IntVar(&maxDepth, "max-depth", 4, "")
	c.Flags().BoolVar(&strict, "strict", false, "exit 1 on YELLOW as well as RED")
	return c
}

func syncbranchFindingsToJSON(fs []syncbranch.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{
			"project_root": f.ProjectRoot,
			"kind":         f.Kind,
			"severity":     string(f.Severity),
			"branch":       f.Branch,
			"commit_count": f.CommitCount,
			"message":      f.Message,
			"remediation":  f.Remediation,
		})
	}
	return out
}

func daemonFindingsToJSON(fs []daemons.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{
			"project_root": f.ProjectRoot,
			"kind":         f.Kind,
			"severity":     string(f.Severity),
			"pids":         f.PIDs,
			"message":      f.Message,
			"remediation":  f.Remediation,
		})
	}
	return out
}
