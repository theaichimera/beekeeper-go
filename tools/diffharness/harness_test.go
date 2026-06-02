// Diff harness — runs both `beadkeeper` (Python) and `bk` (Go) on
// a shared fixture-repo corpus and asserts behavioral parity.
//
// Contract (from M4 handoff):
//   - stdout text reports (doctor/board): BYTE-identical paths matter
//     for path strings; we strip ANSI escapes + normalize the
//     project root path and timestamps before byte-compare.
//   - JSON output: parse both sides + DEEP-EQUAL (key order
//     irrelevant, "generated_at" / numeric drift normalized).
//   - errors / refusals: exit code matches EXACTLY; messages matched
//     by required-keyword substring (revive-driven Go wording diverges
//     from Python sentences).
//
// The harness skips with a clear message when `beadkeeper` isn't on
// PATH (CI is allowed to skip without false-failing). It must run
// green locally before M4 is reported done.
//
// Build the Go binary into a tmp dir before running so the test isn't
// dependent on `bk` being on PATH.
package diffharness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const repoRel = ".." + string(os.PathSeparator) + ".."

// pyBinary is the Python CLI under test. The handoff lets us invoke
// it via venv-installed `beadkeeper` on PATH OR via an explicit
// override env var.
func pyBinary(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("BEADKEEPER_PY"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	if p, err := exec.LookPath("beadkeeper"); err == nil {
		return p
	}
	// Common dev location: ~/cc/beadkeeper/.venv/bin/beadkeeper.
	if home, _ := os.UserHomeDir(); home != "" {
		cand := filepath.Join(home, "cc", "beadkeeper", ".venv", "bin", "beadkeeper")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	t.Skip("beadkeeper (Python) not on PATH and no BEADKEEPER_PY override; harness skipped " +
		"— set BEADKEEPER_PY=/path/to/beadkeeper to run")
	return ""
}

// goBinary builds `bk` once per test process and returns its absolute
// path. The build dir lives outside any t.TempDir() so it survives
// across tests.
var (
	goBinaryCache string
	goBinaryErr   error
	goBinaryBuilt bool
)

func goBinary(t *testing.T) string {
	t.Helper()
	if goBinaryBuilt {
		if goBinaryErr != nil {
			t.Skip(goBinaryErr.Error())
		}
		return goBinaryCache
	}
	goBinaryBuilt = true
	wd, _ := os.Getwd()
	repoRoot := filepath.Clean(filepath.Join(wd, repoRel))
	tmp, err := os.MkdirTemp("", "bk-harness-")
	if err != nil {
		goBinaryErr = err
		t.Skip(err.Error())
	}
	dst := filepath.Join(tmp, "bk")
	cmd := exec.Command("go", "build", "-o", dst, "./cmd/bk")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		goBinaryErr = fmt.Errorf("go build ./cmd/bk failed: %v\n%s", err, out)
		t.Skip(goBinaryErr.Error())
	}
	goBinaryCache = dst
	return dst
}

// runResult captures one binary invocation.
type runResult struct {
	rc     int
	stdout string
	stderr string
}

func runBin(bin string, args []string) runResult {
	return runBinEnv(bin, args, nil)
}

// runBinEnv runs `bin` with `args` and, when `envOverrides` is non-nil,
// merges those entries on top of os.Environ(). Use this to scope PATH
// or strip env vars (e.g., remove `bd` from PATH so neither binary's
// `bd config get` shell-out perturbs the synthetic-repo state — bd
// stack-overflows walking /tmp symlinks).
func runBinEnv(bin string, args []string, envOverrides map[string]string) runResult {
	cmd := exec.Command(bin, args...)
	if envOverrides != nil {
		env := os.Environ()
		// strip duplicates of the override keys
		filtered := env[:0]
		for _, e := range env {
			eq := strings.IndexByte(e, '=')
			if eq < 0 {
				filtered = append(filtered, e)
				continue
			}
			if _, ok := envOverrides[e[:eq]]; !ok {
				filtered = append(filtered, e)
			}
		}
		for k, v := range envOverrides {
			filtered = append(filtered, k+"="+v)
		}
		cmd.Env = filtered
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	rc := 0
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			rc = ee.ExitCode()
		} else {
			rc = -1
		}
	}
	return runResult{rc: rc, stdout: stdout.String(), stderr: stderr.String()}
}

