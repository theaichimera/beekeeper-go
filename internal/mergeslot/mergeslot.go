// Package mergeslot wraps bd's per-workspace merge slot.
//
// Port of Python beadkeeper.mergeslot. Status() is read directly from
// `.beads/issues.jsonl` (no subprocess). Acquire / Release shell out
// to `bd merge-slot acquire|release` via an injectable runner.
//
// Safety semantics replicated verbatim:
//   - acquire/release REFUSE while bd daemon is alive.
//   - acquire is idempotent for the holder (no shell-out).
//   - acquire-by-other => MergeSlotConflict.
//   - release-by-other => MergeSlotConflict; release-when-open => no-op.
//   - CriticalSection() releases on exit (including via panic) unless
//     the acquire was a re-entrant self-claim.
package mergeslot

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// BdRunner is the injectable subprocess runner.
type BdRunner = execwrap.Runner

// --- errors -------------------------------------------------------------

// MergeSlotError is the base error.
type MergeSlotError struct{ Msg string }

func (e *MergeSlotError) Error() string { return e.Msg }

// MergeSlotConflict signals contention with another holder.
type MergeSlotConflict struct{ Msg string }

func (e *MergeSlotConflict) Error() string { return e.Msg }

// IsMergeSlotError reports whether err is either error type.
func IsMergeSlotError(err error) bool {
	var me *MergeSlotError
	var mc *MergeSlotConflict
	return errors.As(err, &me) || errors.As(err, &mc)
}

// --- result types -------------------------------------------------------

// SlotStatus is the typed snapshot read from the JSONL.
type SlotStatus struct {
	SlotID string // empty when no slot exists yet
	Status string // "open" | "in_progress" | "missing"
	Holder string // empty when not held
}

// AcquireResult is returned on a successful acquire.
type AcquireResult struct {
	SlotID      string
	Holder      string
	Acquired    bool
	AlreadyHeld bool
}

// ReleaseResult is returned on a successful release.
type ReleaseResult struct {
	SlotID          string
	Holder          string
	Released        bool
	AlreadyReleased bool
}

// --- status read --------------------------------------------------------

// Status reads the slot's typed view from the JSONL.
func Status(repo string) SlotStatus {
	repoAbs := absResolve(repo)
	p := bkproject.Project{Root: repoAbs}
	rec := readSlotRecord(p.IssuesJSONL())
	if rec == nil {
		return SlotStatus{Status: "missing"}
	}
	st, _ := rec["status"].(string)
	if st == "" {
		st = "open"
	}
	slot := SlotStatus{
		SlotID: stringOr(rec["id"], ""),
		Status: st,
	}
	if h, ok := rec["holder"].(string); ok && h != "" {
		slot.Holder = h
	}
	return slot
}

// --- mutations ---------------------------------------------------------

func refuseOnLiveDaemon(repo string) error {
	st := bkproject.ReadDaemonState(bkproject.Project{Root: repo})
	if st.PIDAlive {
		return &MergeSlotError{Msg: fmt.Sprintf(
			"refusing: bd daemon (pid %d) is alive for %s. Stop the daemon and re-run.",
			st.PID, repo,
		)}
	}
	return nil
}

// Acquire claims the slot for `holder`. Idempotent self-claim. Returns
// *MergeSlotConflict on contention.
func Acquire(repo, holder string, wait bool, runner BdRunner) (AcquireResult, error) {
	if holder == "" {
		return AcquireResult{}, &MergeSlotError{Msg: "holder is required"}
	}
	repoAbs := absResolve(repo)
	current := Status(repoAbs)

	if current.Status == "in_progress" {
		if current.Holder == holder {
			return AcquireResult{
				SlotID:      current.SlotID,
				Holder:      holder,
				Acquired:    true,
				AlreadyHeld: true,
			}, nil
		}
		return AcquireResult{}, &MergeSlotConflict{Msg: fmt.Sprintf(
			"refusing to acquire: slot held by '%s', not '%s'.", current.Holder, holder,
		)}
	}

	if err := refuseOnLiveDaemon(repoAbs); err != nil {
		return AcquireResult{}, err
	}
	if runner == nil {
		runner = execwrap.Default
	}
	cmd := []string{"bd", "merge-slot", "acquire", "--holder", holder}
	if wait {
		cmd = append(cmd, "--wait")
	}
	rc, _, errStr, _ := runner(cmd, repoAbs, 30*time.Second)
	if rc != 0 {
		return AcquireResult{}, &MergeSlotError{Msg: fmt.Sprintf(
			"bd merge-slot acquire failed for %s: rc=%d err=%q",
			holder, rc, strings.TrimSpace(errStr),
		)}
	}
	after := Status(repoAbs)
	return AcquireResult{
		SlotID:   after.SlotID,
		Holder:   holder,
		Acquired: true,
	}, nil
}

// Release frees the slot if held by `holder`. No-op when status==open.
// *MergeSlotConflict if another holder owns it.
func Release(repo, holder string, runner BdRunner) (ReleaseResult, error) {
	if holder == "" {
		return ReleaseResult{}, &MergeSlotError{Msg: "holder is required"}
	}
	repoAbs := absResolve(repo)
	current := Status(repoAbs)
	if current.Status != "in_progress" {
		return ReleaseResult{
			SlotID:          current.SlotID,
			Holder:          holder,
			Released:        true,
			AlreadyReleased: true,
		}, nil
	}
	if current.Holder != "" && current.Holder != holder {
		return ReleaseResult{}, &MergeSlotConflict{Msg: fmt.Sprintf(
			"refusing to release: slot held by '%s', not '%s'.", current.Holder, holder,
		)}
	}

	if err := refuseOnLiveDaemon(repoAbs); err != nil {
		return ReleaseResult{}, err
	}
	if runner == nil {
		runner = execwrap.Default
	}
	rc, _, errStr, _ := runner(
		[]string{"bd", "merge-slot", "release"}, repoAbs, 30*time.Second,
	)
	if rc != 0 {
		return ReleaseResult{}, &MergeSlotError{Msg: fmt.Sprintf(
			"bd merge-slot release failed: rc=%d err=%q",
			rc, strings.TrimSpace(errStr),
		)}
	}
	return ReleaseResult{
		SlotID:   current.SlotID,
		Holder:   holder,
		Released: true,
	}, nil
}

// CriticalSection acquires the slot, runs `body`, and releases on exit.
// Releases ONLY when the acquire wasn't a re-entrant self-claim (so
// callers can manage their own re-entrancy explicitly).
//
// Panics inside `body` propagate AFTER best-effort release. The Python
// version uses a try/finally; this is the Go equivalent.
func CriticalSection(repo, holder string, wait bool, runner BdRunner, body func() error) error {
	res, err := Acquire(repo, holder, wait, runner)
	if err != nil {
		return err
	}
	defer func() {
		if !res.AlreadyHeld {
			_, _ = Release(repo, holder, runner)
		}
	}()
	return body()
}

// --- internal helpers ---------------------------------------------------

func readSlotRecord(path string) map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if t, _ := rec["type"].(string); t == "merge-slot" {
			return rec
		}
	}
	return nil
}

func absResolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func stringOr(v any, dflt string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return dflt
}
