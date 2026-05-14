# Triage Agent: Contextual Labels (Issue #426)

The triage agent should opportunistically apply (or remove) non-control labels
to issues it triages. Teams customize labeling behavior by providing their own
`issue-labels` skill at the org or repo level, shadowing the OOTB skill.

## Problem

Today the triage post script applies only control labels (`needs-info`,
`ready-to-code`, `triaged`, `blocked`, `duplicate`). Teams that use additional
labels (e.g. `area/api`, `priority/high`, `lang/go`) must apply them manually
after triage. The agent already reads the issue deeply enough to make good
label recommendations, but has no channel to express them.

## Approach

1. Add a `label_actions` field to the triage result schema.
2. Ship an OOTB `issue-labels` skill that teaches the agent to discover and
   recommend labels.
3. The post script applies non-control labels and refuses control labels with
   warning annotations.
4. The agent's reason sentence is appended to its comment automatically.

## Schema changes

Add an optional `label_actions` property at the top level of
`triage-result.schema.json`, available for all actions:

```json
"label_actions": {
  "type": "object",
  "required": ["reason", "actions"],
  "properties": {
    "reason": {
      "type": "string",
      "minLength": 1,
      "description": "Single sentence explaining why these labels are being applied or removed"
    },
    "actions": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "required": ["action", "label"],
        "properties": {
          "action": { "type": "string", "enum": ["add", "remove"] },
          "label": { "type": "string", "minLength": 1 }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

`label_actions` is optional. Its absence means no label recommendations.
When present, `reason` and at least one action are required.

## OOTB `issue-labels` skill

Location: `skills/issue-labels/SKILL.md`

The skill instructs the agent to:

1. Discover available labels via `gh label list --repo OWNER/REPO --json name,description --limit 100`.
2. Spawn a sub-agent to research labeling conventions: query recent closed and
   open issues, analyze which labels appear together and in what contexts, and
   return a concise summary (under 500 characters) of observed patterns. The
   sub-agent keeps raw issue data out of the parent context window.
3. Based on the issue content and the observed conventions, recommend labels to
   add or remove.
4. Emit recommendations in the `label_actions` field. Write one concise
   sentence for `reason`.
5. If no labels clearly apply, omit `label_actions` entirely rather than
   guessing.

The skill explicitly tells the agent not to recommend control labels and not to
recommend labels that don't exist in the repo.

### Shadowing

An org admin replaces `skills/issue-labels/SKILL.md` in their `.fullsend` repo
with a bespoke skill that encodes their team's labeling taxonomy and rules. The
agent definition always references `issue-labels` -- no conditional logic. The
bespoke skill replaces the OOTB one entirely.

## Agent definition changes (`agents/triage.md`)

- Add `issue-labels` to the `skills:` frontmatter list.
- Add `label_actions` to the JSON examples in Step 4 (shown as optional).
- Add to output rules: "If you have label recommendations from the
  `issue-labels` skill, include them in the `label_actions` field. If no labels
  clearly apply, omit `label_actions` entirely."
- Add to comment content rules: "If you include `label_actions`, the pipeline
  will append your label reason to the comment automatically -- do not include
  label justifications in the `comment` field yourself."

## Harness changes (`harness/triage.yaml`)

Change `skills: []` to `skills: [skills/issue-labels]`.

## Post script changes (`scripts/post-triage.sh`)

Restructure the script so the comment is posted once, at the end, after all
label processing is complete.

Current flow:

```
case action in
  insufficient) post comment, apply control labels ;;
  duplicate)    post comment, apply control labels, close ;;
  blocked)      post comment, apply control labels ;;
  sufficient)   post comment, apply control labels ;;
esac
```

New flow:

```
case action in
  insufficient) apply control labels ;;
  duplicate)    apply control labels, close ;;
  blocked)      apply control labels ;;
  sufficient)   apply control labels ;;
esac

process label_actions (if present):
  for each entry in .label_actions.actions:
    if label is a control label:
      emit ::warning:: annotation, skip
    else:
      add_label or remove_label
  append .label_actions.reason to COMMENT

post comment (once, with assembled body)
```

### Control label list

The post script refuses to add or remove these labels via `label_actions`
(same set that `pre-triage.sh` resets, plus `blocked` and `triaged`):

- `needs-info`
- `ready-to-code`
- `duplicate`
- `not-ready`
- `not-reproducible`
- `type/feature`
- `blocked`
- `triaged`

### Warning annotation format

```
::warning::Refused to {add|remove} control label '{label}' -- control labels are managed by the triage pipeline
```

## Test changes

### `scripts/post-triage-test.sh`

Five new test cases:

1. **label-actions-applied** -- `sufficient` result with `label_actions`
   containing add/remove of non-control labels. Verify `gh api` calls for
   each.
2. **label-actions-control-label-refused** -- `label_actions` tries to add
   `ready-to-code`. Verify the label API is not called for it via
   `label_actions` (the control label is handled by the case block).
3. **label-actions-absent** -- result with no `label_actions` field. Verify no
   extra label API calls beyond the control labels.
4. **label-actions-with-insufficient** -- `insufficient` action with
   `label_actions`. Verify non-control labels are still applied (label actions
   work for all actions).
5. **label-actions-reason-appended** -- verify the comment body passed to
   `fullsend post-comment` includes the label reason sentence.

### `internal/scaffold/scaffold_test.go`

Add `skills/issue-labels/SKILL.md` to the expected file list.

## Files changed

| File | Change |
|------|--------|
| `schemas/triage-result.schema.json` | Add optional `label_actions` object |
| `skills/issue-labels/SKILL.md` | New OOTB skill |
| `agents/triage.md` | Add skill reference and `label_actions` to examples/rules |
| `harness/triage.yaml` | Add `skills/issue-labels` to skills list |
| `scripts/post-triage.sh` | Restructure comment posting; process `label_actions` |
| `scripts/post-triage-test.sh` | 5 new test cases |
| `internal/scaffold/scaffold_test.go` | Add skill to expected file list |

## Not changed

- `scripts/pre-triage.sh` -- resets control labels only; contextual labels
  persist across re-triage intentionally (the agent can remove them via
  `label_actions` if they no longer apply).
