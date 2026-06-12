# Behavioral parity ledger — beekeeper-go vs Python beadkeeper

This file records:

1. The **test-by-test parity map** between Python `tests/test_*.py`
   (133 tests) and the equivalent Go tests under
   `cmd/bk/`, `internal/*/`, and `tools/diffharness/`.
2. The **diff harness contract**: what we compare byte-for-byte,
   what we deep-equal as JSON, and what we substring-match.
3. The **accepted divergence ledger**: cross-language deltas the
   harness ignores intentionally, with rationale.

The Go tests run under `go test -race -count=1 ./...`; the diff
harness runs under `go test ./tools/diffharness/...` with the
Python `beadkeeper` on `PATH` (or `BEADKEEPER_PY=<path>`).

---

## 1. Comparison contract

| Surface | What we compare | How |
|---|---|---|
| `doctor`, `board`, `guard db` text reports | severity tokens + structure | substring-match key tokens (`RED`, `db-in-filesync`, `Dropbox`, etc.) — ANSI-strip + path normalize is applied first |
| `--json` output (`doctor`, `board`, `guard db`, `lease list`, `merge-slot status`, `trunk-sync`) | shape + values | parse both, run `reflect.DeepEqual` after the key normalization in §4 |
| Refusal paths (daemon-alive, conflict, divergent, dirty-tree) | exit code + required keyword in stderr/stdout | exit code MUST match exactly; messages substring-match (`daemon`, `divergent`, `dirty`/`working tree`/`index`, `held by`, `REFUSE`, `bob`/etc.) |
| Idempotency / no-op paths (`lease claim` self, `merge-slot acquire` self, `lease release` unclaimed, `merge-slot release` open, `trunk-sync --apply` dry-run) | exit code + required keyword in stdout | exit code MUST match exactly; substring-match (`already held`, `already released`, `DRY-RUN`) |
| `--version` | both binaries exit 0 (text differs by design) | rc-only |

The harness lives at `tools/diffharness/harness_test.go`. Build the Go
binary up-front (out of any `t.TempDir()`), invoke both binaries
on the same fixture corpus, and compare per the table above.

### Fixture coverage (24 fixtures at M6 close)

| Subcommand | Cases |
|---|---|
| `doctor` | json (healthy), text (RED via DB-in-filesync) |
| `board` | json |
| `guard db` | clean, RED-inside-sync |
| `identity normalize` | live-daemon refusal (rc=3 + "daemon") |
| `version` | rc=0 |
| `lease list` | empty, active+stale (`age_seconds` ignored — wall-clock-volatile) |
| `lease claim` | self-idempotent (rc=0 + "already held"), conflict (rc=3 + "REFUSE" + "held by" + holder name) |
| `lease release` | non-holder (rc=3 + "REFUSE" + "held by"), unclaimed (rc=0 + "already released") |
| `merge-slot status` | missing, open, held — `--json` parity |
| `merge-slot acquire` | self-idempotent, conflict |
| `merge-slot release` | non-holder, when-open no-op |
| `trunk-sync` | scan clean, sync-branch-ahead, divergent-trunk-edit (rc=2), `--apply` dry-run |

**Bd-stub PATH override.** `trunk-sync` fixtures inject a no-op `bd`
shim at the front of `PATH` so neither binary's `bd config get`
shell-out reaches the real bd binary (which stack-overflows walking
`/tmp` symlinks on synthetic repos). With the shim returning rc=1,
both implementations fall back to reading `.beads/config.json` —
the documented fallback path. See `stubbedBdPath()` in the harness.

## 2. Test-by-test map (133 Python → Go)

Where one Go test covers multiple Python tests (e.g. a table-driven
case), it's listed once with all Python sources. Where a Python test
maps to no Go test 1:1 we record `not ported` with a reason.

### `tests/test_filesync.py` (6 → 8)

| Python | Go (`internal/filesync`) |
|---|---|
| `test_match_inside_synthetic_root` | `TestMatchInsideSyntheticRoot` |
| `test_match_outside_returns_none` | `TestMatchOutsideReturnsNil` |
| `test_longest_root_wins` | `TestLongestRootWins` |
| `test_known_roots_reads_env` | `TestKnownRootsReadsEnv` |
| `test_known_roots_skips_nonexistent` | `TestKnownRootsSkipsNonexistent` |
| `test_label_for_well_known_names` | `TestLabelForWellKnownNames` |
| (extra) | `TestMatchUsesLabelForWellKnownDropbox` |
| (extra) | `TestKnownRootsDedups` |

