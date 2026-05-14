# Triage Contextual Labels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the triage agent opportunistically apply non-control labels to issues, customizable via a shadowable `issue-labels` skill.

**Architecture:** A new optional `label_actions` field in the triage result schema carries label add/remove recommendations from the agent. The post script processes them after control labels, refusing control label mutations with warning annotations. An OOTB `issue-labels` skill teaches the agent how to discover and recommend labels; teams replace it to customize.

**Tech Stack:** Bash, JSON Schema, Claude agent skills (Markdown)

---

## File structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/scaffold/fullsend-repo/schemas/triage-result.schema.json` | Modify | Add `label_actions` property and `$defs/label_actions` |
| `internal/scaffold/fullsend-repo/skills/issue-labels/SKILL.md` | Create | OOTB opportunistic labeling skill |
| `internal/scaffold/fullsend-repo/agents/triage.md` | Modify | Reference skill, add `label_actions` to examples and rules |
| `internal/scaffold/fullsend-repo/harness/triage.yaml` | Modify | Add skill path to skills list |
| `internal/scaffold/fullsend-repo/scripts/post-triage.sh` | Modify | Restructure comment posting; process label_actions |
| `internal/scaffold/fullsend-repo/scripts/post-triage-test.sh` | Modify | Add 5 new test cases |
| `internal/scaffold/scaffold_test.go` | Modify | Add skill to expected file list |

All paths below are relative to `internal/scaffold/fullsend-repo/` unless they start with `internal/`.

---

### Task 1: Add `label_actions` to the JSON schema

**Files:**
- Modify: `internal/scaffold/fullsend-repo/schemas/triage-result.schema.json`

- [ ] **Step 1: Add the `label_actions` property and its `$defs` entry**

Add a `label_actions` property at line 37 (after `blocked_by`) and a new `$defs` entry. The full file becomes:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "triage-result.schema.json",
  "title": "Triage Agent Result",
  "description": "Structured output from the triage agent, validated by the harness before the post-script runs (ADR 0022).",
  "type": "object",
  "additionalProperties": false,
  "required": ["action", "reasoning", "comment"],
  "properties": {
    "action": {
      "type": "string",
      "enum": ["insufficient", "duplicate", "sufficient", "blocked"]
    },
    "reasoning": {
      "type": "string",
      "minLength": 1
    },
    "comment": {
      "type": "string",
      "minLength": 1,
      "maxLength": 16384
    },
    "clarity_scores": {
      "$ref": "#/$defs/clarity_scores"
    },
    "duplicate_of": {
      "type": "integer",
      "minimum": 1
    },
    "triage_summary": {
      "$ref": "#/$defs/triage_summary"
    },
    "blocked_by": {
      "type": "string",
      "pattern": "^https://github\\.com/[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+/(issues|pull)/[0-9]+$",
      "description": "HTML URL of the blocking issue or PR (e.g., https://github.com/org/repo/issues/99 or https://github.com/org/repo/pull/55)"
    },
    "label_actions": {
      "$ref": "#/$defs/label_actions"
    }
  },
  "allOf": [
    {
      "if": { "properties": { "action": { "const": "insufficient" } }, "required": ["action"] },
      "then": { "required": ["clarity_scores"] }
    },
    {
      "if": { "properties": { "action": { "const": "duplicate" } }, "required": ["action"] },
      "then": { "required": ["duplicate_of"] }
    },
    {
      "if": { "properties": { "action": { "const": "sufficient" } }, "required": ["action"] },
      "then": { "required": ["clarity_scores", "triage_summary"] }
    },
    {
      "if": { "properties": { "action": { "const": "blocked" } }, "required": ["action"] },
      "then": { "required": ["blocked_by"] }
    }
  ],
  "$defs": {
    "clarity_scores": {
      "type": "object",
      "required": ["symptom", "cause", "reproduction", "impact", "overall"],
      "properties": {
        "symptom": { "type": "number", "minimum": 0, "maximum": 1 },
        "cause": { "type": "number", "minimum": 0, "maximum": 1 },
        "reproduction": { "type": "number", "minimum": 0, "maximum": 1 },
        "impact": { "type": "number", "minimum": 0, "maximum": 1 },
        "overall": { "type": "number", "minimum": 0, "maximum": 1 }
      },
      "additionalProperties": false
    },
    "triage_summary": {
      "type": "object",
      "required": ["title", "severity", "category", "problem", "root_cause_hypothesis",
                    "reproduction_steps", "impact", "recommended_fix", "proposed_test_case"],
      "properties": {
        "title": { "type": "string", "minLength": 1 },
        "severity": { "type": "string", "enum": ["critical", "high", "medium", "low"] },
        "category": { "type": "string", "enum": ["bug", "performance", "security", "documentation", "feature", "other"] },
        "problem": { "type": "string", "minLength": 1 },
        "root_cause_hypothesis": { "type": "string", "minLength": 1 },
        "reproduction_steps": { "type": "array", "items": { "type": "string" }, "minItems": 1 },
        "environment": { "type": "string" },
        "impact": { "type": "string", "minLength": 1 },
        "recommended_fix": { "type": "string", "minLength": 1 },
        "proposed_test_case": { "type": "string", "minLength": 1 }
      },
      "additionalProperties": false
    },
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
  }
}
```

- [ ] **Step 2: Verify the schema is valid JSON**

Run: `python3 -c "import json; json.load(open('internal/scaffold/fullsend-repo/schemas/triage-result.schema.json'))"`
Expected: no output (success)

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/fullsend-repo/schemas/triage-result.schema.json
git commit -m "feat(triage): add label_actions to triage result schema (#426)"
```

