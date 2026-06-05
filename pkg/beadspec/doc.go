// Package beadspec declaratively defines the required structure of a
// bead (by issue type) and validates a bead against that structure.
//
// It is the source of truth for "what a well-formed bead looks like".
// bk's doctor and hooks consume this package; bk is the engine,
// beadspec is the data + the pure validator.
//
// # Leaf-package contract
//
// beadspec imports NOTHING from beekeeper-go/internal/* or
// beekeeper-go/cmd/*. The dependency direction is one-way
// (bk -> beadspec), so the package can be lifted into its own repo by
// moving the folder once a second consumer appears. A guard test
// (leaf_test.go) enforces this.
//
// # Schema format
//
// Schemas live as TOML files under schemas/, embedded at build time
// via go:embed and loaded by type name (Load("epic") reads
// schemas/epic.toml). Adding a new bead type is a drop-in TOML file —
// no Go changes (a recompile picks up the embedded file).
//
// A schema describes, per bead type:
//
//	type                   = "epic"          # bead issue_type this applies to
//	description            = "..."           # human note
//	require_body_nonempty  = true            # description/body must be non-empty
//	require_labels         = ["progression"] # labels that must be present
//
//	[[sections]]                              # required markdown sections
//	heading           = "## Decisions"        # exact heading (matched case-insensitively)
//	required          = true                  # the section must exist
//	non_empty         = true                  # the section must have content
//	item_must_contain = "Why:"                # every list item must contain this substring
//	item_must_match   = "^\\d{4}-"            # OR: every list item must match this regexp
//
// # Validation semantics
//
// Validate is pure, deterministic, and checks PRESENCE + SHAPE only.
// It never judges quality or completeness — a linter cannot tell that a
// decision was omitted, or that a "Why:" is meaningful. Those belong to
// a review gate (human or advisory LLM), by design.
package beadspec
