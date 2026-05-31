package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/theaichimera/beekeeper-go/internal/mergeslot"
)

func newMergeSlotCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "merge-slot",
		Short: "Acquire / release / inspect the per-workspace bd merge slot.",
	}
	c.AddCommand(newMergeSlotAcquireCmd())
	c.AddCommand(newMergeSlotReleaseCmd())
	c.AddCommand(newMergeSlotStatusCmd())
	return c
}

func envHolder(holder string) string {
	if holder != "" {
		return holder
	}
	for _, k := range []string{"BD_ACTOR", "USER"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func newMergeSlotAcquireCmd() *cobra.Command {
	var (
		repo   string
		holder string
		wait   bool
	)
	c := &cobra.Command{
		Use:   "acquire",
		Short: "Acquire the slot.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := envHolder(holder)
			if h == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "error: holder is required (--holder or $BD_ACTOR/$USER)")
				silentExit(64)
				return nil
			}
			res, err := mergeslot.Acquire(repo, h, wait, nil)
			if err != nil {
				return handleMergeSlotErr(cmd, err)
			}
			if res.AlreadyHeld {
				fmt.Fprintf(cmd.OutOrStdout(), "already held: %s by %s (no-op).\n", res.SlotID, res.Holder)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "acquired %s for %s.\n", res.SlotID, res.Holder)
			}
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "")
	c.Flags().StringVar(&holder, "holder", "", "holder name (default $BD_ACTOR/$USER)")
	c.Flags().BoolVar(&wait, "wait", false, "queue if already held instead of failing")
	return c
}

func newMergeSlotReleaseCmd() *cobra.Command {
	var repo, holder string
	c := &cobra.Command{
		Use:   "release",
		Short: "Release the slot you hold.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := envHolder(holder)
			if h == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "error: holder is required (--holder or $BD_ACTOR/$USER)")
				silentExit(64)
				return nil
			}
			res, err := mergeslot.Release(repo, h, nil)
			if err != nil {
				return handleMergeSlotErr(cmd, err)
			}
			if res.AlreadyReleased {
				fmt.Fprintf(cmd.OutOrStdout(), "already released: %s (no-op).\n", res.SlotID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "released %s by %s.\n", res.SlotID, res.Holder)
			}
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "")
	c.Flags().StringVar(&holder, "holder", "", "")
	return c
}

func newMergeSlotStatusCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	c := &cobra.Command{
		Use:   "status",
		Short: "Show slot status + holder.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s := mergeslot.Status(repo)
			out := cmd.OutOrStdout()
			if jsonOut {
				payload := map[string]any{
					"slot_id": s.SlotID,
					"status":  s.Status,
					"holder":  s.Holder,
				}
				b, _ := json.MarshalIndent(payload, "", "  ")
				_, _ = out.Write(b)
				_, _ = out.Write([]byte("\n"))
				return nil
			}
			if s.Status == "missing" {
				fmt.Fprintf(out, "no merge slot exists at %s. Run `bd merge-slot create`.\n", repo)
				return nil
			}
			holder := s.Holder
			if holder == "" {
				holder = "<none>"
			}
			fmt.Fprintf(out, "%s: status=%s holder=%s\n", s.SlotID, s.Status, holder)
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", ".", "")
	c.Flags().BoolVar(&jsonOut, "json", false, "")
	return c
}

func handleMergeSlotErr(cmd *cobra.Command, err error) error {
	var c *mergeslot.MergeSlotConflict
	if errors.As(err, &c) {
		fmt.Fprintf(cmd.ErrOrStderr(), "REFUSE: %s\n", err)
		silentExit(3)
		return nil
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
	silentExit(1)
	return nil
}
