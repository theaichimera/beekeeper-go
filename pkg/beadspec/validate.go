package beadspec

import (
	"regexp"
	"strings"
)

// Severity classifies a finding. beadspec only emits Error today; the
// type exists so consumers (doctor/hooks) can map to their own scales
// and so future advisory rules can downgrade to Warn.
type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
)

// Bead is the minimal view beadspec validates. It deliberately does NOT
// depend on bk's bead model — callers project their record onto this.
type Bead struct {
	ID     string
	Type   string
	Labels []string
	Body   string // markdown description / body
}

// Finding is a single schema violation.
type Finding struct {
	BeadID   string   `json:"bead_id"`
	Type     string   `json:"type"`
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// ValidateBead loads the schema for b.Type and validates b against it.
// When no schema exists for the type, it returns (nil, err) where
// errors.Is(err, ErrNoSchema) is true — callers should treat that as
// "skip", not "fail".
func ValidateBead(b Bead) ([]Finding, error) {
	s, err := Load(b.Type)
	if err != nil {
		return nil, err
	}
	return Validate(b, s), nil
}

// Validate checks b against s and returns findings. Pure and
// deterministic: presence + shape only, no I/O, no quality judgment.
func Validate(b Bead, s Schema) []Finding {
	var out []Finding
	add := func(rule, msg string) {
		out = append(out, Finding{
			BeadID:   b.ID,
			Type:     b.Type,
			Rule:     rule,
			Severity: SeverityError,
			Message:  msg,
		})
	}

	if s.RequireBodyNonEmpty && strings.TrimSpace(b.Body) == "" {
		add("body.nonempty", "body/description is empty; rationale required")
	}
	for _, want := range s.RequireLabels {
		if !hasLabel(b.Labels, want) {
			add("labels.required", "missing required label: "+want)
		}
	}

	sections := parseSections(b.Body)
	for _, sec := range s.Sections {
		content, present := sections[normalizeHeading(sec.Heading)]
		if !present {
			if sec.Required {
				add("section.required", "missing required section: "+sec.Heading)
			}
			continue
		}
		if sec.NonEmpty && strings.TrimSpace(content) == "" {
			add("section.nonempty", "section is empty: "+sec.Heading)
		}
		if sec.ItemMustContain == "" && sec.ItemMustMatch == "" {
			continue
		}
		var re *regexp.Regexp
		if sec.ItemMustMatch != "" {
			// Invalid regexp -> skip the regexp rule rather than panic.
			re, _ = regexp.Compile(sec.ItemMustMatch)
		}
		for _, item := range listItems(content) {
			ok := true
			if sec.ItemMustContain != "" && !strings.Contains(item, sec.ItemMustContain) {
				ok = false
			}
			if re != nil && !re.MatchString(item) {
				ok = false
			}
			if !ok {
				add("section.item",
					"entry under "+sec.Heading+" must include \""+itemRuleDesc(sec)+"\": "+snippet(item))
			}
		}
	}
	return out
}

// --- helpers ------------------------------------------------------------

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func itemRuleDesc(sec SectionRule) string {
	if sec.ItemMustContain != "" {
		return sec.ItemMustContain
	}
	return sec.ItemMustMatch
}

func snippet(s string) string {
	s = strings.TrimSpace(s)
	const max = 60
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func normalizeHeading(h string) string {
	return strings.ToLower(strings.TrimSpace(h))
}

// headingLevel returns the markdown heading level (number of leading
// '#') and true when the line is a heading ("# ", "## ", ...).
func headingLevel(line string) (int, bool) {
	t := strings.TrimLeft(line, " ")
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n >= len(t) || t[n] != ' ' {
		return 0, false
	}
	return n, true
}

// parseSections splits a markdown body into a map of
// normalized-heading -> content. Every heading line (any level) starts
// a new addressable section whose content runs until the next heading
// of any level. This makes sections like "## Decisions" addressable
// regardless of whether a level-1 title precedes them.
func parseSections(body string) map[string]string {
	out := map[string]string{}
	curKey := ""
	var buf []string
	flush := func() {
		if curKey != "" {
			out[curKey] = strings.Join(buf, "\n")
		}
		buf = nil
	}
	for _, ln := range strings.Split(body, "\n") {
		if _, ok := headingLevel(ln); ok {
			flush()
			curKey = normalizeHeading(ln)
			continue
		}
		if curKey != "" {
			buf = append(buf, ln)
		}
	}
	flush()
	return out
}

var numberedItem = regexp.MustCompile(`^\d+[.)]\s`)

func isBullet(t string) bool {
	if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ") {
		return true
	}
	return numberedItem.MatchString(t)
}

func stripBullet(t string) string {
	if len(t) >= 2 && (t[0] == '-' || t[0] == '*' || t[0] == '+') && t[1] == ' ' {
		return strings.TrimSpace(t[2:])
	}
	if loc := numberedItem.FindStringIndex(t); loc != nil {
		return strings.TrimSpace(t[loc[1]:])
	}
	return t
}

// listItems extracts list items from section content. A bullet line
// plus any following indented / non-bullet continuation lines form one
// item; a blank line ends an item.
func listItems(content string) []string {
	var items []string
	var cur strings.Builder
	has := false
	push := func() {
		if has {
			items = append(items, strings.TrimSpace(cur.String()))
			cur.Reset()
			has = false
		}
	}
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case isBullet(t):
			push()
			cur.WriteString(stripBullet(t))
			has = true
		case t == "":
			push()
		case has:
			cur.WriteString(" " + t)
		}
	}
	push()
	return items
}