### `tests/test_guard.py` (6 → 6) — `internal/guard` ported in M4

| Python | Go (`internal/guard`) |
|---|---|
| `test_clean_project_has_no_findings` | `TestCleanProjectHasNoFindings` |
| `test_db_inside_filesync_is_red` | `TestDBInsideFilesyncIsRed` |
| `test_plan_fix_refuses_while_daemon_alive` | `TestPlanFixRefusesWhileDaemonAlive` |
| `test_plan_fix_dry_run_does_not_move` | `TestPlanFixDryRunDoesNotMove` |
| `test_plan_fix_apply_moves_files` | `TestPlanFixApplyMovesFiles` |
| `test_scan_paths_skips_heavy_dirs` | `TestScanPathsSkipsHeavyDirs` |

### `tests/test_hooks.py` (6 → 6)

| Python | Go (`internal/hooks`) |
|---|---|
| `test_install_writes_executable_hook` | `TestInstallWritesExecutableHook` |
| `test_install_is_idempotent` | `TestInstallIsIdempotent` |
| `test_install_refuses_foreign_hook` | `TestInstallRefusesForeignHook` |
| `test_uninstall_only_removes_our_hook` | `TestUninstallOnlyRemovesOurHook` |
| `test_prompt_indicator_renders_function` | `TestRenderPromptIndicator` |
| `test_install_on_non_git_dir` | `TestInstallOnNonGitDir` |

### `tests/test_cli.py` (7 → 7) — `cmd/bk`

| Python | Go (`cmd/bk/cli_test.go`) |
|---|---|
| `test_cli_doctor_json_clean` | `TestCLIDoctorJSONClean` |
| `test_cli_doctor_red_exits_two` | `TestCLIDoctorRedExitsTwo` |
| `test_cli_guard_db_clean` | `TestCLIGuardDBClean` |
| `test_cli_guard_db_detects_inside_sync` | `TestCLIGuardDBDetectsInsideSync` |
| `test_cli_guard_db_fix_dry_run` | `TestCLIGuardDBFixDryRun` |
| `test_cli_install_hooks_idempotent` | `TestCLIInstallHooksIdempotent` |
| `test_cli_prompt_indicator` | `TestCLIPromptIndicator` |

### `tests/test_daemons.py` (8 → 9)

| Python | Go (`internal/daemons`) |
|---|---|
| `test_list_workspace_daemons_filters_by_workspace` | `TestListWorkspaceDaemonsFiltersByWorkspace` |
| `test_duplicate_daemons_is_red` | `TestDuplicateDaemonsRed` |
| `test_single_daemon_no_log_failures_is_clean` | `TestSingleHealthyNoLog_IsClean` |
| `test_remote_helper_failures_is_red` | `TestRemoteHelperFailuresRed` |
| `test_orphan_state_files_without_daemon_is_yellow` | `TestOrphanStateYellow` |
| `test_doctor_red_on_duplicate_daemons` | `TestDuplicatesAreRedAtModuleLevel` (per-pkg signal) + doctor wiring covered transitively |
| `test_doctor_red_on_log_remote_helper_failure` | `TestRemoteHelperFailureRedAtModuleLevel` (per-pkg signal) |
| `test_scan_does_not_invoke_start_or_stop` | `TestScanDoesNotInvokeStartOrStop` |
| (extra) | `TestRemoteHelperBelowThresholdIsSilent` |
| (extra) | `TestNoOrphanWhenPidFilePresent` |
| (extra) | `TestCountRemoteHelperFailures` |
| (extra) | `TestCountRemoteHelperFailuresMissing` |
| (extra) | `TestScanAggregates` |

### `tests/test_doctor.py` (14 → 14)

