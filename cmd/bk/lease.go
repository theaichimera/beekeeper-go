package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/lease"
)

func newLeaseCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "lease",
		Short: "Per-issue lease discipline (claim / release / list).",
	}
	c.AddCommand(newLeaseClaimCmd())
	c.AddCommand(newLeaseReleaseCmd())
	c.AddCommand(newLeaseListCmd())
	return c
}

func newLeaseClaimCmd() *cobra.Command {
	var repo, asHandle string
	c := &cobra.Command{
		Use:   "claim <issue-id>",
		Short: "Claim a lease on an issue.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			caller, err := lease.ResolveCaller(repo, asHandle)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				silentExit(1)
				return nil
			}
			res, err := lease.Claim(repo, args[0], caller, nil)
			if err != nil {
				return handleLeaseErr(cmd, err)
			}
			out := cmd.OutOrStdout()
			if res.AlreadyHeld {
				fmt.Fprintf(out, "already held: %s by %s (no-op).\n", res.IssueID, res.Canonical)
			} else {
				fmt.Fprintf(out, "claimed %s for %s.\n", res.IssueID, res.Canonical)
			}
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "repo root")
	c.Flags().StringVar(&asHandle, "as", "", "override the auto-detected raw caller handle")
	return c
}

func newLeaseReleaseCmd() *cobra.Command {
	var repo, asHandle string
	c := &cobra.Command{
		Use:   "release <issue-id>",
		Short: "Release a lease you hold.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			caller, err := lease.ResolveCaller(repo, asHandle)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				silentExit(1)
				return nil
			}
			res, err := lease.Release(repo, args[0], caller, nil)
			if err != nil {
				return handleLeaseErr(cmd, err)
			}
			out := cmd.OutOrStdout()
			if res.AlreadyReleased {
				fmt.Fprintf(out, "already released: %s (no-op).\n", res.IssueID)
			} else {
				fmt.Fprintf(out, "released %s by %s.\n", res.IssueID, res.Canonical)
			}
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "repo root")
	c.Flags().StringVar(&asHandle, "as", "", "override the auto-detected raw caller handle")
	return c
}

func newLeaseListCmd() *cobra.Command {
	var (
		jsonOut           bool
		quiet             bool
		maxDepth          int
		staleAfterSeconds int
	)
	c := &cobra.Command{
		Use:   "list [path...]",
		Short: "List active leases (and flag stale).",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := defaultPaths(args)
			r := lease.ListLeases(paths, maxDepth, staleAfterSeconds)
			out := cmd.OutOrStdout()
			if jsonOut {
				payload := map[string]any{
					"leases":      leaseRowsJSON(r.Leases),
					"stale_count": len(r.Stale()),
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
				return nil
			}
			if len(r.Leases) == 0 {
				if !quiet {
					fmt.Fprintln(out, "no active leases.")
				}
				return nil
			}
			for _, l := range r.Leases {
				tag := "ACTIVE"
				if l.IsStale {
					tag = "STALE"
				}
				age := "?"
				if l.HasAge {
					age = fmt.Sprintf("%ds", int(l.AgeSeconds))
				}
				fmt.Fprintf(out, "[%s] %s  %s  assignee=%s  age=%s\n",
					tag, l.ProjectRoot, l.IssueID, l.Assignee, age)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	c.Flags().BoolVar(&quiet, "quiet", false, "")
	c.Flags().IntVar(&maxDepth, "max-depth", 4, "")
	c.Flags().IntVar(&staleAfterSeconds, "stale-after-seconds", lease.DefaultStaleAfterSeconds, "stale threshold")
	return c
}

func handleLeaseErr(cmd *cobra.Command, err error) error {
	var c *lease.LeaseConflict
	if errors.As(err, &c) {
		fmt.Fprintf(cmd.ErrOrStderr(), "REFUSE: %s\n", err)
		silentExit(3)
		return nil
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
	silentExit(1)
	return nil
}

func leaseRowsJSON(xs []lease.Lease) []map[string]any {
	out := make([]map[string]any, 0, len(xs))
	for _, l := range xs {
		row := map[string]any{
			"project_root": l.ProjectRoot,
			"issue_id":     l.IssueID,
			"assignee":     l.Assignee,
			"updated_at":   l.UpdatedAt,
			"is_stale":     l.IsStale,
		}
		if l.HasAge {
			row["age_seconds"] = l.AgeSeconds
		} else {
			row["age_seconds"] = nil
		}
		out = append(out, row)
	}
	return out
}