---

### Task 2: Create the OOTB `issue-labels` skill

**Files:**
- Create: `internal/scaffold/fullsend-repo/skills/issue-labels/SKILL.md`

- [ ] **Step 1: Create the skill directory and file**

```markdown
---
name: issue-labels
description: >-
  Discover repository labels and recommend contextual labels to add or remove
  on triaged issues. Produces label_actions in the agent result JSON.
---

# Issue Labels

Recommend contextual labels for the issue being triaged. These are labels that
describe the issue's domain, area, priority, or other team-specific dimensions
-- NOT control labels used by the triage pipeline.

## Control labels (do NOT recommend these)

The following labels are managed by the triage pipeline. Never include them in
your `label_actions` output -- the post script will refuse them:

- `needs-info`
- `ready-to-code`
- `duplicate`
- `not-ready`
- `not-reproducible`
- `type/feature`
- `blocked`
- `triaged`

## Step 1: Discover available labels

```
gh label list --repo OWNER/REPO --json name,description --limit 100
```

If the repo has no non-control labels, skip labeling entirely -- do not emit
`label_actions`.

## Step 2: Research labeling conventions

Spawn a sub-agent to investigate how labels have been applied to recent issues.
The sub-agent should:

1. Query recent closed and open issues:
   ```
   gh issue list --repo OWNER/REPO --state all --json number,title,labels --limit 50
   ```
2. Analyze which labels appear together and in what contexts.
3. Return a short summary (under 500 characters) describing the labeling
   conventions observed -- which labels are commonly used and any patterns in
   how they are applied.

Do not dump raw issue data into the parent context. Only use the sub-agent's
summary to inform your recommendations.

## Step 3: Recommend labels

Based on the issue content, the available labels, and the observed conventions:

- Recommend labels to **add** if they clearly apply to this issue.
- Recommend labels to **remove** if the issue already has stale labels from a
  prior triage that no longer apply.
- If no labels clearly apply, do not emit `label_actions` at all. Silence is
  better than noise.
- Only recommend labels that exist in `gh label list`. Do not invent labels.

## Output

Include your recommendations in the `label_actions` field of the agent result
JSON:

```json
"label_actions": {
  "reason": "Single sentence explaining the label choices for the whole batch.",
  "actions": [
    { "action": "add", "label": "area/api" },
    { "action": "remove", "label": "area/cli" }
  ]
}
```

Write one concise sentence for `reason` that justifies the batch. Do not
include label justifications in the `comment` field -- the pipeline appends the
reason automatically.
```

- [ ] **Step 2: Verify the file exists and is not empty**