| Python | Go (`internal/doctor/{doctor_test.go,wiring_test.go}`) |
|---|---|
| `test_healthy_project_is_green` | `TestHealthyProjectNotRed` |
| `test_db_in_filesync_makes_doctor_red` | `TestDBInFilesyncRed` |
| `test_dead_daemon_pid_is_red` | `TestStaleDaemonPIDRed` |
| `test_dead_pid_red_even_when_bd_self_heals_pid_file` | `TestDeadPIDRedEvenWhenBdSelfHealsPidFile` |
| `test_needs_manual_sync_is_red` | `TestNeedsManualSyncWithAlivePIDAlsoRed` (with-pid variant) |
| `test_needs_manual_sync_without_pid_is_red` | `TestNeedsManualSyncWithoutPidRed` |
| `test_five_consecutive_failures_without_pid_is_red` | `TestFiveFailuresRed` |
| `test_one_consecutive_failure_without_pid_is_yellow` | `TestOneFailureYellow` |
| `test_last_sync_age_over_24h_is_yellow` | `TestLastSyncAgeOver24hYellow` |
| `test_bd_config_not_set_string_is_yellow` | `TestBdConfigNotSetStringIsYellow` |
| `test_sync_branch_set_is_green` | `TestSyncBranchSetIsGreen` |
| `test_empty_sync_branch_is_yellow` | `TestEmptySyncBranchYellow` |
| `test_json_output_is_valid_and_has_worst` | `TestRenderJSONShape` |
| `test_no_projects_renders_message` | `TestRenderTextEmptyAndProjects` |
| (extra: exit code contract) | `TestExitCodeContract` (table-driven) |
| (extra) | `TestAliveDaemonPIDGreen` + `TestMissingJSONLYellow` |

### `tests/test_identity.py` (10 → 10)

| Python | Go |
|---|---|
| `test_load_config_returns_canonical_and_aliases` | `internal/config.TestLoadIdentityConfigPresent` |
| `test_load_config_missing_returns_none` | `internal/config.TestLoadIdentityConfigMissing` |
| `test_canonical_only_is_clean` | `internal/identity.TestScanCanonicalOnlyIsClean` |
| `test_mapped_alias_is_flagged_normalizable` | `internal/identity.TestScanFlagsAliasAndUnmapped` |
| `test_unmapped_handle_is_flagged` | `internal/identity.TestScanFlagsAliasAndUnmapped` (same) + `TestScanNoConfigEmitsEverythingAsUnmapped` |
| `test_normalize_dry_run_is_default_and_does_not_write` | `internal/identity.TestPlanNormalizeIsDryRun` + `TestNormalizeDryRunDoesNotWrite` |
| `test_normalize_apply_rewrites_jsonl` | `internal/identity.TestNormalizeApplyRewritesJSONL` |
| `test_normalize_refuses_when_daemon_alive` | `internal/identity.TestNormalizeApplyRefusesLiveDaemon` |
| `test_doctor_yellow_on_identity_drift` | `internal/doctor.TestDoctorYellowOnIdentityDrift` |
| `test_doctor_no_identity_check_without_config` | `internal/doctor.TestDoctorNoIdentityCheckWithoutConfig` |

### `tests/test_lease.py` (16 → 17)

| Python | Go (`internal/lease`) |
|---|---|
| `test_resolve_caller_returns_canonical_for_alias` | `TestResolveCallerAlias` |
| `test_resolve_caller_passthrough_canonical` | `TestResolveCallerCanonical` |
| `test_resolve_caller_raises_on_unmapped_handle` | `TestResolveCallerUnmappedErrs` |
| `test_resolve_caller_raises_when_identity_not_configured` | `TestResolveCallerNoConfigErrs` |
| `test_claim_unclaimed_issues_bd_update_call` | `TestClaimUnclaimedCallsBd` |
| `test_claim_self_idempotent_does_not_re_call_bd` | `TestClaimSelfIdempotentNoCall` |
| `test_claim_on_other_held_is_rejected` | `TestClaimByOtherIsConflict` |
| `test_claim_refuses_when_daemon_alive` | `TestClaimRefusesLiveDaemon` |
| `test_claim_unknown_issue_id_raises` | `TestClaimUnknownIssueErrs` |
| `test_release_by_holder_succeeds` | `TestReleaseByHolder` |
| `test_release_by_non_holder_refused` | `TestReleaseByNonHolderConflict` |
| `test_release_unclaimed_is_noop` | `TestReleaseUnclaimedIsNoop` |
| `test_list_returns_active_leases` | `TestListReturnsActive` |
| `test_list_flags_stale_leases` | `TestListFlagsStale` |
| `test_doctor_yellow_on_stale_leases` | `internal/doctor.TestDoctorYellowOnStaleLeases` |
| `test_doctor_no_lease_row_when_no_active_leases` | `internal/doctor.TestDoctorNoLeaseRowWhenNoActiveLeases` |
| (extra) | `TestReleaseRefusesLiveDaemon` |

