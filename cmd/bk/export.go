package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/export"
)

// `bk export` — emit a vectorizable bead corpus (bkg-3xb).
func newExportCmd() *cobra.Command {
	var (
		format          string
		outPath         string
		statusOpt       string
		types           []string
		includeLabels   []string
		excludeLabels   []string
		since           string
		includeComments bool
		noComments      bool
		includeClosed   bool
		classify        bool
		minRichness     int
		decisionsOnly   bool
		textTemplate    string
		fields          []string
		repoFilter      string
		redact          bool
	)
	c := &cobra.Command{
		Use:   "export [paths...]",
		Short: "Emit a vectorizable bead corpus (NDJSON / JSON), one document per bead.",
		Long: `Walks paths (default: current dir) for .beads/ projects and emits one
document per bead. Authoritative state: prefers the sync-branch JSONL,
falls back to the working tree. Identity normalized. Comments pulled
via 'bd comments <id> --json' when available.

Read-only: never mutates beads, never touches the daemon.

Document schema (stable; see README for full field list):
  id, repo, title, body, comments, text, status, issue_type, priority,
  labels, assignee, deps{blocks,blocked_by}, created_at, updated_at,
  closed_at, is_routine, is_decision, richness, superseded_by,
  schema_version.

Curation flags (is_routine / is_decision / richness) are METADATA, never
deletion. --min-richness and --decisions-only filter on those flags;
--classify=false disables computation entirely.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmtSel, ok := parseFormat(format)
			if !ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: invalid --format %q (want ndjson|json)\n", format)
				silentExit(64)
				return nil
			}
			statusSel, ok := parseStatusFilter(statusOpt)
			if !ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: invalid --status %q (want open|closed|all)\n", statusOpt)
				silentExit(64)
				return nil
			}
			// --include-closed is a convenience inverse of --status closed/all.
			// When user explicitly disables closed, narrow status to open.
			if !includeClosed && statusSel == export.StatusAll {
				statusSel = export.StatusOpen
			}
			// Resolve --include-comments / --no-comments precedence.
			incComments := includeComments
			if noComments {
				incComments = false
			}

			var sinceT time.Time
			if since != "" {
				t, err := parseSince(since)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "error: invalid --since %q: %s\n", since, err)
					silentExit(64)
					return nil
				}
				sinceT = t
			}

			// Identity map: best-effort, per first project. Multi-project
			// callers should keep canonical handles consistent across
			// `.beadkeeper/identity.toml` files.
			var identityMap map[string]string
			if len(args) > 0 {
				if im, _ := export.LoadIdentityMap(args[0]); im != nil {
					identityMap = im
				}
			} else {
				if im, _ := export.LoadIdentityMap("."); im != nil {
					identityMap = im
				}
			}

			docs, err := export.Build(export.Opts{
				Paths:           args,
				Status:          statusSel,
				Types:           types,
				IncludeLabels:   includeLabels,
				ExcludeLabels:   excludeLabels,
				Since:           sinceT,
				IncludeComments: incComments,
				Classify:        classify,
				MinRichness:     minRichness,
				DecisionsOnly:   decisionsOnly,
				TextTemplate:    textTemplate,
				Fields:          fields,
				RepoFilter:      repoFilter,
				Redact:          redact,
				IdentityMap:     identityMap,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				silentExit(1)
				return nil
			}

			projected, err := export.ProjectFields(docs, fields)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: project fields: %s\n", err)
				silentExit(1)
				return nil
			}

			out, closeOut, err := openOutput(outPath, cmd.OutOrStdout())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				silentExit(1)
				return nil
			}
			defer closeOut()

			if err := writeOutput(out, projected, fmtSel); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: write: %s\n", err)
				silentExit(1)
				return nil
			}
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "ndjson", "output format: ndjson|json (default ndjson)")
	c.Flags().StringVarP(&outPath, "output", "o", "", "output path (default stdout)")
	c.Flags().StringVar(&statusOpt, "status", "all", "status filter: open|closed|all")
	c.Flags().StringSliceVar(&types, "type", nil, "include only these issue_type values (comma-separated)")
	c.Flags().StringSliceVar(&includeLabels, "labels", nil, "include only beads carrying any of these labels")
	c.Flags().StringSliceVar(&excludeLabels, "exclude-labels", nil, "drop beads carrying any of these labels")
	c.Flags().StringVar(&since, "since", "", "include only beads with updated_at >= DATE (RFC3339 or 'YYYY-MM-DD' or '<N>d' / '<N>h')")
	c.Flags().BoolVar(&includeComments, "include-comments", true, "pull `bd comments <id>` per bead (default true)")
	c.Flags().BoolVar(&noComments, "no-comments", false, "shorthand for --include-comments=false")
	c.Flags().BoolVar(&includeClosed, "include-closed", true, "include closed beads in --status=all (default true)")
	c.Flags().BoolVar(&classify, "classify", true, "compute is_routine / is_decision / richness")
	c.Flags().IntVar(&minRichness, "min-richness", 0, "drop docs whose richness < N (0 = off)")
	c.Flags().BoolVar(&decisionsOnly, "decisions-only", false, "keep only docs with is_decision=true")
	c.Flags().StringVar(&textTemplate, "text-template", "", "template for assembled `text` field (default: title + body + comments)")
	c.Flags().StringSliceVar(&fields, "fields", nil, "project to a subset of schema fields (comma-separated)")
	c.Flags().StringVar(&repoFilter, "repo-filter", "", "glob over project basename / path; restrict to matching projects")
	c.Flags().BoolVar(&redact, "redact", false, "scrub email addresses and PAT-shaped tokens from text fields")
	return c
}

func parseFormat(s string) (export.Format, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "ndjson":
		return export.FormatNDJSON, true
	case "json":
		return export.FormatJSON, true
	}
	return "", false
}

func parseStatusFilter(s string) (export.StatusFilter, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all":
		return export.StatusAll, true
	case "open":
		return export.StatusOpen, true
	case "closed":
		return export.StatusClosed, true
	}
	return "", false
}

// parseSince accepts RFC3339, "YYYY-MM-DD", "<N>d" (N days ago),
// "<N>h" (N hours ago).
func parseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	// Relative form: <N>d / <N>h.
	if len(s) >= 2 {
		unit := s[len(s)-1]
		if unit == 'd' || unit == 'h' {
			var n int
			if _, err := fmt.Sscanf(s[:len(s)-1], "%d", &n); err == nil && n >= 0 {
				switch unit {
				case 'd':
					return time.Now().Add(-time.Duration(n) * 24 * time.Hour), nil
				case 'h':
					return time.Now().Add(-time.Duration(n) * time.Hour), nil
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized DATE format")
}

// openOutput returns a writer for `path` (or stdout when empty) plus
// a close func.
func openOutput(path string, stdout io.Writer) (io.Writer, func(), error) {
	if path == "" {
		return stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}

// writeOutput emits `docs` in the chosen format.
func writeOutput(w io.Writer, docs []map[string]any, format export.Format) error {
	switch format {
	case export.FormatJSON:
		b, err := json.MarshalIndent(docs, "", "  ")
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		_, err = w.Write([]byte("\n"))
		return err
	default: // ndjson
		for _, d := range docs {
			b, err := json.Marshal(d)
			if err != nil {
				return err
			}
			if _, err := w.Write(b); err != nil {
				return err
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return err
			}
		}
		return nil
	}
}
