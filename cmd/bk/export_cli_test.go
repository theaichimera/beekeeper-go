package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// stubBdForExport stubs `bd` so cmd-level tests don't trip the same
// real-bd-init issue the export package's own tests dodge.
func stubBdForExport(t *testing.T) {
	t.Helper()
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		return 1, "", "(cmd-test stub)", nil
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
}

func writeBeadJSONLForExport(t *testing.T, dir string, recs []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, r := range recs {
		b, _ := json.Marshal(r)
		body = append(body, b...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCLIExportNDJSONDefault(t *testing.T) {
	stubBdForExport(t)
	dir := t.TempDir()
	writeBeadJSONLForExport(t, dir, []map[string]any{
		{"id": "p-1", "title": "first", "description": "x", "status": "open",
			"issue_type": "feature", "created_at": "2026-06-01T00:00:00Z",
			"updated_at": "2026-06-02T00:00:00Z"},
		{"id": "p-2", "title": "second", "description": "y", "status": "closed",
			"issue_type": "task", "created_at": "2026-06-01T00:00:00Z",
			"updated_at": "2026-06-02T00:00:00Z"},
	})

	out, _, rc := runCmd(t, "export", dir, "--no-comments")
	if rc != 0 {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ndjson lines=%d want 2:\n%s", len(lines), out)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "{") {
			t.Fatalf("not a JSON object line: %q", l)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(l), &doc); err != nil {
			t.Fatalf("invalid JSON line: %v\n%q", err, l)
		}
		// Schema spot-check: required keys.
		for _, k := range []string{"id", "repo", "title", "body", "comments", "text",
			"status", "issue_type", "labels", "deps", "schema_version"} {
			if _, ok := doc[k]; !ok {
				t.Fatalf("missing key %q in: %v", k, doc)
			}
		}
	}
}

func TestCLIExportJSONFormat(t *testing.T) {
	stubBdForExport(t)
	dir := t.TempDir()
	writeBeadJSONLForExport(t, dir, []map[string]any{
		{"id": "p-1", "title": "x", "description": "y", "status": "open",
			"issue_type": "feature", "created_at": "2026-06-01T00:00:00Z",
			"updated_at": "2026-06-02T00:00:00Z"},
	})

	out, _, rc := runCmd(t, "export", dir, "--format", "json", "--no-comments")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("not a JSON array: %v\n%q", err, out)
	}
	if len(arr) != 1 || arr[0]["id"] != "p-1" {
		t.Fatalf("unexpected shape: %v", arr)
	}
}

func TestCLIExportFieldsProjection(t *testing.T) {
	stubBdForExport(t)
	dir := t.TempDir()
	writeBeadJSONLForExport(t, dir, []map[string]any{
		{"id": "p-1", "title": "x", "description": "y", "status": "open",
			"issue_type": "feature", "created_at": "2026-06-01T00:00:00Z",
			"updated_at": "2026-06-02T00:00:00Z"},
	})

	out, _, _ := runCmd(t, "export", dir, "--format", "json", "--no-comments",
		"--fields", "id,title,status")
	var arr []map[string]any
	_ = json.Unmarshal([]byte(out), &arr)
	if len(arr) != 1 {
		t.Fatalf("len=%d", len(arr))
	}
	doc := arr[0]
	for _, want := range []string{"id", "title", "status"} {
		if _, ok := doc[want]; !ok {
			t.Fatalf("missing projected field %q: %v", want, doc)
		}
	}
	if _, ok := doc["body"]; ok {
		t.Fatalf("non-projected field 'body' leaked: %v", doc)
	}
	if _, ok := doc["text"]; ok {
		t.Fatalf("non-projected field 'text' leaked: %v", doc)
	}
}

func TestCLIExportInvalidFormatExits64(t *testing.T) {
	stubBdForExport(t)
	dir := t.TempDir()
	writeBeadJSONLForExport(t, dir, []map[string]any{
		{"id": "p-1", "title": "x", "description": "y", "status": "open",
			"issue_type": "feature"},
	})
	_, _, rc := runCmd(t, "export", dir, "--format", "wat")
	if rc != 64 {
		t.Fatalf("rc=%d want 64", rc)
	}
}

func TestCLIExportSinceShorthand(t *testing.T) {
	stubBdForExport(t)
	dir := t.TempDir()
	now := time.Now()
	old := now.AddDate(0, 0, -10).Format(time.RFC3339)
	recent := now.AddDate(0, 0, -1).Format(time.RFC3339)
	writeBeadJSONLForExport(t, dir, []map[string]any{
		{"id": "p-old", "title": "old", "description": "x", "status": "open",
			"issue_type": "feature", "created_at": old, "updated_at": old},
		{"id": "p-recent", "title": "new", "description": "x", "status": "open",
			"issue_type": "feature", "created_at": recent, "updated_at": recent},
	})

	out, _, _ := runCmd(t, "export", dir, "--no-comments", "--since", "5d", "--format", "json")
	var arr []map[string]any
	_ = json.Unmarshal([]byte(out), &arr)
	if len(arr) != 1 || arr[0]["id"] != "p-recent" {
		t.Fatalf("--since 5d: %v", arr)
	}
}

func TestCLIExportOutputFile(t *testing.T) {
	stubBdForExport(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "corpus.ndjson")
	writeBeadJSONLForExport(t, dir, []map[string]any{
		{"id": "p-1", "title": "x", "description": "y", "status": "open",
			"issue_type": "feature", "created_at": "2026-06-01T00:00:00Z",
			"updated_at": "2026-06-02T00:00:00Z"},
	})

	_, _, rc := runCmd(t, "export", dir, "--no-comments", "-o", outPath)
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"p-1"`) {
		t.Fatalf("output file missing record: %s", body)
	}
}

func TestCLIExportDecisionsOnly(t *testing.T) {
	stubBdForExport(t)
	dir := t.TempDir()
	rec := func(id, title, desc string, labels []string) map[string]any {
		out := map[string]any{
			"id": id, "title": title, "description": desc,
			"status": "open", "issue_type": "feature",
			"created_at": "2026-06-01T00:00:00Z",
			"updated_at": "2026-06-02T00:00:00Z",
		}
		if len(labels) > 0 {
			raw := make([]any, 0, len(labels))
			for _, l := range labels {
				raw = append(raw, l)
			}
			out["labels"] = raw
		}
		return out
	}
	writeBeadJSONLForExport(t, dir, []map[string]any{
		rec("p-d", "decision", "we decided to ship", []string{"adr"}),
		rec("p-r", "routine", "minor", []string{"chore"}),
	})

	out, _, _ := runCmd(t, "export", dir, "--no-comments", "--format", "json", "--decisions-only")
	var arr []map[string]any
	_ = json.Unmarshal([]byte(out), &arr)
	if len(arr) != 1 || arr[0]["id"] != "p-d" {
		t.Fatalf("decisions-only: %v", arr)
	}
}