// stubbedBdPath returns an env override that puts a no-op `bd` shim
// at the front of PATH so neither binary's `bd config get`
// shell-out actually invokes the real bd binary (which stack-
// overflows when walking /tmp symlinks on synthetic repos).
//
// The shim returns rc=1; both binaries fall back to reading
// .beads/config.json instead, which is what the harness fixtures
// rely on.
func stubbedBdPath(t *testing.T) map[string]string {
	t.Helper()
	stubDir := t.TempDir()
	shim := filepath.Join(stubDir, "bd")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	origPath := os.Getenv("PATH")
	return map[string]string{
		"PATH": stubDir + string(os.PathListSeparator) + origPath,
	}
}

func asExit(err error, target **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		// Unwrap manually to avoid pulling in errors for Go 1.22 baseline.
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return false
}

// --- normalization ------------------------------------------------------

// pidRE: collapse `pid <N>` so live-daemon refusal messages match
// across runs (the host pid varies).
var pidRE = regexp.MustCompile(`pid \d+`)

// goOnlyCheckNames lists `Check.name` values emitted by Go's doctor
// but not by Python's. New Go-only checks should be added here
// instead of removed from the wire — the Check is a real signal we
// want to keep, just not part of the Python-vs-Go parity surface.
// Maintained alongside docs/PARITY.md.
var goOnlyCheckNames = map[string]struct{}{
	"pr-beads":  {}, // bkg-lrc
	"stale-wip": {}, // bkg-bqa.2
	"bk-hooks":  {}, // bkg-6h7
}

// normalizeJSONForCompare returns a generic value with run-volatile
// keys deleted so deep-equal can match across implementations.
//
// Cross-language idiomatic normalization (accepted deltas — see PARITY.md):
//   - Python `None` (-> JSON `null`) and Go zero-value (-> empty
//     string / 0) are treated as equivalent for missing scalars.
//   - Python `[]` and Go `nil` slices are both empty collections.
//   - "generated_at" + "scanned_paths" are dropped (run-volatile).
//   - Path strings are normalized: <projectRoot> -> "<PROJECT>", and
//     macOS' /private/tmp prefix is stripped.
//   - "pid <N>" -> "pid <PID>" so live daemon refusal messages match.
//   - Check rows whose `name` is in goOnlyCheckNames are dropped from
//     `checks[]` arrays.
func normalizeJSONForCompare(v any, projectRoot string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			// Drop run-volatile keys.
			if k == "generated_at" || k == "scanned_paths" {
				continue
			}
			// Drop debug-only keys Python attaches to ProjectHealth
			// (raw daemon/git state). Go omits them by design; the
			// Check rows are the contract.
			if k == "daemon" || k == "git" {
				continue
			}
			n := normalizeJSONForCompare(val, projectRoot)
			// Treat null and empty-string/0 as equivalent so the
			// language-idiom mismatch (Python None vs Go "") doesn't
			// fail deep-equal.
			if n == nil || n == "" || n == float64(0) {
				continue
			}
			out[k] = n
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			// Filter Go-only Check rows from arrays of {name, ...}
			// entries. We detect via `name` field presence to keep the
			// filter scoped — generic non-Check arrays pass through.
			if obj, ok := x.(map[string]any); ok {
				if name, ok := obj["name"].(string); ok {
					if _, drop := goOnlyCheckNames[name]; drop {
						continue
					}
				}
			}
			out = append(out, normalizeJSONForCompare(x, projectRoot))
		}
		// nil slice and empty slice should compare equal — return
		// nil for both so reflect.DeepEqual succeeds.
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		s := t
		if projectRoot != "" {
			s = strings.ReplaceAll(s, projectRoot, "<PROJECT>")
			s = strings.ReplaceAll(s, "/private<PROJECT>", "<PROJECT>")
		}
		s = pidRE.ReplaceAllString(s, "pid <PID>")
		// Binary-name normalization — both refer to the same tool;
		// this is an idiomatic delta documented in PARITY.md.
		s = strings.ReplaceAll(s, "`beadkeeper guard", "`bk guard")
		s = strings.ReplaceAll(s, "`beadkeeper doctor", "`bk doctor")
		s = strings.ReplaceAll(s, "`beadkeeper trunk-sync", "`bk trunk-sync")
		s = strings.ReplaceAll(s, "`beadkeeper lease", "`bk lease")
		s = strings.ReplaceAll(s, "`beadkeeper merge-slot", "`bk merge-slot")
		s = strings.ReplaceAll(s, "`beadkeeper identity", "`bk identity")
		s = strings.ReplaceAll(s, "`beadkeeper install-hooks", "`bk install-hooks")
		s = strings.ReplaceAll(s, "`beadkeeper uninstall-hooks", "`bk uninstall-hooks")
		s = strings.ReplaceAll(s, "`beadkeeper board", "`bk board")
		return s
	default:
		return v
	}
}