### `tests/test_mergeslot.py` (13 → 13)

| Python | Go (`internal/mergeslot`) |
|---|---|
| `test_status_missing_when_slot_not_created` | `TestStatusMissing` |
| `test_status_open_when_no_holder` | `TestStatusOpen` |
| `test_status_held_returns_holder` | `TestStatusHeld` |
| `test_acquire_open_slot_succeeds` | `TestAcquireOpenSlotSucceeds` |
| `test_second_acquire_by_other_holder_is_refused` | `TestAcquireConflictRefused` |
| `test_acquire_self_idempotent` | `TestAcquireSelfIdempotentNoCall` |
| `test_acquire_refuses_when_daemon_alive` | `TestAcquireRefusesLiveDaemon` |
| `test_release_by_holder_succeeds` | `TestReleaseByHolder` |
| `test_release_by_non_holder_refused` | `TestReleaseNonHolderConflict` |
| `test_release_when_open_is_noop` | `TestReleaseWhenOpenIsNoop` |
| `test_critical_section_acquires_then_releases` | `TestCriticalSectionAcquiresThenReleases` |
| `test_critical_section_releases_on_exception` | `TestCriticalSectionReleasesOnError` |
| `test_critical_section_refused_when_held_by_other` | `TestCriticalSectionRefusedWhenHeldByOther` |

### `tests/test_syncbranch.py` (11 → 11)

| Python | Go (`internal/syncbranch`) |
|---|---|
| `test_guard_clean_no_deltas_on_non_sync_branch` | `TestCleanNoStrandedCommits` |
| `test_guard_empty_sync_branch_is_yellow` | `TestEmptySyncBranchIsYellow` |
| `test_guard_bead_deltas_on_feature_branch_is_red` | `TestStrandedCommitOnFeatureBranchIsRed` |
| `test_guard_main_branch_with_bead_commits_is_red` | `TestStrandedCommitOnMainBranchIsRed` |
| `test_guard_trunk_subset_of_sync_is_not_stranded` | `TestContentSubsetSuppressesStranding` |
| `test_guard_trunk_identical_to_sync_is_not_stranded` | `TestTrunkIdenticalToSyncIsNotStranded` |
| `test_guard_trunk_stale_record_is_not_stranded` | `TestTrunkStaleRecordIsNotStranded` |
| `test_set_writes_config_json` | `TestSetBranchWritesConfigJSON` |
| `test_set_refuses_with_live_daemon` | `TestSetBranchRefusesLiveDaemon` |
| `test_doctor_red_when_stranded_bead_commits` | `TestDoctorSignalRedWhenStranded` (per-pkg signal) |
| `test_doctor_green_when_no_stranding` | `TestDoctorSignalGreenWhenClean` (per-pkg signal) |
| (extra) | `TestStrandedMessageFormat`, `TestMissingSyncBranchIsYellow`, `TestSetBranchRefusesNonWorkspace` |

### `tests/test_trunksync.py` (15 → 15)

