# Agent skills for `bk`

Reusable, agent-agnostic skills for working with `bk`'s rationale &
progression workflows on top of the `bd` (beads) issue tracker. Each skill is
an intent-described prompt plus the exact `bk`/`bd` commands — no hardcoded
argument guessing.

These are **consumer** skills (how to *use* `bk`), curated for any team adopting
the rationale/progression contract. They are standalone: they reference only
`bk`, `bd`, and this repo's docs — no private or machine-specific paths.

## The skills

| Skill | Use when… |
|-------|-----------|
| [`bd-setup`](bd-setup/SKILL.md) | Setting a repo up for the gate, installing the `bk` pre-push hook, or wanting a `bk doctor` health read. |
| [`bd-onboard`](bd-onboard/SKILL.md) | Getting up to speed in an unfamiliar repo; seeds the first `Project understanding: <repo>` progression. |
| [`bd-rationale`](bd-rationale/SKILL.md) | Creating an epic, recording the "why" of a decision, or unblocking an epic missing its `## Decisions` section. |
| [`bd-progression`](bd-progression/SKILL.md) | Creating, appending to, or refreshing a progression bead. |

## Using them with your agent

These follow the common `SKILL.md` (YAML frontmatter `name` + `description`)
convention. Most coding agents discover skills by intent from a skills
directory. To make them available, copy the skill folders into wherever your
agent loads skills from, e.g.:

```bash
# project-scoped (Claude Code example)
cp -r skills/bd-* /path/to/your/repo/.claude/skills/

# or user-scoped (available in every session)
cp -r skills/bd-* ~/.claude/skills/
```

## Background

The model these skills assume — what rationale and progressions are, and how
`bk` enforces them — is documented in
[`docs/rationale-and-progressions.md`](../docs/rationale-and-progressions.md).
Schema source of truth: `pkg/beadspec/schemas/{epic,progression}.toml`.

In one sentence: **`bk` enforces that the rationale exists; the agent (or human)
writes what it says.** `bk` is a linter/gate — it has no LLM and does no
extraction.
