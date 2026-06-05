package beadspec

import (
	"errors"
	"testing"
)

const conformingEpic = `# Some epic

Intro prose.

## Decisions
- Decision: use TOML for schemas — Why: BurntSushi/toml is already a dep.
- Decision: beadspec is a leaf package — Why: one-way dep enables extraction.

## Acceptance
- builds.
`

const conformingProgression = `## Current understanding
We consolidated progressions onto beads.

## Log
- 2026-06-05 baseline: audited the legacy library.
- 2026-06-05 pivot: schema-driven enforcement.
`

func rules(fs []Finding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.Rule]++
	}
	return m
}

func TestValidate_Epic(t *testing.T) {
	epicSchema, err := Load("epic")
	if err != nil {
		t.Fatalf("Load(epic): %v", err)
	}
	cases := []struct {
		name      string
		bead      Bead
		wantRules map[string]int
	}{
		{
			name: "conforming",
			bead: Bead{ID: "e1", Type: "epic", Body: conformingEpic},
		},
		{
			name:      "missing decisions section",
			bead:      Bead{ID: "e2", Type: "epic", Body: "# t\n\n## Acceptance\n- ok\n"},
			wantRules: map[string]int{"section.required": 1},
		},
		{
			name: "decision without Why",
			bead: Bead{ID: "e3", Type: "epic", Body: "## Decisions\n- Decision: do X.\n- Decision: do Z — Why: reason.\n"},
			// first item lacks "Why:" -> one section.item finding.
			wantRules: map[string]int{"section.item": 1},
		},
		{
			name:      "empty body",
			bead:      Bead{ID: "e4", Type: "epic", Body: "   "},
			wantRules: map[string]int{"body.nonempty": 1, "section.required": 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rules(Validate(tc.bead, epicSchema))
			if tc.wantRules == nil {
				if len(got) != 0 {
					t.Fatalf("expected no findings, got %v", got)
				}
				return
			}
			for rule, n := range tc.wantRules {
				if got[rule] != n {
					t.Errorf("rule %q: got %d, want %d (all=%v)", rule, got[rule], n, got)
				}
			}
			if len(got) != len(tc.wantRules) {
				t.Errorf("finding rule set mismatch: got %v, want %v", got, tc.wantRules)
			}
		})
	}
}

func TestValidate_Progression(t *testing.T) {
	s, err := Load("progression")
	if err != nil {
		t.Fatalf("Load(progression): %v", err)
	}
	cases := []struct {
		name      string
		bead      Bead
		wantRules map[string]int
	}{
		{
			name: "conforming",
			bead: Bead{ID: "p1", Type: "progression", Labels: []string{"progression"}, Body: conformingProgression},
		},
		{
			name:      "missing label",
			bead:      Bead{ID: "p2", Type: "progression", Body: conformingProgression},
			wantRules: map[string]int{"labels.required": 1},
		},
		{
			name:      "missing current understanding",
			bead:      Bead{ID: "p3", Type: "progression", Labels: []string{"progression"}, Body: "## Log\n- 2026-06-05 baseline: x.\n"},
			wantRules: map[string]int{"section.required": 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rules(Validate(tc.bead, s))
			if tc.wantRules == nil {
				if len(got) != 0 {
					t.Fatalf("expected no findings, got %v", got)
				}
				return
			}
			for rule, n := range tc.wantRules {
				if got[rule] != n {
					t.Errorf("rule %q: got %d, want %d (all=%v)", rule, got[rule], n, got)
				}
			}
		})
	}
}

func TestValidateBead_UnknownType(t *testing.T) {
	_, err := ValidateBead(Bead{ID: "x", Type: "no-such-type"})
	if !errors.Is(err, ErrNoSchema) {
		t.Fatalf("want ErrNoSchema, got %v", err)
	}
}

func TestTypes(t *testing.T) {
	got := Types()
	want := map[string]bool{"epic": true, "progression": true}
	for _, ty := range got {
		delete(want, ty)
	}
	if len(want) != 0 {
		t.Fatalf("Types() missing %v (got %v)", want, got)
	}
}
