package beadspec

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed schemas/*.toml
var schemaFS embed.FS

// ErrNoSchema is returned (wrapped) by Load when no schema is defined
// for the requested bead type. Callers that validate arbitrary beads
// (e.g. doctor) should treat errors.Is(err, ErrNoSchema) as "skip this
// bead", not as a failure.
var ErrNoSchema = errors.New("beadspec: no schema for bead type")

// SectionRule describes a required markdown section within a bead body.
type SectionRule struct {
	// Heading is the exact section heading, including leading '#'
	// markers, e.g. "## Decisions". Matched case-insensitively.
	Heading string `toml:"heading"`
	// Required fails validation when the section is absent.
	Required bool `toml:"required"`
	// NonEmpty fails validation when the section exists but has no
	// content beneath it.
	NonEmpty bool `toml:"non_empty"`
	// ItemMustContain requires every list item under the section to
	// contain this substring (e.g. "Why:"). Empty disables the check.
	ItemMustContain string `toml:"item_must_contain"`
	// ItemMustMatch requires every list item under the section to match
	// this regexp. Empty disables the check. Invalid regexps are
	// ignored (the rule is skipped) rather than panicking.
	ItemMustMatch string `toml:"item_must_match"`
}

// Schema is the declarative shape definition for one bead type.
type Schema struct {
	Type                string        `toml:"type"`
	Description         string        `toml:"description"`
	RequireBodyNonEmpty bool          `toml:"require_body_nonempty"`
	RequireLabels       []string      `toml:"require_labels"`
	Sections            []SectionRule `toml:"sections"`
}

// Load returns the embedded schema for the given bead type. When no
// schema exists for the type, the returned error wraps ErrNoSchema.
func Load(beadType string) (Schema, error) {
	var s Schema
	data, err := schemaFS.ReadFile("schemas/" + beadType + ".toml")
	if err != nil {
		return s, fmt.Errorf("%w: %q", ErrNoSchema, beadType)
	}
	if err := toml.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("beadspec: parsing schema for %q: %w", beadType, err)
	}
	return s, nil
}

// Has reports whether a schema is defined for the given bead type.
func Has(beadType string) bool {
	_, err := schemaFS.ReadFile("schemas/" + beadType + ".toml")
	return err == nil
}

// Types lists every bead type that has an embedded schema, sorted.
func Types() []string {
	entries, err := fs.ReadDir(schemaFS, "schemas")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".toml") {
			out = append(out, strings.TrimSuffix(name, ".toml"))
		}
	}
	sort.Strings(out)
	return out
}