Run: `test -s internal/scaffold/fullsend-repo/skills/issue-labels/SKILL.md && echo OK`
Expected: `OK`

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/fullsend-repo/skills/issue-labels/SKILL.md
git commit -m "feat(triage): add OOTB issue-labels skill (#426)"
```

---

### Task 3: Wire the skill into the agent definition and harness

**Files:**
- Modify: `internal/scaffold/fullsend-repo/agents/triage.md:1-7` (frontmatter)
- Modify: `internal/scaffold/fullsend-repo/agents/triage.md:114-196` (Step 4 JSON examples)
- Modify: `internal/scaffold/fullsend-repo/agents/triage.md:207-211` (output rules)
- Modify: `internal/scaffold/fullsend-repo/agents/triage.md:213-220` (comment content rules)
- Modify: `internal/scaffold/fullsend-repo/harness/triage.yaml:19` (skills list)

- [ ] **Step 1: Update the agent frontmatter**

Change line 4 from:
```
skills: []
```
to:
```
skills:
  - issue-labels
```

- [ ] **Step 2: Add `label_actions` to all four JSON examples in Step 4**

After the last field in each JSON example block, add a comment showing `label_actions` is optional. Only show the full structure in the `sufficient` example. For the other three, add a one-line comment.

In the `insufficient` example (around line 134), after the `"comment"` line, add:

```json
  "label_actions": "(optional — see issue-labels skill)"
```

Actually, since the JSON must be valid in the examples and `label_actions` is optional, the cleanest approach is to add it only to the `sufficient` example as a concrete illustration. Add after the `"comment"` field (before the closing `}`):

```json
  "label_actions": {
    "reason": "This API issue matches the area/api and priority/high labels based on repo conventions.",
    "actions": [
      { "action": "add", "label": "area/api" },
      { "action": "add", "label": "priority/high" }
    ]
  }
```

Then add a note below all four examples (before "## Questioning guidelines"):

```markdown
**Label recommendations (optional, all actions):** If the `issue-labels` skill identifies labels that should be applied or removed, include them in the `label_actions` field. This field is optional for all actions. If no labels clearly apply, omit it entirely.
```

- [ ] **Step 3: Add label rule to output rules section**

After line 211 (`Do NOT post comments, apply labels, or modify the issue in any way...`), add:

```markdown
- If you have label recommendations from the `issue-labels` skill, include them in the `label_actions` field. If no labels clearly apply, omit `label_actions` entirely.
```

- [ ] **Step 4: Add label rule to comment content rules section**

After the last bullet in the comment content rules (line 220), add:

```markdown
- If you include `label_actions`, the pipeline appends your label reason to the comment automatically — do not include label justifications in the `comment` field yourself.
```

- [ ] **Step 5: Update the harness YAML**

Change line 19 of `harness/triage.yaml` from:
```yaml
skills: []
```
to:
```yaml
skills:
  - skills/issue-labels
```

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/fullsend-repo/agents/triage.md internal/scaffold/fullsend-repo/harness/triage.yaml
git commit -m "feat(triage): wire issue-labels skill into agent and harness (#426)"
```

---

### Task 4: Restructure `post-triage.sh` to process label actions

**Files:**
- Modify: `internal/scaffold/fullsend-repo/scripts/post-triage.sh`

This is the largest change. The key restructuring: move comment posting out of each `case` branch and into a single post at the end, after `label_actions` processing.

- [ ] **Step 1: Write the failing test for label-actions-applied**

Add to `post-triage-test.sh` (before the `# --- Summary ---` section):