| Python | Go (`internal/trunksync`) |
|---|---|
| `test_scan_clean_when_no_drift` | `TestScanClean` |
| `test_scan_detects_sync_branch_ahead` | `TestScanSyncBranchAhead` |
| `test_scan_detects_divergent_trunk_edit_is_red` | `TestScanDivergentTrunkEdit` |
| `test_scan_trunk_subset_of_sync_is_not_divergent` | `TestScanTrunkSubsetOfSyncIsNotDivergent` |
| `test_scan_trunk_stale_record_is_not_divergent` | `TestScanTrunkStaleRecordIsNotDivergent` |
| `test_apply_proceeds_when_trunk_subset_of_sync` | `TestApplyProceedsWhenTrunkSubsetOfSync` |
| `test_scan_missing_sync_branch_is_yellow` | `TestScanMissingSyncBranchYellow` |
| `test_apply_dry_run_does_not_mutate` | `TestApplyDryRunNoMutation` |
| `test_apply_fast_forwards_clean_delta` | `TestApplyFastForwardsCleanDelta` |
| `test_apply_is_idempotent` | `TestApplyIdempotent` |
| `test_apply_refuses_on_divergent_trunk_edit` | `TestApplyRefusesOnDivergentTrunkEdit` |
| `test_apply_refuses_when_daemon_alive` | `TestApplyRefusesLiveDaemon` |
| `test_apply_refuses_with_dirty_working_tree` | `TestApplyRefusesDirtyTree` |
| `test_doctor_yellow_on_sync_branch_drift` | `TestDoctorYellowOnSyncBranchDrift` (per-pkg) |
| `test_doctor_red_on_divergent_trunk_edit` | `TestDoctorRedOnDivergentTrunkEdit` (per-pkg) |

### `tests/test_board.py` (21 → 21)

| Python | Go |
|---|---|
| `test_open_no_deps_is_ready` | `internal/board.TestOpenNoDepsIsReady` |
| `test_open_blocked_by_open_dep_is_blocked` | `internal/board.TestBlockedByOpenDep` |
| `test_unblocked_when_dep_closed` | `internal/board.TestUnblockedWhenDepClosed` |
| `test_parent_child_does_not_block` | `internal/board.TestParentChildIsNotBlocking` |
| `test_in_progress_with_assignee_no_lease_gap` | `internal/board.TestInProgressWithAssigneeNoLeaseGap` |
| `test_in_progress_without_assignee_is_lease_gap` | `internal/board.TestInProgressLeaseGap` |
| `test_closed_excluded_but_counted` | `internal/board.TestClosedExcludedButCounted` |
| `test_unresolved_blocker_is_non_blocking` | `internal/board.TestUnresolvedBlockerIsNonBlocking` |
| `test_multiple_blocks_blocked_if_any_open` | `internal/board.TestMultipleBlocksBlockedIfAnyOpen` |
| `test_status_blocked_string_is_blocked` | `internal/board.TestStatusBlockedString` |
| `test_unknown_status_omitted_from_buckets` | `internal/board.TestUnknownStatusGoesToOther` |
| `test_cross_project_aggregation_and_ordering` | `internal/board.TestCrossProjectOrderingAndPriority` |
| `test_malformed_lines_are_skipped` | `internal/board.TestMalformedLinesSkipped` |
| `test_empty_jsonl_is_safe` | `internal/board.TestEmptyJSONLSafe` |
| `test_missing_jsonl_is_safe` | `internal/board.TestMissingJSONLSafe` |
| `test_cli_json_shape` | `cmd/bk.TestCLIBoardJSONShape` |
| `test_cli_status_filter_ready_only` | `cmd/bk.TestCLIBoardStatusFilterReadyOnly` |
| `test_cli_strict_exits_one_on_lease_gaps` | `cmd/bk.TestCLIBoardStrictExitsOneOnGaps` |
| `test_cli_strict_zero_when_no_gaps` | `cmd/bk.TestCLIBoardStrictZeroWhenNoGaps` |
| `test_cli_lease_gaps_only` | `cmd/bk.TestCLIBoardLeaseGapsOnly` |
| `test_cli_quiet_silences_empty_message` | `cmd/bk.TestCLIBoardQuietSilencesEmptyMessage` |

---

## 3. Tests that don't port 1:1

None. Every Python test has at least one Go counterpart.

A handful of Python tests target Python-only mechanics (`PlanNormalize`
existed only in M2 because Go was staged differently). Those still
have a Go equivalent that exercises the same external contract — just
under different names and packages.

## 4. Accepted divergence ledger

Recorded by the harness so deep-equal succeeds even when the
implementations differ idiomatically. None of these affect behavior
the user observes — they're language-level conveniences.