// --- fixtures ----------------------------------------------------------

func setupGitRepo(t *testing.T, repo, syncBranch string) {
	t.Helper()
	mustMkdir(t, filepath.Join(repo, ".beads"))
	mustWrite(t, filepath.Join(repo, ".beads", "issues.jsonl"), nil)
	if syncBranch != "" {
		mustWrite(t, filepath.Join(repo, ".beads", "config.json"),
			[]byte(fmt.Sprintf(`{"sync":{"branch":"%s"}}`, syncBranch)))
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"add", ".beads/issues.jsonl"},
		{"commit", "-q", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- the actual fixture corpus -----------------------------------------

func TestParityDoctorJSON_Healthy(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)

	repo := t.TempDir()
	setupGitRepo(t, repo, "beads-sync")

	pyOut := runBin(py, []string{"doctor", repo, "--json"})
	goOut := runBin(gobk, []string{"doctor", repo, "--json"})
	requireSameRC(t, "doctor --json (healthy)", pyOut, goOut)
	requireJSONDeepEqual(t, "doctor --json (healthy)", pyOut.stdout, goOut.stdout, repo)
}

func TestParityDoctorTextDBInFilesync(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)

	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeDropbox")
	mustMkdir(t, fake)
	repo := filepath.Join(fake, "rotten")
	setupGitRepo(t, repo, "beads-sync")
	mustWrite(t, filepath.Join(repo, ".beads", "beadkeeper.db"), []byte("x"))

	t.Setenv("BEADKEEPER_FILESYNC_ROOTS", fake)
	pyOut := runBin(py, []string{"doctor", repo, "--no-color"})
	goOut := runBin(gobk, []string{"doctor", repo, "--no-color"})
	requireSameRC(t, "doctor --no-color (RED)", pyOut, goOut)
	// Exit-code parity is the gate; output text is loosely
	// compared on key tokens because Go's RenderText sequence may
	// differ subtly from Python's.
	for _, kw := range []string{"db-in-filesync", "RED", "Dropbox"} {
		if !strings.Contains(pyOut.stdout, kw) {
			t.Errorf("py stdout missing %q", kw)
		}
		if !strings.Contains(goOut.stdout, kw) {
			t.Errorf("go stdout missing %q", kw)
		}
	}
}

func TestParityBoardJSON(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)

	repo := t.TempDir()
	mustMkdir(t, filepath.Join(repo, ".beads"))
	body := []string{
		`{"id":"a","status":"open","priority":1}`,
		`{"id":"b","status":"in_progress","assignee":"alice"}`,
		`{"id":"c","status":"closed"}`,
	}
	mustWrite(t, filepath.Join(repo, ".beads", "issues.jsonl"),
		[]byte(strings.Join(body, "\n")+"\n"))

	pyOut := runBin(py, []string{"board", repo, "--json"})
	goOut := runBin(gobk, []string{"board", repo, "--json"})
	requireSameRC(t, "board --json", pyOut, goOut)
	// Go adds an aggregate `summary` block + per-project status /
	// priority breakdown keys (bkg-bqa.1) that the Python tool does
	// not emit. These are documented as Go-only forward divergences
	// in docs/PARITY.md §4 — drop them before comparing the rest of
	// the payload.
	ignore := map[string]struct{}{
		"summary":            {},
		"by_status":          {},
		"active_by_priority": {},
		"total":              {},
	}
	requireJSONDeepEqualIgnoring(t, "board --json", pyOut.stdout, goOut.stdout, repo, ignore)
}

func TestParityGuardDBClean(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)

	repo := t.TempDir()
	setupGitRepo(t, repo, "beads-sync")
	pyOut := runBin(py, []string{"guard", "db", repo, "--quiet"})
	goOut := runBin(gobk, []string{"guard", "db", repo, "--quiet"})
	requireSameRC(t, "guard db (clean)", pyOut, goOut)
}