```bash
run_test "label-actions-applied" \
  '{"action":"sufficient","reasoning":"all clear","clarity_scores":{"symptom":0.9,"cause":0.85,"reproduction":0.9,"impact":0.8,"overall":0.87},"triage_summary":{"title":"Fix crash","severity":"high","category":"bug","problem":"Crash","root_cause_hypothesis":"Buffer overflow","reproduction_steps":["step 1"],"environment":"Linux","impact":"All users","recommended_fix":"Fix buffer","proposed_test_case":"test_crash"},"comment":"## Triage Summary\n\nReady.","label_actions":{"reason":"API crash matches area/api label.","actions":[{"action":"add","label":"area/api"}]}}' \
  "gh api repos/test-org/test-repo/issues/42/labels -f labels[]=area/api --silent"
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `bash internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`
Expected: `FAIL: label-actions-applied`

- [ ] **Step 3: Rewrite `post-triage.sh`**

Replace the full contents of `post-triage.sh` with:

```bash
#!/usr/bin/env bash
# post-triage.sh — Parse triage agent JSON output and perform GitHub mutations.
#
# Runs on the host after sandbox cleanup. Working directory is the fullsend
# run output directory (e.g., /tmp/fullsend/agent-triage-<id>/iteration-1/).
#
# Required env vars:
#   GITHUB_ISSUE_URL  — HTML URL of the issue (e.g., https://github.com/org/repo/issues/42)
#   GH_TOKEN          — GitHub token with issues read/write scope
#
# The agent writes its decision to output/agent-result.json (relative to
# the iteration directory). This script finds the most recent iteration's output.
#
# IMPORTANT: Label mutations use the labels API directly (gh api) instead of
# gh issue edit. gh issue edit uses PATCH /issues/{number} which fires
# issues.edited, re-triggering the triage dispatch in the shim workflow.
# The labels API (POST/DELETE /issues/{number}/labels) only fires
# issues.labeled/issues.unlabeled, avoiding the re-triage loop.

set -euo pipefail

# Find the triage result JSON. The run dir contains iteration-N/ subdirectories;
# we want the last one's output.
RESULT_FILE=""
for dir in iteration-*/output; do
  if [[ -f "${dir}/agent-result.json" ]]; then
    RESULT_FILE="${dir}/agent-result.json"
  fi
done

if [[ -z "${RESULT_FILE}" ]]; then
  echo "ERROR: agent-result.json not found in any iteration output directory"
  exit 1
fi

echo "Reading triage result from: ${RESULT_FILE}"

# Validate JSON is parseable.
if ! jq empty "${RESULT_FILE}" 2>/dev/null; then
  echo "ERROR: ${RESULT_FILE} is not valid JSON"
  exit 1
fi

ACTION=$(jq -r '.action' "${RESULT_FILE}")
COMMENT=$(jq -r '.comment // empty' "${RESULT_FILE}")

