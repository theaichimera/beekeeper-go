package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeIdentityTOML(t *testing.T, repo string, canon []string, aliases map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".beadkeeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[identity]\ncanonical = ["
	for i, c := range canon {
		if i > 0 {
			body += ", "
		}
		body += `"` + c + `"`
	}
	body += "]\n\n[identity.aliases]\n"
	for k, v := range aliases {
		body += `"` + k + `" = "` + v + `"` + "\n"
	}
	if err := os.WriteFile(filepath.Join(repo, ".beadkeeper", "identity.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSONL(t *testing.T, repo string, records []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, r := range records {
		b, _ := json.Marshal(r)
		body = append(body, b...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanCanonicalOnlyIsClean(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, map[string]string{})
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "assignee": "alice", "created_by": "alice"},
		{"id": "x.2", "owner": "alice"},
	})
	got := ScanProject(repo)
	if got.HasDrift() {
		t.Fatalf("drift unexpected: %+v", got)
	}
}

func TestScanFlagsAliasAndUnmapped(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, map[string]string{
		"alice@example.com": "alice",
	})
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "assignee": "alice@example.com"},
		{"id": "x.2", "assignee": "alice"},
		{"id": "x.3", "owner": "carol"},
	})
	got := ScanProject(repo)
	if got.AliasedHandles["alice@example.com"] != "alice" {
		t.Fatalf("alias mapping wrong: %+v", got)
	}
	if _, ok := got.UnmappedHandles["carol"]; !ok {
		t.Fatalf("expected carol unmapped: %+v", got)
	}
	if got.AliasedOccurrences != 1 || got.UnmappedOccurrences != 1 {
		t.Fatalf("occ aliased=%d unmapped=%d", got.AliasedOccurrences, got.UnmappedOccurrences)
	}
}

func TestScanNoConfigEmitsEverythingAsUnmapped(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "assignee": "alice"},
	})
	got := ScanProject(repo)
	if !got.HasDrift() {
		t.Fatalf("expected drift when no config: %+v", got)
	}
	if _, ok := got.UnmappedHandles["alice"]; !ok {
		t.Fatalf("alice not unmapped: %+v", got)
	}
}

func TestPlanNormalizeIsDryRun(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, map[string]string{"alice@x": "alice"})
	jsonlPath := filepath.Join(repo, ".beads", "issues.jsonl")
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "assignee": "alice@x"},
		{"id": "x.2", "owner": "alice"},
	})
	before, _ := os.ReadFile(jsonlPath)
	r := PlanNormalize(repo)
	if !r.DryRun {
		t.Fatal("expected DryRun=true")
	}
	if r.WouldRewriteCount != 1 {
		t.Fatalf("WouldRewriteCount=%d want 1", r.WouldRewriteCount)
	}
	if r.MappedHandles["alice@x"] != "alice" {
		t.Fatalf("mappedHandles=%v", r.MappedHandles)
	}
	after, _ := os.ReadFile(jsonlPath)
	if string(before) != string(after) {
		t.Fatal("PlanNormalize must NOT mutate the JSONL")
	}
}

func TestScanIgnoresMalformedAndNonStringHandles(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, map[string]string{})
	// One malformed JSONL line + one record with non-string assignee.
	body := []byte(
		`{"id":"a","assignee":"alice"}` + "\n" +
			"not-json-at-all\n" +
			`{"id":"b","assignee":42}` + "\n",
	)
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	got := ScanProject(repo)
	if got.HasDrift() {
		t.Fatalf("drift unexpected on malformed-tolerant scan: %+v", got)
	}
}
