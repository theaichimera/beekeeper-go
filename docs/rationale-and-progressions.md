# Rationale & Progressions

Two distinct, durable knowledge artifacts live on top of beads. They are
different in *temporality* and *scope*, and bk enforces their structure
from `pkg/beadspec` (the schema), surfaced by `bk doctor` and hard-gated
by the pre-push hook.

## Rationale — the "why" of a decision (lives in the epic)

- **Scope:** one decision. **Temporality:** point-in-time, frozen.
- Authored *at epic creation*, next to the "what".
- Captured as a `## Decisions` section in the epic body; every entry
  pairs a decision with its reason:

  ```
  ## Decisions
  - Decision: <what we chose> — Why: <the reason at the time>
  ```

- Enforced (`epic` schema): the body must be non-empty, a `## Decisions`
  section must exist, and **every entry must contain `Why:`**.
- `bk guard beadspec` exits non-zero on epic violations, so the pre-push
  hook blocks the push (bypass: `BEADKEEPER_SKIP_HOOK=1`).

## Progression — the "why" of an arc (its own bead)

- **Scope:** a topic that may span many epics/PRs. **Temporality:**
  living, self-correcting.
- A dedicated bead labeled `progression`, NOT owned by a single epic.
- Body shape (`progression` schema):

  ```
  ## Current understanding
  <the synthesized "where we are now" — keep rewriting this>

  ## Log
  - 2026-06-05 baseline: ...
  - 2026-06-06 pivot: ...
  ```

- Log entry types: `baseline | deepening | pivot | correction`.
- Kept OFF the work board (`bk board` / ready / summary exclude the
  `progression` label) — living documents are not actionable work.

### Scaffolding

```
bk progression new "<topic>"                       # creates the labeled bead
bk progression add <id> --type pivot "<message>"   # appends a dated log entry
bk progression list                                # lists progressions
```

bk scaffolds the structure; **you (or the agent) author the content.** A
linter enforces presence + shape, never quality — completeness and
whether a `Why:` is meaningful belong to review (human or advisory LLM).

## Where this is defined

- Schema source of truth: `pkg/beadspec/schemas/{epic,progression}.toml`.
- Validation: `pkg/beadspec` (pure) + `internal/beadlint` (plumbing),
  consumed by both `bk doctor` and `bk guard beadspec`.

## Agent skills

Reusable, agent-agnostic skills for these workflows (setup, onboarding,
authoring rationale, maintaining progressions) live in [`skills/`](../skills).
Copy them into your agent's skills directory to make the workflows
intent-discoverable.