# Validate and extract repo and issue number from the HTML URL.
# GITHUB_ISSUE_URL is e.g. https://github.com/org/repo/issues/42
if [[ ! "${GITHUB_ISSUE_URL}" =~ ^https://github\.com/[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+/issues/[0-9]+$ ]]; then
  echo "ERROR: GITHUB_ISSUE_URL does not match expected pattern: ${GITHUB_ISSUE_URL}"
  exit 1
fi
REPO=$(echo "${GITHUB_ISSUE_URL}" | sed 's|https://github.com/||; s|/issues/.*||')
ISSUE_NUMBER=$(basename "${GITHUB_ISSUE_URL}")

echo "Action: ${ACTION}"
echo "Repo: ${REPO}"
echo "Issue: #${ISSUE_NUMBER}"

# add_label uses the labels API to avoid firing issues.edited.
add_label() {
  if ! gh api "repos/${REPO}/issues/${ISSUE_NUMBER}/labels" -f "labels[]=$1" --silent; then
    echo "ERROR: failed to add label '$1' to issue #${ISSUE_NUMBER}" >&2
    exit 1
  fi
}

# remove_label silently removes a label (no error if absent).
remove_label() {
  gh api "repos/${REPO}/issues/${ISSUE_NUMBER}/labels/$1" -X DELETE --silent 2>/dev/null || true
}

# Control labels managed by the triage pipeline. The post script refuses to
# add or remove these via label_actions (same set that pre-triage.sh resets,
# plus blocked and triaged).
CONTROL_LABELS="needs-info ready-to-code duplicate not-ready not-reproducible type/feature blocked triaged"

is_control_label() {
  local label="$1"
  for cl in ${CONTROL_LABELS}; do
    if [[ "${cl}" == "${label}" ]]; then
      return 0
    fi
  done
  return 1
}

# --- Action-specific validation and control labels ---

case "${ACTION}" in
  insufficient)
    if [[ -z "${COMMENT}" ]]; then
      echo "ERROR: action is 'insufficient' but no comment provided"
      exit 1
    fi
    remove_label "blocked"
    add_label "needs-info"
    ;;

  duplicate)
    if [[ -z "${COMMENT}" ]]; then
      echo "ERROR: action is 'duplicate' but no comment provided"
      exit 1
    fi
    DUPLICATE_OF=$(jq -r '.duplicate_of' "${RESULT_FILE}")
    if [[ "${DUPLICATE_OF}" -eq "${ISSUE_NUMBER}" ]]; then
      echo "ERROR: issue cannot be a duplicate of itself (#${ISSUE_NUMBER})"
      exit 1
    fi
    remove_label "blocked"
    add_label "duplicate"
    ;;

  blocked)
    # NOTE: There is no automatic mechanism to remove the "blocked" label when
    # the blocking issue is resolved. Currently, editing the issue re-triggers
    # triage, and the agent checks whether existing blockers are still open
    # (Step 2c in triage.md). A scheduled workflow to check blocked issues
    # periodically would be a more complete solution. (See review notes.)
    if [[ -z "${COMMENT}" ]]; then
      echo "ERROR: action is 'blocked' but no comment provided"
      exit 1
    fi
    BLOCKED_BY=$(jq -r '.blocked_by // empty' "${RESULT_FILE}")
    if [[ -z "${BLOCKED_BY}" ]]; then
      echo "ERROR: action is 'blocked' but no blocked_by URL provided"
      exit 1
    fi
    echo "Blocked by: ${BLOCKED_BY}"
    remove_label "ready-to-code"
    remove_label "needs-info"
    add_label "blocked"
    ;;

  sufficient)
    if [[ -z "${COMMENT}" ]]; then
      echo "ERROR: action is 'sufficient' but no comment provided"
      exit 1
    fi

    # Guard: reject sufficient results that contain information_gaps.
    # If the agent identified open questions, it should have used "insufficient".
    GAP_COUNT=$(jq '.triage_summary.information_gaps // [] | length' "${RESULT_FILE}")
    if [[ "${GAP_COUNT}" -gt 0 ]]; then
      echo "ERROR: action is 'sufficient' but triage_summary contains ${GAP_COUNT} information_gaps — open questions must block triage"
      exit 1
    fi

    remove_label "blocked"
    remove_label "needs-info"

    # Low-risk categories (bug, documentation, performance) auto-promote to
    # ready-to-code, which triggers the code agent. Feature work and anything
    # else receives the triaged label and waits for human prioritization
    # (per #561, only feature issues should require human review before coding).
    CATEGORY=$(jq -r '.triage_summary.category // "unknown"' "${RESULT_FILE}")
    echo "Category: ${CATEGORY}"
    case "${CATEGORY}" in
      bug|documentation|performance)
        echo "Applying ready-to-code label (${CATEGORY})..."
        add_label "ready-to-code"
        ;;
      *)
        echo "Applying triaged label (${CATEGORY})..."
        add_label "triaged"
        ;;
    esac
    ;;

  *)
    echo "ERROR: unknown action '${ACTION}' — this may be a newer action that post-triage.sh does not handle yet"
    exit 1
    ;;
esac

# --- Process label_actions (applies to all actions) ---

HAS_LABEL_ACTIONS=$(jq 'has("label_actions")' "${RESULT_FILE}")
if [[ "${HAS_LABEL_ACTIONS}" == "true" ]]; then
  LABEL_REASON=$(jq -r '.label_actions.reason' "${RESULT_FILE}")
  LABEL_COUNT=$(jq '.label_actions.actions | length' "${RESULT_FILE}")

  echo "Processing ${LABEL_COUNT} label action(s)..."

  for i in $(seq 0 $((LABEL_COUNT - 1))); do
    LA_ACTION=$(jq -r ".label_actions.actions[${i}].action" "${RESULT_FILE}")
    LA_LABEL=$(jq -r ".label_actions.actions[${i}].label" "${RESULT_FILE}")

    if is_control_label "${LA_LABEL}"; then
      echo "::warning::Refused to ${LA_ACTION} control label '${LA_LABEL}' -- control labels are managed by the triage pipeline"
      continue
    fi

    case "${LA_ACTION}" in
      add)
        echo "Adding label '${LA_LABEL}'..."
        add_label "${LA_LABEL}"
        ;;
      remove)
        echo "Removing label '${LA_LABEL}'..."
        remove_label "${LA_LABEL}"
        ;;
    esac
  done

  # Append the label reason to the comment.
  COMMENT="${COMMENT}

