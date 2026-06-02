// Package export emits a vectorizable bead corpus — one document per
// bead, drawn from the AUTHORITATIVE bead state (sync branch JSONL +
// bd-known comments + identity normalization), shaped for any
// downstream vector store. bk owns no vector index; this package
// only produces the corpus.
//
// Authoritative read strategy (bkg-3xb):
//
//   - Prefer `git show <sync.branch>:.beads/issues.jsonl` when a
//     sync branch is configured AND exists locally — this is the
//     reconciled truth, not the possibly-stale trunk copy.
//   - Fall back to the working tree's `.beads/issues.jsonl` (which
//     reflects local commits + uncommitted edits). Operators with
//     dirty trees see their working-state, not stale trunk —
//     consistent with the warn-not-refuse open-question stance.
//   - Comments come from `bd comments <id> --json` (JSONL doesn't
//     carry them). bd unavailable / errors -> comments=[] per bead;
//     export still completes.
//
// Curation is METADATA, never deletion. is_routine / is_decision /
// richness are emitted on every doc; filters (--min-richness,
// --decisions-only) read those flags but don't redefine them.
package export

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/theaichimera/beekeeper-go/internal/config"
	"github.com/theaichimera/beekeeper-go/internal/git"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// JSONLRelPath duplicates the bead JSONL constant — package is
// import-isolated from board / staleship.
const JSONLRelPath = ".beads/issues.jsonl"

// SchemaVersion bumps when the document shape changes. Downstream
// consumers can pin against this. New fields can be added without a
// bump; field removal / rename requires one.
const SchemaVersion = 1