| # | Surface | Python | Go | Reason |
|---|---|---|---|---|
| 1 | JSON missing scalar fields | `null` | `""` / `0` | Go emits zero-values via `json.Marshal`; Python emits `None`. Functionally indistinguishable. Harness treats them as equivalent. |
| 2 | JSON empty collections | `[]` | `null` (nil slice) | `json.Marshal(nil)` yields `null` in Go. Python emits `[]`. Harness treats both as "no items". |
| 3 | JSON debug fields on `ProjectHealth` | `daemon: {...}`, `git: {...}` | (omitted) | Python attaches raw daemon/git state to the JSON for debugging; Go's contract is the `checks` array. Harness drops both keys. |
| 4 | Remediation messages | `\`beadkeeper guard sync-branch --set\`` | `\`bk guard sync-branch --set\`` | The two binaries have different names. Same instruction, different binary. Harness rewrites Python form -> Go form. |
| 5 | Run-volatile JSON keys | `generated_at`, `scanned_paths` | (same fields, different values per run) | Wall-clock time. Harness drops both. |
| 6 | Daemon-pid in error messages | `bd daemon (pid 12345)` | (same) | The host pid varies per test. Harness collapses `pid <N>` -> `pid <PID>`. |
| 7 | Path resolution | `/tmp/...` | `/private/tmp/...` (macOS resolved symlink) | macOS' `/tmp` is a symlink to `/private/tmp`. Harness collapses both. |
| 8 | Refusal error wording | "refusing to apply: ... is alive for /...; stop the daemon and re-run" (Go) vs "...is alive. Stop the daemon and re-run." (Python) | trailing punctuation, capitalization | `golangci-lint`'s `revive.error-strings` rule. Harness substring-matches `daemon`, `divergent`, `dirty`, `working tree`, `index`, `conflict`. |
| 9 | `bk board --json` aggregate summary | (no aggregate block; flat `projects` + `totals`) | adds `summary` (total / by_status / active_by_priority / percent_complete / in_progress_count / lease_gaps_count / stale_wip_count) plus per-project `by_status` / `active_by_priority` / `total` | bkg-bqa.1: agents need backlog rollups. Go-only forward feature; the existing `projects` / `totals` keys are unchanged. Harness drops the new keys via `requireJSONDeepEqualIgnoring`. |
| 10 | `bk doctor` stale-wip check | (no such check) | adds YELLOW `stale-wip` row when an in_progress bead's `updated_at` is older than `--stale-days` (default 7d) | bkg-bqa.2: a real 627-bead review found 22 of 33 in_progress beads untouched >7d (oldest 26d). Go uses `time.RFC3339Nano` so TZ-offset + fractional-second timestamps (which jq's `fromdateiso8601` rejects) parse correctly. Go-only forward feature; silent on clean repos so doctor parity isn't broken. |
| 11 | `bk guard stale-beads` | (no such command) | new subcommand: scans merged commit subjects on the default branch for bead-id tokens and reports open / in_progress beads whose work already shipped | bkg-bqa.3 + bkg-td0.{1,2,3} hardening: post-merge sibling of `pr-beads`. Same Go-only-divergence rationale (CI-shaped feature; no Python counterpart). Match precision contract: subject-only + token-bounded + (conventional-scope OR `(#N)` PR-merge marker) + implementation-type allowlist (feat/fix/perf/refactor) + bd/beads/spec exclusion. Status source: bd-authoritative merge with JSONL fallback (`--source auto`). |
| 12 | `bk board` JSONL-only status read | (same — JSONL only) | (same — JSONL only) | **Known divergence still pending**: `bk guard stale-beads` reads bd's authoritative status by default (bkg-td0.2) but `bk board` still parses JSONL only. JSONL lags the bd SQLite DB after a `bd close`, so a freshly-closed bead can still appear under "in_progress" or "ready" in `bk board` until bd flushes. Tracked but not in scope for bkg-td0.2. |
| 13 | `bk doctor --gate` cheap drift scan | (no such mode) | new mode: cheap rev-list-count + sync.branch + needs_manual_sync read; meant to run in pre-commit | bkg-59b: drift originates at COMMIT time (bd auto-commits on stale branch) but the existing pr-beads / trunk-sync guards fire only at PUSH time, leaving an unguarded window. Go-only forward feature; the cheap path is intentional to avoid taxing every commit with a full pr-beads scan. |
| 14 | `bk doctor` bk-hooks check | (no such check) | YELLOW row when bk's pre-push or pre-commit drift-guard hooks are missing (distinguishing from bd's flush-only pre-commit by `# bk-managed: pre-commit drift-guard v1` marker) | bkg-6h7: an unprotected repo had no signal that bk's hooks weren't installed. Pairs with bkg-59b's hook installer; harness drops `bk-hooks` from the Go-only-checks set so doctor parity stays clean. |
| 15 | `bk doctor` jsonl-freshness check (+ board banner, guard beadspec hint) | (no such check) | YELLOW row when a `.beads/*.db` (or its `-wal` sidecar) mtime is newer than `issues.jsonl`; `bk board` prints a per-project stale banner and `bk guard beadspec` appends a `bd sync` hint to FAILING output | bkg-ckb: bd writes mutations to its sqlite DB immediately but exports DB→JSONL on a debounce, so bk surfaces report stale state right after `bd update` (the row-12 divergence). Read-side detection only — bk never auto-flushes. Harness drops `jsonl-freshness` from the parity surface. |

