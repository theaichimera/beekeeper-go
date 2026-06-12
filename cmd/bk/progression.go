package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// progressionLabel is the label that marks a bead as a progression
// (a topic-scoped living document). It MUST match the require_labels
// entry in pkg/beadspec/schemas/progression.toml and the board
// ExcludedLabels default.
const progressionLabel = "progression"

// validEntryTypes are the allowed --type values for `progression add`,
// mirroring the document types of the legacy progressions system.
var validEntryTypes = map[string]bool{
	"baseline":   true,
	"deepening":  true,
	"pivot":      true,
	"correction": true,
}

func newProgressionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "progression",
		Short: "Scaffold and maintain progression beads (topic arcs / the 'why').",
		Long: `A progression is a topic-scoped, living narrative bead: where a topic
started, how it pivoted, and the current understanding. bk scaffolds the
structure; you fill in the content. Progression beads carry the
"` + progressionLabel + `" label and are kept off the work board.`,
	}
	c.AddCommand(newProgressionNewCmd())
	c.AddCommand(newProgressionAddCmd())
	c.AddCommand(newProgressionListCmd())
	return c
}

func newProgressionNewCmd() *cobra.Command {
	var repo string
	c := &cobra.Command{
		Use:   "new <topic>",
		Short: "Create a new progression bead (scaffolds the structure).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := strings.Join(args, " ")
			body := newProgressionBody(topic, today())
			rc, stdout, stderr, err := runBd(
				[]string{"create", topic, "-t", "task", "--labels", progressionLabel, "-d", body, "--silent"},
				repo,
			)
			if err != nil || rc != 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: bd create failed: rc=%d err=%v\n%s\n", rc, err, stderr)
				silentExit(1)
				return nil
			}
			id := strings.TrimSpace(stdout)
			if id == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "error: bd create exited 0 but returned no bead id")
				silentExit(1)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created progression %s: %q\n", id, topic)
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "repo root")
	return c
}

func newProgressionAddCmd() *cobra.Command {
	var repo, entryType string
	c := &cobra.Command{
		Use:   "add <id> [message...]",
		Short: "Append a dated entry to a progression's ## Log.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validEntryTypes[entryType] {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"error: --type must be one of baseline|deepening|pivot|correction (got %q)\n", entryType)
				silentExit(1)
				return nil
			}
			id := args[0]
			message := strings.TrimSpace(strings.Join(args[1:], " "))
			body, err := bdDescription(repo, id)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				silentExit(1)
				return nil
			}
			updated := appendLogEntry(body, today(), entryType, message)
			rc, _, stderr, err := runBd([]string{"update", id, "-d", updated}, repo)
			if err != nil || rc != 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: bd update failed: rc=%d err=%v\n%s\n", rc, err, stderr)
				silentExit(1)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added %s entry to %s.\n", entryType, id)
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "repo root")
	c.Flags().StringVar(&entryType, "type", "deepening", "entry type: baseline|deepening|pivot|correction")
	return c
}