// Comment is one comment on a bead.
type Comment struct {
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// Deps is the resolved dependency graph for one bead.
type Deps struct {
	Blocks    []string `json:"blocks"`
	BlockedBy []string `json:"blocked_by"`
}

// Doc is the per-bead export document. Field order matches the
// declared schema in docs/README. New fields go at the end.
type Doc struct {
	ID            string    `json:"id"`
	Repo          string    `json:"repo"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	Comments      []Comment `json:"comments"`
	Text          string    `json:"text"`
	Status        string    `json:"status"`
	IssueType     string    `json:"issue_type"`
	Priority      *int      `json:"priority"`
	Labels        []string  `json:"labels"`
	Assignee      string    `json:"assignee"`
	Deps          Deps      `json:"deps"`
	CreatedAt     string    `json:"created_at"`
	UpdatedAt     string    `json:"updated_at"`
	ClosedAt      string    `json:"closed_at,omitempty"`
	IsRoutine     bool      `json:"is_routine"`
	IsDecision    bool      `json:"is_decision"`
	Richness      int       `json:"richness"`
	SupersededBy  []string  `json:"superseded_by,omitempty"`
	SchemaVersion int       `json:"schema_version"`
}

// Format selects the output framing.
type Format string

const (
	FormatNDJSON Format = "ndjson"
	FormatJSON   Format = "json"
)

// StatusFilter selects which beads to include by status.
type StatusFilter string

const (
	StatusOpen   StatusFilter = "open"
	StatusClosed StatusFilter = "closed"
	StatusAll    StatusFilter = "all"
)

// Opts controls the export pass.
type Opts struct {
	Paths           []string // input roots; default {"."}
	MaxDepth        int      // project walker depth; 0 -> default
	Status          StatusFilter
	Types           []string  // include filter (empty = all)
	IncludeLabels   []string  // include filter
	ExcludeLabels   []string  // exclude filter
	Since           time.Time // include only beads updated_at >= Since (zero = no filter)
	IncludeComments bool      // pull `bd comments` per bead
	Classify        bool      // compute is_routine/is_decision/richness
	MinRichness     int       // drop docs whose richness < N (0 = off)
	DecisionsOnly   bool      // keep only is_decision==true
	TextTemplate    string    // assembly template; empty -> default
	Fields          []string  // project-down list; empty -> all fields
	RepoFilter      string    // glob over project root
	Redact          bool      // scrub emails / token-looking patterns from text fields

	// IdentityMap is an optional alias->canonical map used to
	// normalize author / assignee. Pass nil to skip normalization.
	IdentityMap map[string]string

	// CommentsRunner is an injectable shell-out for `bd comments
	// <id> --json`. nil -> use the package default (bdCommentsDefault).
	CommentsRunner func(repo, id string) ([]Comment, error)
}

// DefaultTextTemplate concatenates title, body, and comment bodies
// in the order documented in the README. Comments win the most
// rationale signal so they go last.
const DefaultTextTemplate = "{{title}}\n\n{{body}}\n\n{{comments}}"

// Build runs the export and returns a deterministic slice of Docs.
// Ordering: by repo (project root absolute path), then by bead id.
// Caller controls framing (NDJSON / JSON / fields projection).
func Build(opts Opts) ([]Doc, error) {
	paths := opts.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = bkproject.DefaultMaxDepth
	}
	status := opts.Status
	if status == "" {
		status = StatusAll
	}
	template := opts.TextTemplate
	if template == "" {
		template = DefaultTextTemplate
	}
	commentsRunner := opts.CommentsRunner
	if commentsRunner == nil {
		commentsRunner = bdCommentsDefault
	}

	projects := bkproject.FindProjects(paths, maxDepth)
	if opts.RepoFilter != "" {
		projects = filterProjectsByGlob(projects, opts.RepoFilter)
	}

	includeTypes := makeSet(opts.Types)
	includeLabels := makeSet(opts.IncludeLabels)
	excludeLabels := makeSet(opts.ExcludeLabels)

	var docs []Doc
	for _, p := range projects {
		records, err := readAuthoritativeJSONL(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p.Root, err)
		}
		// Build id->record index for resolveDeps.
		byID := make(map[string]map[string]any, len(records))
		for _, rec := range records {
			if id, _ := rec["id"].(string); id != "" {
				byID[id] = rec
			}
		}
		for _, rec := range records {
			doc, ok := buildDoc(p.Root, rec, byID, opts, template, commentsRunner)
			if !ok {
				continue
			}
			// Status filter.
			switch status {
			case StatusOpen:
				if doc.Status == "closed" {
					continue
				}
			case StatusClosed:
				if doc.Status != "closed" {
					continue
				}
			}
			// Type filter.
			if len(includeTypes) > 0 && !includeTypes[doc.IssueType] {
				continue
			}
			// Labels include / exclude.
			if len(includeLabels) > 0 {
				hit := false
				for _, l := range doc.Labels {
					if includeLabels[l] {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
			}
			if len(excludeLabels) > 0 {
				skip := false
				for _, l := range doc.Labels {
					if excludeLabels[l] {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
			}
			// Since filter.
			if !opts.Since.IsZero() {
				t, ok := parseTime(doc.UpdatedAt)
				if ok && t.Before(opts.Since) {
					continue
				}
			}
			// Curation filters (must run after Classify).
			if opts.MinRichness > 0 && doc.Richness < opts.MinRichness {
				continue
			}
			if opts.DecisionsOnly && !doc.IsDecision {
				continue
			}
			docs = append(docs, doc)
		}
	}

	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].Repo != docs[j].Repo {
			return docs[i].Repo < docs[j].Repo
		}
		return docs[i].ID < docs[j].ID
	})
	return docs, nil
}