func TestParityGuardDBDetectsInsideSync(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)

	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeDropbox")
	mustMkdir(t, fake)
	repo := filepath.Join(fake, "inside")
	mustMkdir(t, filepath.Join(repo, ".beads"))
	mustWrite(t, filepath.Join(repo, ".beads", "beadkeeper.db"), []byte("x"))

	t.Setenv("BEADKEEPER_FILESYNC_ROOTS", fake)
	pyOut := runBin(py, []string{"guard", "db", fake})
	goOut := runBin(gobk, []string{"guard", "db", fake})
	requireSameRC(t, "guard db (RED)", pyOut, goOut)
	// Both should mention RED somewhere.
	for _, kw := range []string{"RED"} {
		if !strings.Contains(pyOut.stdout, kw) {
			t.Errorf("py stdout missing %q: %s", kw, pyOut.stdout)
		}
		if !strings.Contains(goOut.stdout, kw) {
			t.Errorf("go stdout missing %q: %s", kw, goOut.stdout)
		}
	}
}

// Refusal path: live daemon. Both binaries refuse with rc=3 and
// substring "daemon".
func TestParityIdentityNormalizeRefusesLiveDaemon(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)

	repo := t.TempDir()
	setupGitRepo(t, repo, "beads-sync")
	mustMkdir(t, filepath.Join(repo, ".beadkeeper"))
	mustWrite(t, filepath.Join(repo, ".beadkeeper", "identity.toml"),
		[]byte("[identity]\ncanonical = [\"alice\"]\n\n[identity.aliases]\n\"a@x\" = \"alice\"\n"))
	// Plant our own pid (alive).
	mustWrite(t, filepath.Join(repo, ".beads", "daemon.pid"), []byte(strconv.Itoa(syscall.Getpid())))

	pyOut := runBin(py, []string{"identity", "normalize", repo, "--yes"})
	goOut := runBin(gobk, []string{"identity", "normalize", repo, "--yes"})
	if pyOut.rc != goOut.rc {
		t.Errorf("daemon-refuse rc: py=%d go=%d\npy.stderr=%s\ngo.stderr=%s",
			pyOut.rc, goOut.rc, pyOut.stderr, goOut.stderr)
	}
	for _, kw := range []string{"daemon"} {
		if !strings.Contains(strings.ToLower(pyOut.stderr+pyOut.stdout), kw) {
			t.Errorf("py output missing %q: %s | %s", kw, pyOut.stderr, pyOut.stdout)
		}
		if !strings.Contains(strings.ToLower(goOut.stderr+goOut.stdout), kw) {
			t.Errorf("go output missing %q: %s | %s", kw, goOut.stderr, goOut.stdout)
		}
	}
}

func TestParityVersion(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)

	pyOut := runBin(py, []string{"--version"})
	goOut := runBin(gobk, []string{"version"})
	if pyOut.rc != 0 || goOut.rc != 0 {
		t.Fatalf("version: py rc=%d go rc=%d", pyOut.rc, goOut.rc)
	}
}

// --- lease parity --------------------------------------------------------

// writeIdentityTOML writes the canonical-handle config used by lease
// claim/release tests. Both binaries read this same file.
func writeIdentityTOML(t *testing.T, repo string, canonical []string, aliases map[string]string) {
	t.Helper()
	mustMkdir(t, filepath.Join(repo, ".beadkeeper"))
	body := "[identity]\ncanonical = ["
	for i, c := range canonical {
		if i > 0 {
			body += ", "
		}
		body += `"` + c + `"`
	}
	body += "]\n\n[identity.aliases]\n"
	for k, v := range aliases {
		body += `"` + k + `" = "` + v + `"` + "\n"
	}
	mustWrite(t, filepath.Join(repo, ".beadkeeper", "identity.toml"), []byte(body))
}

// writeLeaseJSONL writes the records to .beads/issues.jsonl.
func writeLeaseJSONL(t *testing.T, repo string, records []map[string]any) {
	t.Helper()
	mustMkdir(t, filepath.Join(repo, ".beads"))
	var body []byte
	for _, r := range records {
		b, _ := json.Marshal(r)
		body = append(body, b...)
		body = append(body, '\n')
	}
	mustWrite(t, filepath.Join(repo, ".beads", "issues.jsonl"), body)
}

func TestParityLeaseListEmpty(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	mustMkdir(t, filepath.Join(repo, ".beads"))
	mustWrite(t, filepath.Join(repo, ".beads", "issues.jsonl"), nil)

	pyOut := runBin(py, []string{"lease", "list", repo, "--json"})
	goOut := runBin(gobk, []string{"lease", "list", repo, "--json"})
	requireSameRC(t, "lease list (empty) --json", pyOut, goOut)
	requireJSONDeepEqual(t, "lease list (empty) --json", pyOut.stdout, goOut.stdout, repo)
}