## 5. Running

```bash
# Build + unit tests (no Python dependency).
go test -race -count=1 ./...

# Diff harness — needs Python beadkeeper on PATH or BEADKEEPER_PY=...
BEADKEEPER_PY=/path/to/beadkeeper go test -count=1 -v ./tools/diffharness/...
```

The harness skips with a clear message when the Python tool isn't
findable, so CI without Python doesn't false-fail.

## 6. Results

- **Python tests counted:** 133 (12 files).
- **Go tests at M6 close:** ~210 (`grep -hE '^func Test' cmd/bk/*.go internal/*/*.go tools/*/*.go`).
- **Diff harness corpus:** **24 fixtures** across doctor (json + text), board (json), guard db (clean + RED), identity (live-daemon refuse), version, lease (list × 2 + claim × 2 + release × 2), merge-slot (status × 3 + acquire × 2 + release × 2), trunk-sync (scan × 3 + apply dry-run).
- **Harness verdict:** ZERO divergence under the contract above, with the cross-language deltas in §4 normalized away. Every fixture passes locally with `BEADKEEPER_PY=/Users/.../beadkeeper`.

## 7. M6 follow-up: coordination harness coverage (bkg-9cs.7)

M4 left a gap — the harness omitted lease, merge-slot, and trunk-sync
fixtures. M6 closed it with the 12 new fixtures listed in §1. The
PARITY.md §1 contract now reflects the actual coverage rather than
asserted coverage.

## 8. Go-only divergence: `bk guard pr-beads` (bkg-lrc)

`bk guard pr-beads` is **Go-only**. It has no counterpart in the
Python `beadkeeper` tool, and we are NOT porting it back — this is a
documented forward divergence rather than a parity gap.

Rationale:

  - The motivating incident is CI-shaped: the GitHub merge UI flagged
    mergeability UNKNOWN on a PR carrying a stale JSONL snapshot. The
    natural place to gate that is a CI step running a single static
    binary, not a Python interpreter + venv.
  - The Python tool's release surface is contracting (the M5 cutover
    moved the public install path to `bk` via Homebrew). Adding new
    functionality to the Python tool would widen a surface we want
    narrower.
  - `bk guard pr-beads`'s detection logic is content-aware (see the
    package doc-comment in `internal/prbeads`). The Python
    `syncbranch` module's subset check is id-only and explicitly
    assumes "bead edits only ever originate on the sync branch."
    Porting the new logic into Python would either duplicate the
    `syncbranch` code path or introduce a second module — both worse
    than living with one Go-only feature.

The diff harness covers this divergence by **omission**: there are no
pr-beads fixtures in the harness because there is no Python side to
compare against. The `regressions()` table-driven tests in
`internal/prbeads/prbeads_test.go` are the regression bar.

If a future operator decides to bring the Python tool back to feature
parity, the algorithm to port is documented in
`internal/prbeads/prbeads.go`'s package doc-comment:
status-rank table (`open=0, in_progress=1, blocked=1, closed=2`),
assignee-equality check, RFC-3339 `updated_at` comparison, dropped-id
check; combined under a `regression` policy, plus the strict
`no-beads` policy (any `.beads/issues.jsonl` modification in
`base..head` fails). Until then, the Go binary is the source of
truth.