// buildDoc assembles one Doc from a JSONL record. Returns (zero,
// false) for malformed records (no id).
func buildDoc(repo string, rec map[string]any, byID map[string]map[string]any, opts Opts, template string, commentsRunner func(repo, id string) ([]Comment, error)) (Doc, bool) {
	id, _ := rec["id"].(string)
	if id == "" {
		return Doc{}, false
	}
	title, _ := rec["title"].(string)
	body, _ := rec["description"].(string)
	status, _ := rec["status"].(string)
	issueType, _ := rec["issue_type"].(string)
	assignee := normalizeIdentity(stringOr(rec["assignee"]), opts.IdentityMap)
	createdAt, _ := rec["created_at"].(string)
	updatedAt, _ := rec["updated_at"].(string)
	closedAt, _ := rec["closed_at"].(string)
	closeReason, _ := rec["close_reason"].(string)

	priority := intField(rec, "priority")
	labels := stringSliceField(rec, "labels")
	deps := resolveDeps(id, rec, byID)

	var comments []Comment
	if opts.IncludeComments && commentsRunner != nil {
		if cs, err := commentsRunner(repo, id); err == nil {
			for i := range cs {
				cs[i].Author = normalizeIdentity(cs[i].Author, opts.IdentityMap)
			}
			comments = cs
		}
	}

	doc := Doc{
		ID:            id,
		Repo:          repo,
		Title:         title,
		Body:          body,
		Comments:      comments,
		Status:        status,
		IssueType:     issueType,
		Priority:      priority,
		Labels:        labels,
		Assignee:      assignee,
		Deps:          deps,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		ClosedAt:      closedAt,
		SchemaVersion: SchemaVersion,
	}
	doc.Text = renderText(template, doc, opts.Redact)
	if opts.Redact {
		doc.Body = redactString(body)
		doc.Title = redactString(title)
		for i := range doc.Comments {
			doc.Comments[i].Body = redactString(doc.Comments[i].Body)
		}
	}
	if opts.Classify {
		doc.IsRoutine, doc.IsDecision, doc.Richness = classify(doc)
	}
	doc.SupersededBy = deriveSupersededBy(rec, closeReason)
	return doc, true
}

