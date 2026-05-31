package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/daemons"
	"github.com/theaichimera/beekeeper-go/internal/guard"
	"github.com/theaichimera/beekeeper-go/internal/syncbranch"
)

func newGuardCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "guard",
		Short: "Config + runtime guardrails.",
	}
	c.AddCommand(newGuardDBCmd())
	c.AddCommand(newGuardSyncBranchCmd())
	c.AddCommand(newGuardDaemonCmd())
	return c
}

// `bk guard db` — DB-in-file-sync guardrail.
func newGuardDBCmd() *cobra.Command {
	var (
		jsonOut     bool
		quiet       bool
		maxDepth    int
		fix         bool
		destination string
		yes         bool
	)
	c := &cobra.Command{
		Use:   "db [path...]",
		Short: "Detect bead DBs that live inside a file-sync folder.",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			projects, r := guard.ScanPaths(paths, maxDepth, nil)
			out := cmd.OutOrStdout()

			if jsonOut {
				payload := map[string]any{
					"worst":    string(r.Worst()),
					"findings": guardDBFindingsJSON(r.Findings),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
			} else if r.IsClean() {
				if !quiet {
					fmt.Fprintf(out, "OK — scanned %d project(s); no bead DBs found inside known file-sync folders.\n", len(projects))
				}
			} else {
				for _, f := range r.Findings {
					tag := upper(string(f.Severity))
					fmt.Fprintf(out, "[%s] %s\n", tag, f.DBPath)
					fmt.Fprintf(out, "        %s\n", f.Message)
					for _, line := range splitLines(f.Remediation) {
						fmt.Fprintf(out, "        %s\n", line)
					}
				}
			}

			if !fix {
				if r.Worst() == guard.RED {
					silentExit(2)
				}
				return nil
			}

			if destination == "" {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"error: --fix requires --destination DIR (where to move the .db / -wal / -shm files).")
				silentExit(64)
				return nil
			}
			if !yes {
				fmt.Fprintln(out, "DRY-RUN mode (pass --yes to actually move files).")
			}
			rc := 0
			for _, p := range projects {
				plan := guard.PlanFix(p, destination)
				if plan.DaemonBlocking {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"REFUSE: daemon alive (pid %d) for %s; stop it and re-run.\n",
						plan.Daemon.PID, p.Root)
					if rc < 3 {
						rc = 3
					}
					continue
				}
				lines, err := guard.ApplyFix(plan, !yes)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "REFUSE: %s\n", err)
					if rc < 3 {
						rc = 3
					}
					continue
				}
				for _, l := range lines {
					fmt.Fprintln(out, l)
				}
			}
			if rc != 0 {
				silentExit(rc)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	c.Flags().BoolVar(&quiet, "quiet", false, "")
	c.Flags().IntVar(&maxDepth, "max-depth", 4, "")
	c.Flags().BoolVar(&fix, "fix", false, "plan a relocation; dry-run unless --yes")
	c.Flags().StringVar(&destination, "destination", "", "root directory to move .db files into")
	c.Flags().BoolVar(&yes, "yes", false, "actually move files (otherwise --fix is a dry run)")
	return c
}

func guardDBFindingsJSON(fs []guard.DBFinding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		row := map[string]any{
			"project_root":   f.Project.Root,
			"db_path":        f.DBPath,
			"filesync_root":  "",
			"filesync_label": "",
			"severity":       string(f.Severity),
			"message":        f.Message,
			"remediation":    f.Remediation,
		}
		if f.Match != nil {
			row["filesync_root"] = f.Match.Root
			row["filesync_label"] = f.Match.Label
		}
		out = append(out, row)
	}
	return out
}

func newGuardSyncBranchCmd() *cobra.Command {
	var (
		jsonOut  bool
		quiet    bool
		maxDepth int
		strict   bool
		setVal   string
	)
	c := &cobra.Command{
		Use:   "sync-branch [path...]",
		Short: "Detect empty sync.branch + bead commits stranded off the sync branch.",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			if setVal != "" {
				target := paths[0]
				res, err := syncbranch.SetBranch(target, setVal)
				out := cmd.OutOrStdout()
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "REFUSE: %s (%s)\n", err, res.Path)
					silentExit(3)
					return nil
				}
				fmt.Fprintf(out, "sync.branch set to '%s' at %s\n", setVal, res.Path)
				return nil
			}
			r := syncbranch.Scan(paths, maxDepth)
			out := cmd.OutOrStdout()
			if jsonOut {
				payload := map[string]any{
					"worst":    string(r.Worst()),
					"findings": syncbranchFindingsToJSON(r.Findings),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
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
	c.Flags().StringVar(&setVal, "set", "", "set sync.branch in .beads/config.json (refuses if daemon is alive)")
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
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
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