---
**Labels:** ${LABEL_REASON}"
fi

# --- Post comment ---

echo "Posting comment..."
if [[ "${ACTION}" == "blocked" ]]; then
  # Blocked uses plain gh issue comment (no marker-based upsert).
  printf '%s' "${COMMENT}" | gh issue comment "${ISSUE_NUMBER}" --repo "${REPO}" --body-file -
else
  printf '%s' "${COMMENT}" | fullsend post-comment --repo "${REPO}" --number "${ISSUE_NUMBER}" --marker "<!-- fullsend:triage-agent -->" --token "${GH_TOKEN}" --result -
fi

# --- Post-action: close duplicate issues ---

if [[ "${ACTION}" == "duplicate" ]]; then
  gh issue close "${ISSUE_NUMBER}" --repo "${REPO}" --reason "duplicate"
fi

echo "Post-triage complete."
```

- [ ] **Step 4: Run the test to verify label-actions-applied passes**

Run: `bash internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`
Expected: `label-actions-applied` PASS. All existing tests should also pass (the restructuring preserves behavior).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/fullsend-repo/scripts/post-triage.sh internal/scaffold/fullsend-repo/scripts/post-triage-test.sh
git commit -m "feat(triage): process label_actions in post script (#426)"
```

---

### Task 5: Add remaining test cases

**Files:**
- Modify: `internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`

- [ ] **Step 1: Update the `fullsend` mock to capture stdin**

The reason-appended test needs to verify the comment body piped to `fullsend`.
Replace the existing `fullsend` mock (near line 27) with a version that captures
stdin when `--result -` is used. Existing tests still pass because `run_test`
uses `grep -qF` (substring match), and the captured body is appended after the
args.

```bash
cat > "${MOCK_BIN}/fullsend" <<'MOCKEOF'
#!/usr/bin/env bash
BODY=""
PREV=""
for arg in "$@"; do
  if [[ "${arg}" == "-" ]] && [[ "${PREV}" == "--result" ]]; then
    BODY=$(cat)
  fi
  PREV="${arg}"
done
if [[ -n "${BODY}" ]]; then
  echo "fullsend $* <<BODY:${BODY}:BODY>>" >> "${GH_LOG}"
else
  echo "fullsend $*" >> "${GH_LOG}"
fi
MOCKEOF
chmod +x "${MOCK_BIN}/fullsend"
```

- [ ] **Step 2: Add `run_test_stdout` helper**

Add before the `# --- Test cases ---` line. This helper checks stdout/stderr
output instead of the GH_LOG, used for verifying warning annotations.

```bash
run_test_stdout() {
  local test_name="$1"
  local json_content="$2"
  local expected_stdout="$3"

  local run_dir="${TMPDIR}/run-${test_name}"
  mkdir -p "${run_dir}/iteration-1/output"
  echo "${json_content}" > "${run_dir}/iteration-1/output/agent-result.json"
  > "${GH_LOG}"

  local exit_code=0
  (cd "${run_dir}" && bash "${POST_SCRIPT}") > "${TMPDIR}/stdout.log" 2>&1 || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    echo "FAIL: ${test_name} — exit code ${exit_code}"
    cat "${TMPDIR}/stdout.log"
    FAILURES=$((FAILURES + 1))
    return
  fi

  if ! grep -qF "${expected_stdout}" "${TMPDIR}/stdout.log"; then
    echo "FAIL: ${test_name} — expected stdout pattern '${expected_stdout}' not found"
    echo "Actual stdout:"
    cat "${TMPDIR}/stdout.log"
    FAILURES=$((FAILURES + 1))
    return
  fi

  echo "PASS: ${test_name}"
}
```

- [ ] **Step 3: Add the four remaining test cases**

Add before the `# --- Summary ---` section:

