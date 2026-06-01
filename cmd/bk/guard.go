package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/daemons"
	"github.com/theaichimera/beekeeper-go/internal/git"
	"github.com/theaichimera/beekeeper-go/internal/guard"
	"github.com/theaichimera/beekeeper-go/internal/prbeads"
	"github.com/theaichimera/beekeeper-go/internal/staleship"
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
	c.AddCommand(newGuardPRBeadsCmd())
	c.AddCommand(newGuardStaleBeadsCmd())
	return c
}

// `bk guard stale-beads` — git<->backlog reconciliation.
func newGuardStaleBeadsCmd() *cobra.Command {
	var (
		repo      string
		branch    string
		prefix    string
		lookback  int
		jsonOut   bool
		shipTypes []string
		source    string
		closeMode bool
		apply     bool
		exclude   []string
		force     bool
	)
	c := &cobra.Command{
		Use:   "stale-beads [path]",
		Short: "Detect open / in_progress beads whose work already shipped on the default branch.",
		Long: `Scans merged commit subjects on a branch (default: remote default branch)
for bead-id tokens, and reports open / in_progress beads whose id
appears in a shipped subject. The post-merge sibling of guard pr-beads.

Match precision (the make-or-break detail):
  - The bead id must appear in the commit SUBJECT, not the body.
  - The id must be delimited (id-alphabet boundary).
  - Either the subject is in conventional-commit scope form
    (feat(<id>): ..., <id>: ..., [<id>] ...), OR the subject ends
    with a (#N) PR-merge marker and the id is token-bounded.
  - Tangential mentions like "see <id> for context" are NOT matched.

Exit codes (per bk contract):
  0  no shipped-not-closed beads
  2  one or more shipped-not-closed beads (RED)
  64 missing / invalid flag
  127 git not on PATH`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !git.Available() {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "error: `git` not found on PATH.")
				silentExit(127)
				return nil
			}
			if len(args) > 0 {
				repo = args[0]
			}
			if repo == "" {
				repo = "."
			}
			if prefix == "" {
				if got, ok := staleship.DerivePrefix(repo); ok {
					prefix = got
				} else {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"error: cannot derive bead prefix; pass --prefix explicitly.")
					silentExit(64)
					return nil
				}
			}
			if branch == "" {
				if got, ok := resolveDefaultBranch(repo); ok {
					branch = got
				} else {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"error: cannot resolve default branch; pass --branch.")
					silentExit(64)
					return nil
				}
			}

			src, sok := parseStatusSource(source)
			if !sok {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"error: invalid --source %q (want auto|bd|jsonl).\n", source)
				silentExit(64)
				return nil
			}
			r, err := staleship.DiagnoseWithOpts(repo, branch, prefix, staleship.Opts{
				LookbackDays:     lookback,
				AllowedShipTypes: shipTypes,
				StatusSource:     src,
			})
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				silentExit(1)
				return nil
			}

			// `--close` (with optional `--apply`) — bkg-td0.3 batch-close path.
			if closeMode {
				runStaleBeadsClose(cmd, repo, r, exclude, force, apply, jsonOut)
				return nil
			}

			out := cmd.OutOrStdout()
			if jsonOut {
				payload := map[string]any{
					"branch":   r.Branch,
					"prefix":   r.Prefix,
					"lookback": r.Lookback,
					"worst":    string(r.Worst()),
					"findings": staleshipFindingsToJSON(r.Findings),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
			} else if len(r.Findings) == 0 {
				_, _ = fmt.Fprintf(out,
					"OK — no shipped-not-closed beads on `%s` (prefix=%s, lookback=%dd).\n",
					r.Branch, r.Prefix, r.Lookback)
			} else {
				_, _ = fmt.Fprintf(out, "Branch: %s\nPrefix: %s\nLookback: %dd\n\n",
					r.Branch, r.Prefix, r.Lookback)
				for _, f := range r.Findings {
					prTag := "(no #N)"
					if f.LandingPR > 0 {
						prTag = fmt.Sprintf("(#%d)", f.LandingPR)
					}
					_, _ = fmt.Fprintf(out, "[RED] %s  status=%s  %s  %s\n",
						f.BeadID, f.Status, prTag, f.LandingSHA[:7])
					_, _ = fmt.Fprintf(out, "        subject: %s\n", f.LandingSubject)
				}
				_, _ = fmt.Fprintf(out, "\n%d shipped-not-closed bead(s).\n",
					len(r.Findings))
			}

			if r.Worst() == staleship.RED {
				silentExit(2)
			}
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", "", "repo path (default: current dir or first positional arg)")
	c.Flags().StringVar(&branch, "branch", "", "branch to scan (default: remote default branch)")
	c.Flags().StringVar(&prefix, "prefix", "", "bead-id prefix (default: derived from JSONL)")
	c.Flags().IntVar(&lookback, "lookback-days", 90, "limit git log to last N days (0 = no filter)")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	c.Flags().StringSliceVar(&shipTypes, "ship-types", nil,
		"conventional-commit types treated as shipping (default: feat,fix,perf,refactor); "+
			"`spec` and scope `bd`/`beads` are always non-shipping regardless")
	c.Flags().StringVar(&source, "source", "auto",
		"bead-status source: auto|bd|jsonl (default auto: bd authoritative, JSONL fallback)")
	c.Flags().BoolVar(&closeMode, "close", false,
		"plan / apply close for shipped-not-closed beads (dry-run unless --apply)")
	c.Flags().BoolVar(&apply, "apply", false,
		"with --close: actually run `bd close` (mutates bead state)")
	c.Flags().StringSliceVar(&exclude, "exclude", nil,
		"comma-separated bead ids to skip even if they look shipped (e.g. holds pending verification)")
	c.Flags().BoolVar(&force, "force", false,
		"with --close --apply: pass --force to `bd close` so beads with open blockers still close")
	return c
}