// readAuthoritativeJSONL prefers the sync-branch JSONL (the
// reconciled truth) and falls back to the working tree's JSONL when
// no sync branch exists or the read fails. Caller-visible behavior:
// authoritative state by default, working-state fallback for
// freshly-cloned / pre-sync repos.
func readAuthoritativeJSONL(p bkproject.Project) ([]map[string]any, error) {
	gitState := bkproject.ReadGitState(p)
	syncBranch := strings.TrimSpace(gitState.SyncBranch)
	if syncBranch != "" && git.BranchExists(p.Root, syncBranch) {
		if body, ok := git.Show(p.Root, syncBranch, JSONLRelPath); ok {
			return parseJSONL(strings.NewReader(body))
		}
	}
	// Fallback: working tree.
	f, err := os.Open(filepath.Join(p.Root, JSONLRelPath))
	if err != nil {
		// Empty / missing JSONL -> empty record set, NOT an error.
		// Lets cross-project export survive a freshly-init repo.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return parseJSONL(f)
}

// parseJSONL consumes one JSON object per line. Malformed lines are
// silently skipped (parity with the rest of bk).
func parseJSONL(r interface {
	Read(p []byte) (int, error)
}) ([]map[string]any, error) {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)
	var out []map[string]any
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// resolveDeps walks the bead's `dependencies` array and yields the
// resolved blocks/blocked-by sets keyed on the SAME project. Cross-
// project deps are dropped — bk's data model doesn't carry external
// references.
func resolveDeps(id string, rec map[string]any, byID map[string]map[string]any) Deps {
	deps := Deps{}
	raw, _ := rec["dependencies"].([]any)
	for _, item := range raw {
		dep, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := dep["type"].(string); t != "blocks" {
			continue
		}
		issue, _ := dep["issue_id"].(string)
		dependsOn, _ := dep["depends_on_id"].(string)
		if issue == "" || dependsOn == "" {
			continue
		}
		if issue == id {
			// "this issue blocks-on dependsOn" -> dependsOn blocks me.
			deps.BlockedBy = appendUnique(deps.BlockedBy, dependsOn)
		}
		if dependsOn == id {
			// dep targets me -> issue is blocking me too (mirrored).
			deps.BlockedBy = appendUnique(deps.BlockedBy, issue)
		}
	}
	// Forward edges: scan the index for entries whose dep targets us.
	for otherID, otherRec := range byID {
		if otherID == id {
			continue
		}
		oraw, _ := otherRec["dependencies"].([]any)
		for _, item := range oraw {
			dep, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := dep["type"].(string); t != "blocks" {
				continue
			}
			issue, _ := dep["issue_id"].(string)
			dependsOn, _ := dep["depends_on_id"].(string)
			// otherID has issue=otherID, depends_on=id => I block other.
			if issue == otherID && dependsOn == id {
				deps.Blocks = appendUnique(deps.Blocks, otherID)
			}
		}
	}
	sort.Strings(deps.Blocks)
	sort.Strings(deps.BlockedBy)
	return deps
}

// deriveSupersededBy extracts a bead-id list from the close reason
// when it mentions "superseded by <id>" / "shipped in <id>" patterns.
// Best-effort — `superseded_by` is a hint to the indexer, not ground
// truth.
var supersededByRE = regexp.MustCompile(`(?i)(?:superseded|replaced|obsoleted)\s+by\s+([a-z0-9._-]+)`)

func deriveSupersededBy(rec map[string]any, closeReason string) []string {
	src := closeReason
	if src == "" {
		src, _ = rec["close_reason"].(string)
	}
	if src == "" {
		return nil
	}
	matches := supersededByRE.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		id := strings.TrimSpace(m[1])
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// classify produces (is_routine, is_decision, richness) from a Doc.
// Heuristics:
//
//   - is_decision: any label in {decision, adr, architecture, design},
//     OR any of {because, decided, rejected, instead of} appears in
//     body+comments (case-insensitive).
//   - is_routine: short body (richness < routineRichnessThreshold),
//     no decision signal, AND title/labels match routine words
//     (chore, typo, bump, deps, docs).
//   - richness: len(body) + sum(len(comment.body)).
//
// Heuristics are a ranking aid, not ground truth — the indexer can
// override at query time using these flags.
const (
	routineRichnessThreshold = 200
)

var (
	decisionLabelSet = map[string]bool{
		"decision": true, "adr": true, "architecture": true, "design": true,
	}
	decisionPhrases = []string{
		"decided", "rejected", "instead of", "because we", "trade-off", "tradeoff",
	}
	routineLabelSet = map[string]bool{
		"chore": true, "typo": true, "bump": true, "deps": true, "docs": true,
	}
)

func classify(doc Doc) (routine bool, decision bool, richness int) {
	richness = len(doc.Body)
	for _, c := range doc.Comments {
		richness += len(c.Body)
	}

	for _, l := range doc.Labels {
		ll := strings.ToLower(l)
		if decisionLabelSet[ll] {
			decision = true
		}
	}
	if !decision {
		body := strings.ToLower(doc.Body)
		for _, c := range doc.Comments {
			body += "\n" + strings.ToLower(c.Body)
		}
		for _, p := range decisionPhrases {
			if strings.Contains(body, p) {
				decision = true
				break
			}
		}
	}

	if !decision && richness < routineRichnessThreshold {
		hit := false
		for _, l := range doc.Labels {
			if routineLabelSet[strings.ToLower(l)] {
				hit = true
				break
			}
		}
		if !hit {
			lt := strings.ToLower(doc.Title)
			for r := range routineLabelSet {
				if strings.Contains(lt, r) {
					hit = true
					break
				}
			}
		}
		if hit {
			routine = true
		}
	}
	return routine, decision, richness
}

// renderText assembles `text` from a tiny mustache-ish substitution.
// Supported tokens: {{title}}, {{body}}, {{comments}}. Anything else
// renders literally.
func renderText(template string, d Doc, redact bool) string {
	commentsBlob := ""
	if len(d.Comments) > 0 {
		var parts []string
		for _, c := range d.Comments {
			parts = append(parts, c.Body)
		}
		commentsBlob = strings.Join(parts, "\n\n")
	}
	out := template
	out = strings.ReplaceAll(out, "{{title}}", d.Title)
	out = strings.ReplaceAll(out, "{{body}}", d.Body)
	out = strings.ReplaceAll(out, "{{comments}}", commentsBlob)
	if redact {
		out = redactString(out)
	}
	return out
}

// redactString scrubs email addresses + GitHub-style PAT-looking
// blobs from `s`. Best-effort; not a security boundary. The flag is
// labeled "scrub" not "encrypt" — meant to keep low-stakes secrets
// out of an embedding payload.
var (
	emailRE = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	ghPatRE = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`)
)

func redactString(s string) string {
	s = emailRE.ReplaceAllString(s, "<EMAIL>")
	s = ghPatRE.ReplaceAllString(s, "<TOKEN>")
	return s
}

// normalizeIdentity returns the canonical handle for `raw` per the
// supplied alias map. nil map (or no match) -> raw passthrough.
func normalizeIdentity(raw string, m map[string]string) string {
	if raw == "" {
		return ""
	}
	if m == nil {
		return raw
	}
	if c, ok := m[raw]; ok && c != "" {
		return c
	}
	return raw
}

// LoadIdentityMap reads `.beadkeeper/identity.toml` for the project
// and returns a flat alias->canonical map suitable for opts.IdentityMap.
// Returns nil + nil when no config exists (opt-in identity).
func LoadIdentityMap(repo string) (map[string]string, error) {
	cfg, err := config.LoadIdentityConfig(repo)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	out := map[string]string{}
	for c := range cfg.Canonical {
		out[c] = c
	}
	for alias, canonical := range cfg.Aliases {
		out[alias] = canonical
	}
	return out, nil
}

// filterProjectsByGlob keeps only projects whose absolute root
// matches `pattern`. Uses filepath.Match semantics; pattern is
// matched against the full path AND the basename.
func filterProjectsByGlob(projects []bkproject.Project, pattern string) []bkproject.Project {
	if pattern == "" {
		return projects
	}
	out := make([]bkproject.Project, 0, len(projects))
	for _, p := range projects {
		base := filepath.Base(p.Root)
		if ok, _ := filepath.Match(pattern, base); ok {
			out = append(out, p)
			continue
		}
		if ok, _ := filepath.Match(pattern, p.Root); ok {
			out = append(out, p)
		}
	}
	return out
}

// ProjectFields applies a fields-allowlist projection to docs. An
// empty list returns docs unchanged.
//
// Rendering goes through json round-trip so the projected output
// carries only the requested keys. This keeps the schema honest at
// the JSON layer (consumers see exactly what they asked for).
func ProjectFields(docs []Doc, fields []string) ([]map[string]any, error) {
	if len(fields) == 0 {
		// Round-trip into map[string]any anyway so callers always
		// have one wire shape.
		out := make([]map[string]any, 0, len(docs))
		for _, d := range docs {
			b, err := json.Marshal(d)
			if err != nil {
				return nil, err
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return out, nil
	}
	allow := makeSet(fields)
	out := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		b, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		for k := range m {
			if !allow[k] {
				delete(m, k)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// --- helpers -----------------------------------------------------------

func makeSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" {
			out[x] = true
		}
	}
	return out
}

func stringOr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intField(rec map[string]any, key string) *int {
	switch v := rec[key].(type) {
	case float64:
		i := int(v)
		return &i
	case int:
		return &v
	}
	return nil
}

func stringSliceField(rec map[string]any, key string) []string {
	raw, _ := rec[key].([]any)
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func appendUnique(dst []string, s string) []string {
	for _, x := range dst {
		if x == s {
			return dst
		}
	}
	return append(dst, s)
}

// parseTime accepts the RFC3339 / RFC3339Nano timestamps bd emits.
func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// bdCommentsDefault shells `bd comments <id> --json` and parses the
// result. Returns ([], nil) when bd reports no comments; (nil, err)
// when bd is unavailable / errored — caller treats as empty.
func bdCommentsDefault(repo, id string) ([]Comment, error) {
	rc, stdout, _, err := bkproject.BdRun([]string{"bd", "comments", id, "--json"}, repo, 30*time.Second)
	if err != nil || rc != 0 {
		return nil, fmt.Errorf("bd comments %s: rc=%d err=%v", id, rc, err)
	}
	body := strings.TrimSpace(stdout)
	if body == "" || body == "[]" {
		return nil, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("bd comments %s output not parseable: %w", id, err)
	}
	out := make([]Comment, 0, len(raw))
	for _, r := range raw {
		c := Comment{
			Author:    pickStr(r, "author", "created_by", "user"),
			Body:      pickStr(r, "body", "text", "content"),
			CreatedAt: pickStr(r, "created_at", "createdAt", "timestamp"),
		}
		out = append(out, c)
	}
	return out, nil
}

func pickStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