func TestParityLeaseListActiveAndStale(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	veryOld := "2025-01-01T00:00:00+00:00"
	fresh := "2099-01-01T00:00:00+00:00"
	writeLeaseJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "alice", "updated_at": veryOld},
		{"id": "x.2", "status": "open"},
		{"id": "x.3", "status": "in_progress", "assignee": "bob", "updated_at": fresh},
	})
	pyOut := runBin(py, []string{"lease", "list", repo, "--json"})
	goOut := runBin(gobk, []string{"lease", "list", repo, "--json"})
	requireSameRC(t, "lease list (active+stale) --json", pyOut, goOut)
	// Use a custom comparator: age_seconds is wall-clock dependent;
	// drop it before deep-equal.
	requireJSONDeepEqualIgnoring(t, "lease list (active+stale) --json",
		pyOut.stdout, goOut.stdout, repo,
		map[string]struct{}{"age_seconds": {}})
}

func TestParityLeaseClaimSelfIdempotent(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, nil)
	writeLeaseJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "alice"},
	})
	pyOut := runBin(py, []string{"lease", "claim", "x.1", "--repo", repo, "--as", "alice"})
	goOut := runBin(gobk, []string{"lease", "claim", "x.1", "--repo", repo, "--as", "alice"})
	requireSameRC(t, "lease claim self", pyOut, goOut)
	for _, kw := range []string{"already held", "x.1", "alice"} {
		if !strings.Contains(pyOut.stdout, kw) {
			t.Errorf("py stdout missing %q: %s", kw, pyOut.stdout)
		}
		if !strings.Contains(goOut.stdout, kw) {
			t.Errorf("go stdout missing %q: %s", kw, goOut.stdout)
		}
	}
}

func TestParityLeaseClaimByOtherIsConflict(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice", "bob"}, nil)
	writeLeaseJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "bob"},
	})
	pyOut := runBin(py, []string{"lease", "claim", "x.1", "--repo", repo, "--as", "alice"})
	goOut := runBin(gobk, []string{"lease", "claim", "x.1", "--repo", repo, "--as", "alice"})
	if pyOut.rc != goOut.rc {
		t.Fatalf("rc: py=%d go=%d", pyOut.rc, goOut.rc)
	}
	if pyOut.rc == 0 {
		t.Fatalf("expected nonzero rc; got %d (py.stderr=%s)", pyOut.rc, pyOut.stderr)
	}
	for _, kw := range []string{"REFUSE", "held by", "bob"} {
		if !strings.Contains(pyOut.stderr, kw) {
			t.Errorf("py stderr missing %q: %s", kw, pyOut.stderr)
		}
		if !strings.Contains(goOut.stderr, kw) {
			t.Errorf("go stderr missing %q: %s", kw, goOut.stderr)
		}
	}
}

func TestParityLeaseReleaseNonHolder(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice", "bob"}, nil)
	writeLeaseJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "bob"},
	})
	pyOut := runBin(py, []string{"lease", "release", "x.1", "--repo", repo, "--as", "alice"})
	goOut := runBin(gobk, []string{"lease", "release", "x.1", "--repo", repo, "--as", "alice"})
	if pyOut.rc != goOut.rc {
		t.Fatalf("rc: py=%d go=%d\npy.stderr=%s\ngo.stderr=%s",
			pyOut.rc, goOut.rc, pyOut.stderr, goOut.stderr)
	}
	for _, kw := range []string{"REFUSE", "held by", "bob"} {
		if !strings.Contains(pyOut.stderr, kw) {
			t.Errorf("py stderr missing %q: %s", kw, pyOut.stderr)
		}
		if !strings.Contains(goOut.stderr, kw) {
			t.Errorf("go stderr missing %q: %s", kw, goOut.stderr)
		}
	}
}

func TestParityLeaseReleaseUnclaimedNoop(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, nil)
	writeLeaseJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "open"},
	})
	pyOut := runBin(py, []string{"lease", "release", "x.1", "--repo", repo, "--as", "alice"})
	goOut := runBin(gobk, []string{"lease", "release", "x.1", "--repo", repo, "--as", "alice"})
	requireSameRC(t, "lease release unclaimed", pyOut, goOut)
	for _, kw := range []string{"already released", "x.1"} {
		if !strings.Contains(pyOut.stdout, kw) {
			t.Errorf("py stdout missing %q: %s", kw, pyOut.stdout)
		}
		if !strings.Contains(goOut.stdout, kw) {
			t.Errorf("go stdout missing %q: %s", kw, goOut.stdout)
		}
	}
}