// runStaleBeadsClose handles the --close / --apply branch of `bk
// guard stale-beads`. Builds the plan from the supplied Report,
// executes (when --apply is set), and prints either the agent-driven
// JSON summary or a human-readable line-by-line plan/result.
//
// Exit code stays at the bkg-bqa.3 contract: rc=2 when ANY finding
// was RED (the underlying detect step), regardless of close outcome.
// Re-runs land at rc=0 because authoritative-status (bkg-td0.2) sees
// the freshly-closed beads as closed and emits no findings.
func runStaleBeadsClose(cmd *cobra.Command, repo string, r staleship.Report, exclude []string, force, apply, jsonOut bool) {
	out := cmd.OutOrStdout()
	plan, err := staleship.PlanCloses(repo, r.Findings, exclude, force)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "error: plan failed: %s\n", err)
		silentExit(1)
		return
	}
	summary := staleship.ApplyCloses(repo, plan, !apply)

	if jsonOut {
		payload := map[string]any{
			"branch":   r.Branch,
			"prefix":   r.Prefix,
			"lookback": r.Lookback,
			"worst":    string(r.Worst()),
			"findings": staleshipFindingsToJSON(r.Findings),
			"close": map[string]any{
				"dry_run":          summary.DryRun,
				"applied":          summary.Apply,
				"closed":           summary.Closed,
				"skipped_blocked":  skippedBlockedJSON(summary.SkippedBlocked),
				"skipped_excluded": summary.SkippedExcluded,
				"failed":           failedJSON(summary.Failed),
				"plan":             actionsJSON(summary.Actions),
				"counts": map[string]int{
					"closed":           len(summary.Closed),
					"skipped_blocked":  len(summary.SkippedBlocked),
					"skipped_excluded": len(summary.SkippedExcluded),
					"failed":           len(summary.Failed),
				},
			},
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		_, _ = out.Write(b)
		_, _ = out.Write([]byte("\n"))
	} else {
		mode := "DRY-RUN"
		if apply {
			mode = "APPLY"
		}
		_, _ = fmt.Fprintf(out, "[%s] %d action(s)\n", mode, len(summary.Actions))
		for _, a := range summary.Actions {
			line := fmt.Sprintf("  %-22s %s  reason=%q", a.Decision, a.BeadID, a.Reason)
			if a.Blocker != "" {
				line += "  blocker=" + a.Blocker
			}
			if a.Error != "" {
				line += "  error=" + a.Error
			}
			if a.Applied {
				line += "  ✓ closed"
			}
			_, _ = fmt.Fprintln(out, line)
		}
		_, _ = fmt.Fprintf(out,
			"\nsummary: closed=%d  skipped_blocked=%d  skipped_excluded=%d  failed=%d\n",
			len(summary.Closed), len(summary.SkippedBlocked),
			len(summary.SkippedExcluded), len(summary.Failed),
		)
	}

	if r.Worst() == staleship.RED {
		silentExit(2)
	}
}