```bash
run_test_stdout "label-actions-control-label-refused" \
  '{"action":"sufficient","reasoning":"all clear","clarity_scores":{"symptom":0.9,"cause":0.85,"reproduction":0.9,"impact":0.8,"overall":0.87},"triage_summary":{"title":"Fix crash","severity":"high","category":"bug","problem":"Crash","root_cause_hypothesis":"Buffer overflow","reproduction_steps":["step 1"],"environment":"Linux","impact":"All users","recommended_fix":"Fix buffer","proposed_test_case":"test_crash"},"comment":"## Triage Summary\n\nReady.","label_actions":{"reason":"Tried to set control label.","actions":[{"action":"add","label":"ready-to-code"}]}}' \
  "::warning::Refused to add control label 'ready-to-code' -- control labels are managed by the triage pipeline"

run_test "label-actions-absent-still-posts-comment" \
  '{"action":"sufficient","reasoning":"all clear","clarity_scores":{"symptom":0.9,"cause":0.85,"reproduction":0.9,"impact":0.8,"overall":0.87},"triage_summary":{"title":"Fix crash","severity":"high","category":"bug","problem":"Crash","root_cause_hypothesis":"Buffer overflow","reproduction_steps":["step 1"],"environment":"Linux","impact":"All users","recommended_fix":"Fix buffer","proposed_test_case":"test_crash"},"comment":"## Triage Summary\n\nReady."}' \
  "fullsend post-comment --repo test-org/test-repo --number 42"

run_test "label-actions-with-insufficient" \
  '{"action":"insufficient","reasoning":"missing repro","clarity_scores":{"symptom":0.6,"cause":0.3,"reproduction":0.1,"impact":0.5,"overall":0.39},"comment":"Could you share the exact steps to reproduce this?","label_actions":{"reason":"Component label applies regardless of triage outcome.","actions":[{"action":"add","label":"component/parser"}]}}' \
  "gh api repos/test-org/test-repo/issues/42/labels -f labels[]=component/parser --silent"

run_test "label-actions-reason-appended-to-comment" \
  '{"action":"sufficient","reasoning":"all clear","clarity_scores":{"symptom":0.9,"cause":0.85,"reproduction":0.9,"impact":0.8,"overall":0.87},"triage_summary":{"title":"Fix crash","severity":"high","category":"bug","problem":"Crash","root_cause_hypothesis":"Buffer overflow","reproduction_steps":["step 1"],"environment":"Linux","impact":"All users","recommended_fix":"Fix buffer","proposed_test_case":"test_crash"},"comment":"## Triage Summary\n\nReady.","label_actions":{"reason":"API crash matches area/api label.","actions":[{"action":"add","label":"area/api"}]}}' \
  "API crash matches area/api label."
```

- [ ] **Step 4: Run all tests**

Run: `bash internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`
Expected: `All tests passed`

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/fullsend-repo/scripts/post-triage-test.sh
git commit -m "test(triage): add label_actions test cases (#426)"
```

---

### Task 6: Update scaffold test

**Files:**
- Modify: `internal/scaffold/scaffold_test.go:83`

- [ ] **Step 1: Add the skill to the expected file list**

After line 83 (`"skills/code-implementation/SKILL.md",`), add:

```go
		"skills/issue-labels/SKILL.md",
```

- [ ] **Step 2: Run the scaffold tests**

Run: `go test ./internal/scaffold/ -run TestFullsendRepoFilesExist -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/scaffold_test.go
git commit -m "test(scaffold): expect issue-labels skill in file list (#426)"
```

---

### Task 7: Run full test suite and lint

- [ ] **Step 1: Run post-triage tests**

Run: `bash internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`
Expected: `All tests passed`

- [ ] **Step 2: Run Go tests**

Run: `make go-test`
Expected: PASS

- [ ] **Step 3: Run Go vet**

Run: `make go-vet`
Expected: no issues

- [ ] **Step 4: Run lint**

Run: `make lint`
Expected: no failures

- [ ] **Step 5: Verify JSON schema is valid**

Run: `python3 -c "import json; json.load(open('internal/scaffold/fullsend-repo/schemas/triage-result.schema.json'))"`
Expected: no output (success)