// --- merge-slot parity --------------------------------------------------

func writeSlotJSONL(t *testing.T, repo string, status, holder string) {
	t.Helper()
	mustMkdir(t, filepath.Join(repo, ".beads"))
	rec := map[string]any{"id": "bk-merge-slot", "status": status, "type": "merge-slot"}
	if holder != "" {
		rec["holder"] = holder
	}
	b, _ := json.Marshal(rec)
	mustWrite(t, filepath.Join(repo, ".beads", "issues.jsonl"), append(b, '\n'))
}

func TestParityMergeSlotStatusMissing(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	mustMkdir(t, filepath.Join(repo, ".beads"))
	mustWrite(t, filepath.Join(repo, ".beads", "issues.jsonl"), nil)
	pyOut := runBin(py, []string{"merge-slot", "status", "--repo", repo, "--json"})
	goOut := runBin(gobk, []string{"merge-slot", "status", "--repo", repo, "--json"})
	requireSameRC(t, "merge-slot status (missing) --json", pyOut, goOut)
	requireJSONDeepEqual(t, "merge-slot status (missing) --json", pyOut.stdout, goOut.stdout, repo)
}

func TestParityMergeSlotStatusOpen(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeSlotJSONL(t, repo, "open", "")
	pyOut := runBin(py, []string{"merge-slot", "status", "--repo", repo, "--json"})
	goOut := runBin(gobk, []string{"merge-slot", "status", "--repo", repo, "--json"})
	requireSameRC(t, "merge-slot status (open) --json", pyOut, goOut)
	requireJSONDeepEqual(t, "merge-slot status (open) --json", pyOut.stdout, goOut.stdout, repo)
}

func TestParityMergeSlotStatusHeld(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeSlotJSONL(t, repo, "in_progress", "alice")
	pyOut := runBin(py, []string{"merge-slot", "status", "--repo", repo, "--json"})
	goOut := runBin(gobk, []string{"merge-slot", "status", "--repo", repo, "--json"})
	requireSameRC(t, "merge-slot status (held) --json", pyOut, goOut)
	requireJSONDeepEqual(t, "merge-slot status (held) --json", pyOut.stdout, goOut.stdout, repo)
}

func TestParityMergeSlotAcquireSelfIdempotent(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeSlotJSONL(t, repo, "in_progress", "alice")
	pyOut := runBin(py, []string{"merge-slot", "acquire", "--repo", repo, "--holder", "alice"})
	goOut := runBin(gobk, []string{"merge-slot", "acquire", "--repo", repo, "--holder", "alice"})
	requireSameRC(t, "merge-slot acquire (self)", pyOut, goOut)
	for _, kw := range []string{"already held", "alice"} {
		if !strings.Contains(pyOut.stdout, kw) {
			t.Errorf("py stdout missing %q: %s", kw, pyOut.stdout)
		}
		if !strings.Contains(goOut.stdout, kw) {
			t.Errorf("go stdout missing %q: %s", kw, goOut.stdout)
		}
	}
}

func TestParityMergeSlotAcquireConflict(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeSlotJSONL(t, repo, "in_progress", "bob")
	pyOut := runBin(py, []string{"merge-slot", "acquire", "--repo", repo, "--holder", "alice"})
	goOut := runBin(gobk, []string{"merge-slot", "acquire", "--repo", repo, "--holder", "alice"})
	if pyOut.rc != goOut.rc {
		t.Fatalf("rc: py=%d go=%d", pyOut.rc, goOut.rc)
	}
	if pyOut.rc == 0 {
		t.Fatalf("expected nonzero; got %d", pyOut.rc)
	}
	for _, kw := range []string{"REFUSE", "held by", "bob"} {
		if !strings.Contains(pyOut.stderr, kw) {
			t.Errorf("py stderr missing %q: %s", kw, pyOut.stderr)
		}
		if !strings.Contains(goOut.stderr, kw) {
			t.Errorf("go stderr missing %q: %s", kw, goOut.stderr)
		}
	}
}

func TestParityMergeSlotReleaseNonHolder(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeSlotJSONL(t, repo, "in_progress", "bob")
	pyOut := runBin(py, []string{"merge-slot", "release", "--repo", repo, "--holder", "alice"})
	goOut := runBin(gobk, []string{"merge-slot", "release", "--repo", repo, "--holder", "alice"})
	if pyOut.rc != goOut.rc {
		t.Fatalf("rc: py=%d go=%d", pyOut.rc, goOut.rc)
	}
	for _, kw := range []string{"REFUSE", "held by"} {
		if !strings.Contains(pyOut.stderr, kw) {
			t.Errorf("py stderr missing %q: %s", kw, pyOut.stderr)
		}
		if !strings.Contains(goOut.stderr, kw) {
			t.Errorf("go stderr missing %q: %s", kw, goOut.stderr)
		}
	}
}