func skippedBlockedJSON(xs []staleship.CloseAction) []map[string]any {
	out := make([]map[string]any, 0, len(xs))
	for _, a := range xs {
		out = append(out, map[string]any{
			"id":      a.BeadID,
			"blocker": a.Blocker,
			"reason":  a.Reason,
		})
	}
	return out
}

func failedJSON(xs []staleship.CloseAction) []map[string]any {
	out := make([]map[string]any, 0, len(xs))
	for _, a := range xs {
		out = append(out, map[string]any{
			"id":     a.BeadID,
			"reason": a.Reason,
			"error":  a.Error,
		})
	}
	return out
}

func actionsJSON(xs []staleship.CloseAction) []map[string]any {
	out := make([]map[string]any, 0, len(xs))
	for _, a := range xs {
		row := map[string]any{
			"id":       a.BeadID,
			"reason":   a.Reason,
			"decision": string(a.Decision),
			"applied":  a.Applied,
		}
		if a.Blocker != "" {
			row["blocker"] = a.Blocker
		}
		if a.Error != "" {
			row["error"] = a.Error
		}
		out = append(out, row)
	}
	return out
}

// parseStatusSource maps the --source flag to the staleship enum.
// "" / "auto" -> Auto, "bd" -> Bd, "jsonl" -> JSONL. Other values
// surface as a 64 usage error.
func parseStatusSource(s string) (staleship.StatusSource, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return staleship.StatusSourceAuto, true
	case "bd":
		return staleship.StatusSourceBd, true
	case "jsonl":
		return staleship.StatusSourceJSONL, true
	}
	return 0, false
}

func staleshipFindingsToJSON(fs []staleship.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{
			"id":              f.BeadID,
			"status":          f.Status,
			"landing_sha":     f.LandingSHA,
			"landing_subject": f.LandingSubject,
			"landing_pr":      f.LandingPR,
			// `bd_close_command` is shaped so an agent can drive
			// closure deterministically (per acceptance criteria).
			"bd_close_command": bdCloseCommand(f),
		})
	}
	return out
}

// bdCloseCommand renders the canonical close-incantation for a
// shipped-not-closed bead. Agents reading the JSON can execute this
// verbatim. The `--reason` quoting is shell-safe (no embedded quotes
// in the canonical form).
func bdCloseCommand(f staleship.Finding) string {
	if f.LandingPR > 0 {
		return fmt.Sprintf(`bd close %s --reason "shipped in #%d"`, f.BeadID, f.LandingPR)
	}
	return fmt.Sprintf(`bd close %s --reason "shipped in %s"`, f.BeadID, f.LandingSHA[:7])
}

// resolveDefaultBranch picks the remote default branch with the
// short alias, or falls back to a local `main` / `master`.
func resolveDefaultBranch(repo string) (string, bool) {
	if def, ok := git.DefaultRemoteBranch(repo, "origin"); ok {
		// Prefer the remote-tracking ref so locally-stale clones
		// reflect upstream merges.
		if git.RefExists(repo, "origin/"+def) {
			return "origin/" + def, true
		}
		if git.RefExists(repo, def) {
			return def, true
		}
	}
	for _, candidate := range []string{"origin/main", "main", "origin/master", "master"} {
		if git.RefExists(repo, candidate) {
			return candidate, true
		}
	}
	return "", false
}