func newProgressionListCmd() *cobra.Command {
	var repo string
	c := &cobra.Command{
		Use:   "list",
		Short: "List progression beads with their current-understanding one-liner.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, stdout, stderr, err := runBd([]string{"list", "-l", progressionLabel, "--json"}, repo)
			if err != nil || rc != 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: bd list failed: rc=%d err=%v\n%s\n", rc, err, stderr)
				silentExit(1)
				return nil
			}
			rows := parseBeadList(stdout)
			out := cmd.OutOrStdout()
			if len(rows) == 0 {
				fmt.Fprintln(out, "no progressions.")
				return nil
			}
			for _, r := range rows {
				fmt.Fprintf(out, "%s  %s\n", r.id, r.title)
				if oneLiner := currentUnderstandingOneLiner(r.description); oneLiner != "" {
					fmt.Fprintf(out, "    %s\n", oneLiner)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "repo root")
	return c
}

// runBd shells out to `bd <args...>` in repo via the injectable
// project runner, prepending the "bd" argv[0] so call sites can't
// forget it (execwrap executes args[0] as the binary — bkg-qv6).
// Per the execwrap contract, callers must branch on rc, not err.
func runBd(args []string, repo string) (int, string, string, error) {
	return bkproject.BdRun(append([]string{"bd"}, args...), repo, 30*time.Second)
}

// --- pure helpers (no bd) ----------------------------------------------

func today() string { return time.Now().Format("2006-01-02") }

// newProgressionBody returns the scaffolded body for a new progression.
// It conforms to pkg/beadspec/schemas/progression.toml: a non-empty
// "## Current understanding" section and a "## Log" section.
func newProgressionBody(topic, date string) string {
	return fmt.Sprintf(`# %s

## Current understanding

_TODO: summarize where this topic stands. Update this as understanding evolves._

## Log

- %s baseline: progression created for %q.
`, topic, date, topic)
}

// appendLogEntry inserts a dated entry at the end of the "## Log"
// section. If no Log section exists, one is appended.
func appendLogEntry(body, date, entryType, message string) string {
	entry := fmt.Sprintf("- %s %s:", date, entryType)
	if message != "" {
		entry += " " + message
	}

	lines := strings.Split(body, "\n")
	logIdx := -1
	for i, ln := range lines {
		if strings.EqualFold(strings.TrimSpace(ln), "## Log") {
			logIdx = i
			break
		}
	}
	if logIdx == -1 {
		trimmed := strings.TrimRight(body, "\n")
		return trimmed + "\n\n## Log\n\n" + entry + "\n"
	}

	// Find the end of the Log section: the next heading after logIdx, or EOF.
	end := len(lines)
	for i := logIdx + 1; i < len(lines); i++ {
		if isHeadingLine(lines[i]) {
			end = i
			break
		}
	}
	// Trim trailing blank lines within the section before inserting.
	insertAt := end
	for insertAt > logIdx+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, entry)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n")
}

func isHeadingLine(line string) bool {
	t := strings.TrimLeft(line, " ")
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return n > 0 && n < len(t) && t[n] == ' '
}

// currentUnderstandingOneLiner returns the first non-empty, non-italic
// content line under "## Current understanding", truncated.
func currentUnderstandingOneLiner(body string) string {
	lines := strings.Split(body, "\n")
	in := false
	for _, ln := range lines {
		if strings.EqualFold(strings.TrimSpace(ln), "## Current understanding") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if isHeadingLine(ln) {
			break
		}
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "_") {
			continue
		}
		if len(t) > 80 {
			return t[:80] + "…"
		}
		return t
	}
	return ""
}

// --- bd JSON parsing ---------------------------------------------------

type beadRow struct {
	id          string
	title       string
	description string
}

// bdDescription fetches a single bead's description via `bd show --json`.
func bdDescription(repo, id string) (string, error) {
	rc, stdout, stderr, err := runBd([]string{"show", id, "--json"}, repo)
	if err != nil || rc != 0 {
		return "", fmt.Errorf("bd show %s failed: rc=%d err=%v\n%s", id, rc, err, stderr)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(stdout), &rec); err != nil {
		// Some bd versions (0.47+) emit a one-element array.
		var arr []map[string]any
		if err2 := json.Unmarshal([]byte(stdout), &arr); err2 != nil || len(arr) == 0 {
			return "", fmt.Errorf("parsing bd show output: %w", err)
		}
		rec = arr[0]
	}
	// Some bd versions wrap the issue under "issue"/"data".
	if inner, ok := rec["issue"].(map[string]any); ok {
		rec = inner
	} else if inner, ok := rec["data"].(map[string]any); ok {
		rec = inner
	}
	if d, ok := rec["description"].(string); ok {
		return d, nil
	}
	return "", nil
}

// parseBeadList parses `bd list --json` output, tolerating either a
// bare array or an object wrapping the array under issues/data.
func parseBeadList(s string) []beadRow {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		var wrap map[string]any
		if err2 := json.Unmarshal([]byte(s), &wrap); err2 != nil {
			return nil
		}
		for _, key := range []string{"issues", "data", "results"} {
			if raw, ok := wrap[key].([]any); ok {
				for _, v := range raw {
					if m, ok := v.(map[string]any); ok {
						arr = append(arr, m)
					}
				}
				break
			}
		}
	}
	var out []beadRow
	for _, m := range arr {
		out = append(out, beadRow{
			id:          asString(m["id"]),
			title:       asString(m["title"]),
			description: asString(m["description"]),
		})
	}
	return out
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