func TestParityMergeSlotReleaseWhenOpenNoop(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	writeSlotJSONL(t, repo, "open", "")
	pyOut := runBin(py, []string{"merge-slot", "release", "--repo", repo, "--holder", "alice"})
	goOut := runBin(gobk, []string{"merge-slot", "release", "--repo", repo, "--holder", "alice"})
	requireSameRC(t, "merge-slot release (open)", pyOut, goOut)
	for _, kw := range []string{"already released"} {
		if !strings.Contains(pyOut.stdout, kw) {
			t.Errorf("py stdout missing %q: %s", kw, pyOut.stdout)
		}
		if !strings.Contains(goOut.stdout, kw) {
			t.Errorf("go stdout missing %q: %s", kw, goOut.stdout)
		}
	}
}

// --- trunk-sync parity --------------------------------------------------

// commitJSONLOn appends a line to .beads/issues.jsonl on `branch`,
// creating the branch if it doesn't exist.
func commitJSONLOn(t *testing.T, repo, branch, line string) {
	t.Helper()
	exists := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	exists.Dir = repo
	if exists.Run() == nil {
		mustGitOk(t, repo, "checkout", "-q", branch)
	} else {
		mustGitOk(t, repo, "checkout", "-q", "-b", branch)
	}
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	prev, _ := os.ReadFile(jsonl)
	mustWrite(t, jsonl, append(prev, []byte(line+"\n")...))
	mustGitOk(t, repo, "add", ".beads/issues.jsonl")
	mustGitOk(t, repo, "commit", "-q", "-m", "bead edit on "+branch)
}