// `bk guard pr-beads` — content-aware backlog-regression gate.
func newGuardPRBeadsCmd() *cobra.Command {
	var (
		base    string
		head    string
		policy  string
		jsonOut bool
		strict  bool
		repo    string
	)
	c := &cobra.Command{
		Use:   "pr-beads",
		Short: "Detect backlog regressions when merging head into base.",
		Long: `Diff .beads/issues.jsonl between two refs and report any change that
would rewind backlog state on merge: status rewinds, dropped assignees,
stale timestamps, dropped records.

Defaults: base = $GITHUB_BASE_REF or remote default branch; head = HEAD.

Policies:
  regression (default): only regressions fail.
  no-beads:             ANY commit in base..head touching the JSONL fails.

Exit codes match bk's contract:
  0 clean | 1 generic / --strict YELLOW | 2 RED | 64 missing flag | 127 git missing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !git.Available() {
				fmt.Fprintln(cmd.ErrOrStderr(), "error: `git` not found on PATH.")
				silentExit(127)
				return nil
			}
			if repo == "" {
				repo = "."
			}
			pol, ok := normalizePolicy(policy)
			if !ok {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"error: invalid --policy %q (want regression|no-beads).\n", policy)
				silentExit(64)
				return nil
			}
			resolvedBase, ok := resolveBaseRef(repo, base)
			if !ok {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"error: cannot resolve --base; pass --base explicitly or set GITHUB_BASE_REF.\n")
				silentExit(64)
				return nil
			}
			resolvedHead := head
			if resolvedHead == "" {
				resolvedHead = "HEAD"
			}
			if !git.RefExists(repo, resolvedHead) {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: head ref %q not found.\n", resolvedHead)
				silentExit(64)
				return nil
			}

			r, err := prbeads.Diagnose(repo, resolvedBase, resolvedHead, pol)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				silentExit(1)
				return nil
			}

			out := cmd.OutOrStdout()
			if jsonOut {
				payload := map[string]any{
					"base":     r.BaseRef,
					"head":     r.HeadRef,
					"policy":   string(r.Policy),
					"worst":    string(r.Worst()),
					"findings": prbeadsFindingsToJSON(r.Findings),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
			} else if len(r.Findings) == 0 {
				fmt.Fprintf(out,
					"OK — no backlog regressions between %s and %s (policy=%s).\n",
					r.BaseRef, r.HeadRef, r.Policy)
			} else {
				fmt.Fprintf(out, "Base: %s\nHead: %s\nPolicy: %s\n\n",
					r.BaseRef, r.HeadRef, r.Policy)
				for _, f := range r.Findings {
					fmt.Fprintf(out, "[%s] %s  %s\n",
						upper(string(f.Severity)), f.Kind, f.ID)
					fmt.Fprintf(out, "        %s\n", f.Message)
				}
			}

			switch r.Worst() {
			case prbeads.RED:
				silentExit(2)
			case prbeads.YELLOW:
				if strict {
					silentExit(1)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&base, "base", "", "base ref (default: $GITHUB_BASE_REF or remote default branch)")
	c.Flags().StringVar(&head, "head", "", "head ref (default: HEAD)")
	c.Flags().StringVar(&policy, "policy", "regression", "regression|no-beads")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	c.Flags().BoolVar(&strict, "strict", false, "exit 1 on YELLOW as well as RED")
	c.Flags().StringVar(&repo, "repo", "", "repo path (default: current dir)")
	return c
}

func normalizePolicy(p string) (prbeads.Policy, bool) {
	switch p {
	case "regression", "":
		return prbeads.PolicyRegression, true
	case "no-beads":
		return prbeads.PolicyNoBeads, true
	}
	return "", false
}

// resolveBaseRef wraps prbeads.ResolveBaseRef for the CLI, which
// honors $GITHUB_BASE_REF as a CI fallback.
func resolveBaseRef(repo, explicit string) (string, bool) {
	return prbeads.ResolveBaseRef(repo, explicit, true)
}

func prbeadsFindingsToJSON(fs []prbeads.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{
			"id":         f.ID,
			"field":      f.Field,
			"kind":       f.Kind,
			"base_value": f.BaseValue,
			"head_value": f.HeadValue,
			"severity":   string(f.Severity),
			"message":    f.Message,
		})
	}
	return out
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