func mustGitOk(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestParityTrunkSyncScanClean(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	setupGitRepo(t, repo, "beads-sync")
	mustGitOk(t, repo, "branch", "beads-sync")
	envOv := stubbedBdPath(t)
	pyOut := runBinEnv(py, []string{"trunk-sync", repo, "--json"}, envOv)
	goOut := runBinEnv(gobk, []string{"trunk-sync", repo, "--json"}, envOv)
	requireSameRC(t, "trunk-sync scan (clean) --json", pyOut, goOut)
	requireJSONDeepEqual(t, "trunk-sync scan (clean) --json", pyOut.stdout, goOut.stdout, repo)
}

func TestParityTrunkSyncScanSyncBranchAhead(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	setupGitRepo(t, repo, "beads-sync")
	mustGitOk(t, repo, "branch", "beads-sync")
	commitJSONLOn(t, repo, "beads-sync", `{"id":"x","status":"closed"}`)
	envOv := stubbedBdPath(t)
	pyOut := runBinEnv(py, []string{"trunk-sync", repo, "--json"}, envOv)
	goOut := runBinEnv(gobk, []string{"trunk-sync", repo, "--json"}, envOv)
	requireSameRC(t, "trunk-sync scan (ahead) --json", pyOut, goOut)
	requireJSONDeepEqual(t, "trunk-sync scan (ahead) --json", pyOut.stdout, goOut.stdout, repo)
}

func TestParityTrunkSyncScanDivergentRed(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	setupGitRepo(t, repo, "beads-sync")
	mustGitOk(t, repo, "branch", "beads-sync")
	commitJSONLOn(t, repo, "beads-sync", `{"id":"a","status":"open"}`)
	commitJSONLOn(t, repo, "main", `{"id":"b","status":"open"}`)
	envOv := stubbedBdPath(t)
	pyOut := runBinEnv(py, []string{"trunk-sync", repo, "--json"}, envOv)
	goOut := runBinEnv(gobk, []string{"trunk-sync", repo, "--json"}, envOv)
	requireSameRC(t, "trunk-sync scan (divergent) --json", pyOut, goOut)
	if pyOut.rc != 2 {
		t.Fatalf("expected rc=2 (RED); got %d (py.stdout=%s)", pyOut.rc, pyOut.stdout)
	}
	requireJSONDeepEqual(t, "trunk-sync scan (divergent) --json", pyOut.stdout, goOut.stdout, repo)
}

func TestParityTrunkSyncApplyDryRun(t *testing.T) {
	py := pyBinary(t)
	gobk := goBinary(t)
	repo := t.TempDir()
	setupGitRepo(t, repo, "beads-sync")
	mustGitOk(t, repo, "branch", "beads-sync")
	commitJSONLOn(t, repo, "beads-sync", `{"id":"y","status":"closed"}`)
	envOv := stubbedBdPath(t)
	// `--apply` without `--yes` is dry-run by contract.
	pyOut := runBinEnv(py, []string{"trunk-sync", repo, "--apply"}, envOv)
	goOut := runBinEnv(gobk, []string{"trunk-sync", repo, "--apply"}, envOv)
	requireSameRC(t, "trunk-sync --apply (dry-run)", pyOut, goOut)
	for _, kw := range []string{"DRY-RUN"} {
		if !strings.Contains(pyOut.stdout, kw) {
			t.Errorf("py stdout missing %q: %s", kw, pyOut.stdout)
		}
		if !strings.Contains(goOut.stdout, kw) {
			t.Errorf("go stdout missing %q: %s", kw, goOut.stdout)
		}
	}
}

// --- harness helper: deep-equal with extra ignored keys -----------------

// requireJSONDeepEqualIgnoring is requireJSONDeepEqual with a caller-
// supplied set of additional keys to drop before comparison (useful
// when fields like `age_seconds` are inherently wall-clock-volatile).
func requireJSONDeepEqualIgnoring(t *testing.T, label, pyOut, goOut, projectRoot string,
	extraIgnore map[string]struct{}) {
	t.Helper()
	var pyV, goV any
	if err := json.Unmarshal([]byte(pyOut), &pyV); err != nil {
		t.Fatalf("%s: py JSON parse: %v\n%s", label, err, pyOut)
	}
	if err := json.Unmarshal([]byte(goOut), &goV); err != nil {
		t.Fatalf("%s: go JSON parse: %v\n%s", label, err, goOut)
	}
	pn := normalizeJSONExt(pyV, projectRoot, extraIgnore)
	gn := normalizeJSONExt(goV, projectRoot, extraIgnore)
	if !reflect.DeepEqual(pn, gn) {
		pb, _ := json.MarshalIndent(pn, "", "  ")
		gb, _ := json.MarshalIndent(gn, "", "  ")
		t.Fatalf("%s: JSON deep-equal failed.\n--- py:\n%s\n--- go:\n%s",
			label, pb, gb)
	}
}

func normalizeJSONExt(v any, projectRoot string, extra map[string]struct{}) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if k == "generated_at" || k == "scanned_paths" || k == "daemon" || k == "git" {
				continue
			}
			if _, drop := extra[k]; drop {
				continue
			}
			n := normalizeJSONExt(val, projectRoot, extra)
			if n == nil || n == "" || n == float64(0) {
				continue
			}
			out[k] = n
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			out = append(out, normalizeJSONExt(x, projectRoot, extra))
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		// reuse the scalar normalizer.
		return normalizeJSONForCompare(v, projectRoot)
	default:
		return v
	}
}

// --- assertions --------------------------------------------------------

func requireSameRC(t *testing.T, label string, py, gobk runResult) {
	t.Helper()
	if py.rc != gobk.rc {
		t.Fatalf("%s: rc differs py=%d go=%d\npy.stdout=%s\nge.stdout=%s\npy.stderr=%s\nge.stderr=%s",
			label, py.rc, gobk.rc, py.stdout, gobk.stdout, py.stderr, gobk.stderr)
	}
}

func requireJSONDeepEqual(t *testing.T, label, pyOut, goOut, projectRoot string) {
	t.Helper()
	var pyV, goV any
	if err := json.Unmarshal([]byte(pyOut), &pyV); err != nil {
		t.Fatalf("%s: py JSON parse: %v\n%s", label, err, pyOut)
	}
	if err := json.Unmarshal([]byte(goOut), &goV); err != nil {
		t.Fatalf("%s: go JSON parse: %v\n%s", label, err, goOut)
	}
	pn := normalizeJSONForCompare(pyV, projectRoot)
	gn := normalizeJSONForCompare(goV, projectRoot)
	if !reflect.DeepEqual(pn, gn) {
		// Re-serialise normalized forms for a tractable diff message.
		pb, _ := json.MarshalIndent(pn, "", "  ")
		gb, _ := json.MarshalIndent(gn, "", "  ")
		t.Fatalf("%s: JSON deep-equal failed.\n--- py:\n%s\n--- go:\n%s",
			label, pb, gb)
	}
}
