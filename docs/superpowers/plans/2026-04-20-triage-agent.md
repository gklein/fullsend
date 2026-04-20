# Triage Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the triage agent (Story 3, #126) — a single-shot agent that reads a GitHub issue, decides whether information is sufficient, and either asks a clarifying question or declares the issue triaged, communicating its decision via structured JSON that a deterministic post-script interprets and acts on.

**Architecture:** The agent runs inside a sandbox via `claude --print --agent triage`. It reads the issue (title, body, all comments) using `gh issue view`, applies a hybrid questioning strategy (phased interview + ambiguity gating from the PR #170 experiment), and writes a single JSON file to `$FULLSEND_OUTPUT_DIR/triage-result.json`. Per [ADR 0022](../ADRs/0022-harness-level-output-schema-enforcement.md), the harness first validates the JSON against a declared schema (with retry on non-compliance), then a post-script (`scripts/post-triage.sh`) running on the host performs the deterministic GitHub mutations: posting a comment, applying labels, and optionally closing the issue. Before the agent runs, a label-reset step in the triage workflow strips all triage-related labels to a clean baseline (per Story 2 #125). The agent never directly mutates the issue — all side effects flow through the post-script.

**Tech Stack:** Bash (post-script, schema validation), JSON Schema, Go (forge interface, e2e tests), Claude agent markdown, GitHub Actions YAML

---

## Design Decisions

### Agent output contract (ADR 0022)

The agent writes a single JSON file. Per ADR 0022, the harness validates this file against a declared JSON Schema before the post-script runs. The pipeline is:

```
agent produces JSON → schema validation (retry on fail) → post-script (applies GitHub mutations)
```

This three-layer separation means:

1. The agent prompt can be tested offline against fixture issues (no GitHub access needed for prompt iteration).
2. Schema validation catches malformed output before it reaches any mutation logic — the post-script can trust the structure.
3. The post-script is deterministic shell code that can be unit-tested with fixture JSON.
4. Future actions (e.g. "not a bug", "feature request") are added by extending the JSON Schema, the prompt, and the post-script `case` branch — all versioned together.
5. The schema is generic infrastructure — `scripts/validate-output-schema.sh` works for any agent, not just triage. Each agent declares its schema in the harness config.

### Action enum

MVP actions: `insufficient`, `duplicate`, `sufficient`

Future-reserved (the JSON Schema uses `enum` so unknown values fail validation, and the post-script has an explicit `unknown action` error path as defense-in-depth):
- `not_a_bug` — working as intended
- `feature_request` — reclassify as feature request
- `stale` — issue is too old / abandoned

### Questioning strategy

The experiment in PR #170 found that the top two strategies (omo-prometheus phased interview and omc-deep-interview ambiguity gating) were statistically tied. The biggest problem across all strategies was **premature resolution** — agents resolve after 1 turn when they should ask follow-ups.

The production prompt uses a hybrid approach:
- **Phased interview** from omo-prometheus (scope → investigate → hypothesize → resolve)
- **Explicit resolution gate** inspired by omc-deep-interview: the agent must self-assess four clarity dimensions (symptom 35%, cause 30%, reproduction 20%, impact 15%) and may only resolve when overall clarity >= 80%
- **Anti-premature-resolution rule**: if the agent's own assessment lists information gaps, it MUST ask rather than resolve

### Label state machine

Story 2 (#125) specifies: *"The label reset logic (strip labels on stage start) runs as the first action of the entry point before launching the agent, not inside the agent itself — this keeps it deterministic and outside the LLM's control."*

The label lifecycle for triage:

```
Issue opened (no labels)
        │
        ▼
┌─── TRIAGE START ─────────────────────────────────────────────┐
│ Pre-script: strip needs-info, ready-to-code, duplicate,      │
│             not-ready, not-reproducible                       │
│ (clean baseline — every triage run starts from the same state)│
└───────────────────────────────┬───────────────────────────────┘
                                │
                       agent runs + decides
                                │
              ┌─────────────────┼──────────────────┐
              ▼                 ▼                  ▼
        insufficient        duplicate          sufficient
        +needs-info         +duplicate         +ready-to-code
        post question       post notice        post summary
                            CLOSE issue
              │
       human replies
              │
              ▼
        TRIAGE START (loop — labels stripped, agent re-runs)
```

The label reset is a **pre-script** (`scripts/pre-triage.sh`) that runs on the host via the harness `pre_script` mechanism (`run.go:149-160`). It runs after the app token is generated but before the sandbox is created, so it has the `GH_TOKEN` (with `issues: write` on the source repo) and is fully deterministic — outside the LLM's control.

Why a pre-script and not a workflow step: the `GITHUB_TOKEN` in `.fullsend`'s workflow is scoped to the `.fullsend` repo and cannot modify labels on the source repo. The app token (generated at `triage.yml:56`) is what has cross-repo `issues: write`, and it flows through `setup-agent-env.sh` → `GH_TOKEN` env var → harness `runner_env` → pre-script.

Edge cases handled:
- **Re-triage via `/triage`**: strips `ready-to-code` from a previously-triaged issue before re-running. Without this, both `ready-to-code` and `needs-info` could coexist.
- **Re-triage of a closed duplicate**: the label strip runs, but the issue is still closed. The agent re-reads a closed issue, which is fine — it may decide differently. If it decides `sufficient`, it applies `ready-to-code` to a closed issue, which is harmless (the issue stays closed; a human can reopen if needed).
- **Bot loop prevention**: the shim only re-dispatches for non-bot commenters, so the agent's own `needs-info` comment doesn't trigger a loop.

The post-script is purely additive — each action applies only its own label. It never removes labels from a previous triage cycle; that's the pre-script's job.

### Label naming: deviation from ADR 0002

ADR 0002 uses `not-ready` (lines 70, 107-109, 115-117, 125, 280, 413, 417) and `ready-to-implement` (lines 72, 115-116, 125, 129, 133). This plan intentionally deviates:

- **`needs-info`** instead of `not-ready`: more descriptive — it tells the reporter what's needed, not just that the issue isn't ready. `not-ready` is ambiguous (not ready for what? could mean anything from "needs triage" to "blocked on dependency").
- **`ready-to-code`** instead of `ready-to-implement`: aligns with the renamed code agent role (the `dispatch-code` job, `code.yml` workflow). We deprecated "implement" in favor of "code" across the platform.

ADR 0002 is a living design document, not a frozen spec. These label names should be updated in ADR 0002 as part of a separate documentation sweep (tracked in "Out of Scope" below).

### Post-script and the triage loop

The triage workflow fires on `issues.opened`, `issues.edited`, `issue_comment.created` (for `/triage`), and crucially on `issue_comment.created` **from non-bot authors** when the issue has a `needs-info` label. This means:

1. Pre-script strips all triage labels to a clean baseline
2. Agent runs, reads full issue, assesses, writes JSON
3. Schema validation checks JSON against declared schema (retry on fail per ADR 0022)
4. Post-script reads validated JSON and performs GitHub mutations:
   - `insufficient`: post question + apply `needs-info`
   - `duplicate`: post notice + apply `duplicate` + close issue
   - `sufficient`: post summary + apply `ready-to-code`
5. If `insufficient`: human replies → shim re-dispatches → back to step 1

The loop is stateless between invocations. Each agent invocation is single-shot (reads everything, decides, dies). The conversation history lives in the issue comments.

---

## File Structure

### Files to create

| File | Responsibility |
|------|---------------|
| `internal/scaffold/fullsend-repo/agents/triage.md` | Agent prompt (overwrite existing placeholder) |
| `internal/scaffold/fullsend-repo/schemas/triage-result.schema.json` | JSON Schema for triage agent output (ADR 0022) |
| `internal/scaffold/fullsend-repo/scripts/validate-output-schema.sh` | Generic schema validator — works for any agent |
| `internal/scaffold/fullsend-repo/scripts/pre-triage.sh` | Pre-script: strip triage labels to clean baseline |
| `internal/scaffold/fullsend-repo/scripts/post-triage.sh` | Post-script: parse validated JSON, post comment, apply labels |
| `internal/scaffold/fullsend-repo/scripts/post-triage-test.sh` | Test harness for post-triage.sh with fixture JSON |
| `internal/scaffold/fullsend-repo/scripts/validate-output-schema-test.sh` | Test harness for validate-output-schema.sh with conditional requirements |

### Files to modify

| File | Change |
|------|--------|
| `internal/scaffold/fullsend-repo/harness/triage.yaml` | Add `pre_script`, `post_script`, repurpose `validation_loop` for schema validation |
| `internal/scaffold/fullsend-repo/scripts/validate-triage.sh` | Delete (replaced by generic validate-output-schema.sh + post-triage.sh) |
| `internal/scaffold/fullsend-repo/templates/shim-workflow.yaml` | Add `issue_comment` trigger for triage re-entry on human reply |
| `internal/forge/forge.go` | Add `ListIssueComments` to Client interface and `IssueComment` type |
| `internal/forge/github/github.go` | Implement `ListIssueComments` for GitHub |
| `internal/forge/fake.go` | Implement `ListIssueComments` for FakeClient |
| `e2e/admin/admin_test.go` | Extend triage smoke test to wait for completion and verify comment |
| `internal/cli/run.go` | Expose `FULLSEND_DIR` env var so `runner_env` values can reference it |

### Files unchanged

| File | Why |
|------|-----|
| `internal/scaffold/fullsend-repo/.github/workflows/triage.yml` | Already correct — app token generated before `fullsend run` |
| `internal/scaffold/fullsend-repo/.github/actions/fullsend/action.yml` | Already invokes `fullsend run` generically |
| `internal/scaffold/fullsend-repo/policies/triage.yaml` | Network policy already correct (GitHub + Vertex AI) |
| `internal/scaffold/fullsend-repo/env/triage.env` | Already exports `GITHUB_ISSUE_URL` and `GH_TOKEN` |
| `internal/harness/harness.go` | `PreScript`, `PostScript`, and `ValidationLoop` fields already exist |

---

## Tasks

### Task 1: Agent prompt — triage.md

**Files:**
- Overwrite: `internal/scaffold/fullsend-repo/agents/triage.md`

- [ ] **Step 1: Write the agent prompt**

Replace the existing placeholder prompt with the production triage agent prompt. The prompt must:
- Instruct the agent to fetch the issue via `gh issue view`
- Apply the hybrid questioning strategy (phased interview + clarity gating)
- Require JSON output to `$FULLSEND_OUTPUT_DIR/triage-result.json`
- Define the exact JSON schema for each action type
- Include anti-premature-resolution rules

```markdown
---
name: triage
description: Inspect a GitHub issue, assess information sufficiency, and produce a structured triage decision.
skills: []
tools: Bash(gh,jq)
model: opus
---

You are a triage agent. Your job is to inspect a single GitHub issue — including all comments — and produce a structured triage decision.

## Inputs

- `GITHUB_ISSUE_URL` — the HTML URL of the issue (e.g., `https://github.com/org/repo/issues/42`).

## Step 1: Fetch the issue

```
gh issue view "$GITHUB_ISSUE_URL" --json number,title,body,labels,assignees,createdAt,updatedAt,author,comments,state,milestone
```

If the command fails, write a JSON error result and stop.

## Step 2: Check for duplicates

Search for potential duplicates among open issues:

```
gh issue list --repo OWNER/REPO --state open --json number,title,body --limit 100
```

Extract the owner/repo from `GITHUB_ISSUE_URL`. Compare issue titles and descriptions for semantic overlap. An issue is a duplicate if it describes the same root problem, even if the symptoms or wording differ.

## Step 3: Assess information sufficiency

Use this phased approach to evaluate the issue:

### Phase 1 — Scope identification
- What component or feature is affected?
- Is this a regression, new bug, or misunderstanding?
- Is there any version or timeline information?

### Phase 2 — Deep investigation
- Are exact error messages or logs provided?
- Are reproduction steps present and specific (not vague)?
- Is the environment described (OS, browser, version, configuration)?

### Phase 3 — Hypothesis formation
- Can you form a plausible root cause hypothesis from the available information?
- Could a developer start investigating without contacting the reporter?

### Clarity scoring

Rate each dimension 0.0–1.0:

| Dimension | Weight | What it measures |
|-----------|--------|-----------------|
| Symptom clarity | 35% | Do we know exactly what goes wrong? |
| Cause clarity | 30% | Do we have a plausible hypothesis for why? |
| Reproduction clarity | 20% | Could a developer reproduce this? |
| Impact clarity | 15% | How severe? Who is affected? Workaround? |

Calculate overall clarity: `symptom*0.35 + cause*0.30 + reproduction*0.20 + impact*0.15`

**Resolution threshold: overall clarity >= 0.80**

**Anti-premature-resolution rule:** If your assessment identifies information gaps that would change your severity rating, root cause hypothesis, or recommended fix approach, you MUST ask — even if overall clarity is above threshold. When in doubt, ask.

## Step 4: Decide and write result

Based on your assessment, choose exactly one action and write the result as JSON to `$FULLSEND_OUTPUT_DIR/triage-result.json`.

### Action: `insufficient`

Information is missing that would change the triage outcome. Ask ONE focused, specific clarifying question.

```json
{
  "action": "insufficient",
  "reasoning": "Brief internal note about what information is missing and why it matters",
  "clarity_scores": {
    "symptom": 0.0,
    "cause": 0.0,
    "reproduction": 0.0,
    "impact": 0.0,
    "overall": 0.0
  },
  "comment": "Your clarifying question, written as a professional GitHub comment. Address the reporter as a person. Ask ONE question — the most diagnostic question that would move clarity scores the most. Be specific about what you need."
}
```

### Action: `duplicate`

This issue describes the same problem as an existing open issue.

```json
{
  "action": "duplicate",
  "reasoning": "Brief explanation of why this is a duplicate",
  "duplicate_of": 123,
  "comment": "A professional comment explaining the duplicate finding and linking to the canonical issue. Be kind — the reporter may not have found the original."
}
```

### Action: `sufficient`

Information is sufficient for a developer to investigate and fix.

```json
{
  "action": "sufficient",
  "reasoning": "Brief note on why this is ready for implementation",
  "clarity_scores": {
    "symptom": 0.0,
    "cause": 0.0,
    "reproduction": 0.0,
    "impact": 0.0,
    "overall": 0.0
  },
  "triage_summary": {
    "title": "Refined issue title (clear, specific, actionable)",
    "severity": "critical | high | medium | low",
    "category": "bug | performance | security",
    "problem": "Clear description of the problem",
    "root_cause_hypothesis": "Most likely root cause",
    "reproduction_steps": ["step 1", "step 2"],
    "environment": "Relevant environment details",
    "impact": "Who is affected and how",
    "recommended_fix": "What a developer should investigate",
    "proposed_test_case": "Description of a test that would verify the fix, including the test framework, file path, and assertions — written to match the repo's existing test patterns",
    "information_gaps": ["Any remaining unknowns that did not block triage"]
  },
  "comment": "A triage summary comment formatted in markdown, presenting the assessment to the maintainers. Include the proposed test case as a fenced code block."
}
```

## Questioning guidelines

- Ask ONE question per invocation. The most diagnostic question — the one that would move the lowest clarity dimension the most.
- Never re-ask for information already provided in the issue body or prior comments.
- Push back on vague descriptions: if the reporter says "it crashes," ask what specifically happens (error dialog? freeze? silent exit?).
- Reference prior comments: "You mentioned X earlier — can you elaborate on [specific aspect]?"
- Be empathetic but efficient. Acknowledge the reporter's experience, then ask your question.
- Do NOT ask questions whose answers would not change your triage outcome.

## Output rules

- Write ONLY the JSON file. No markdown report, no other output files.
- The JSON must be valid and parseable. No markdown fences around it, no trailing text.
- Do NOT post comments, apply labels, or modify the issue in any way. Your only output is the JSON file. A post-script handles all GitHub mutations.
```

- [ ] **Step 2: Verify the prompt parses correctly as a Claude agent definition**

Run: `head -7 internal/scaffold/fullsend-repo/agents/triage.md`

Expected: YAML frontmatter with `name: triage`, `tools: Bash(gh,jq)`, `model: opus`

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/fullsend-repo/agents/triage.md
git commit -m "feat(triage): replace placeholder prompt with production triage agent

Hybrid strategy combining omo-prometheus phased interview with
omc-deep-interview clarity gating. Agent writes structured JSON
to output dir; never mutates the issue directly.

Refs #126"
```

---

### Task 2: Post-script — post-triage.sh

**Files:**
- Create: `internal/scaffold/fullsend-repo/scripts/post-triage.sh`

The post-script runs on the host after sandbox cleanup. It reads the agent's validated JSON output and performs deterministic GitHub mutations. The working directory is the run output directory (set by `run.go:180`). By the time this script runs, ADR 0022 schema validation has already passed, so the JSON structure is guaranteed.

- [ ] **Step 1: Write the post-script**

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
# The agent writes its decision to output/triage-result.json (relative to
# the iteration directory). This script finds the most recent iteration's output.

set -euo pipefail

# Find the triage result JSON. The run dir contains iteration-N/ subdirectories;
# we want the last one's output.
RESULT_FILE=""
for dir in iteration-*/output; do
  if [[ -f "${dir}/triage-result.json" ]]; then
    RESULT_FILE="${dir}/triage-result.json"
  fi
done

if [[ -z "${RESULT_FILE}" ]]; then
  echo "ERROR: triage-result.json not found in any iteration output directory"
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

# Extract repo and issue number from the HTML URL.
# GITHUB_ISSUE_URL is e.g. https://github.com/org/repo/issues/42
REPO=$(echo "${GITHUB_ISSUE_URL}" | sed 's|https://github.com/||; s|/issues/.*||')
ISSUE_NUMBER=$(basename "${GITHUB_ISSUE_URL}")

echo "Action: ${ACTION}"
echo "Repo: ${REPO}"
echo "Issue: #${ISSUE_NUMBER}"

case "${ACTION}" in
  insufficient)
    if [[ -z "${COMMENT}" ]]; then
      echo "ERROR: action is 'insufficient' but no comment provided"
      exit 1
    fi
    echo "Posting clarifying question..."
    gh issue comment "${ISSUE_NUMBER}" --repo "${REPO}" --body "${COMMENT}"

    echo "Applying label..."
    gh issue edit "${ISSUE_NUMBER}" --repo "${REPO}" --add-label "needs-info"
    ;;

  duplicate)
    if [[ -z "${COMMENT}" ]]; then
      echo "ERROR: action is 'duplicate' but no comment provided"
      exit 1
    fi
    echo "Posting duplicate notice..."
    gh issue comment "${ISSUE_NUMBER}" --repo "${REPO}" --body "${COMMENT}"

    echo "Applying label and closing..."
    gh issue edit "${ISSUE_NUMBER}" --repo "${REPO}" --add-label "duplicate"
    gh issue close "${ISSUE_NUMBER}" --repo "${REPO}" --reason "not planned"
    ;;

  sufficient)
    if [[ -z "${COMMENT}" ]]; then
      echo "ERROR: action is 'sufficient' but no comment provided"
      exit 1
    fi
    echo "Posting triage summary..."
    gh issue comment "${ISSUE_NUMBER}" --repo "${REPO}" --body "${COMMENT}"

    echo "Applying label..."
    gh issue edit "${ISSUE_NUMBER}" --repo "${REPO}" --add-label "ready-to-code"
    ;;

  *)
    echo "ERROR: unknown action '${ACTION}' — this may be a newer action that post-triage.sh does not handle yet"
    exit 1
    ;;
esac

echo "Post-triage complete."
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x internal/scaffold/fullsend-repo/scripts/post-triage.sh`

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/fullsend-repo/scripts/post-triage.sh
git commit -m "feat(triage): add post-triage.sh for deterministic GitHub mutations

The post-script parses the agent's schema-validated triage-result.json
and performs GitHub mutations: posting comments, applying labels,
and closing duplicates.

Refs #126"
```

---

### Task 3: JSON Schema for triage result (ADR 0022)

**Files:**
- Create: `internal/scaffold/fullsend-repo/schemas/triage-result.schema.json`

Define the formal JSON Schema that the harness validates agent output against before the post-script runs. This is the single source of truth for the triage result contract.

- [ ] **Step 1: Write the schema**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "triage-result.schema.json",
  "title": "Triage Agent Result",
  "description": "Structured output from the triage agent, validated by the harness before the post-script runs (ADR 0022).",
  "type": "object",
  "required": ["action", "reasoning", "comment"],
  "properties": {
    "action": {
      "type": "string",
      "enum": ["insufficient", "duplicate", "sufficient"]
    },
    "reasoning": {
      "type": "string",
      "minLength": 1
    },
    "comment": {
      "type": "string",
      "minLength": 1
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
    }
  },
  "allOf": [
    {
      "if": { "properties": { "action": { "const": "insufficient" } } },
      "then": { "required": ["clarity_scores"] }
    },
    {
      "if": { "properties": { "action": { "const": "duplicate" } } },
      "then": { "required": ["duplicate_of"] }
    },
    {
      "if": { "properties": { "action": { "const": "sufficient" } } },
      "then": { "required": ["clarity_scores", "triage_summary"] }
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
        "category": { "type": "string", "minLength": 1 },
        "problem": { "type": "string", "minLength": 1 },
        "root_cause_hypothesis": { "type": "string", "minLength": 1 },
        "reproduction_steps": { "type": "array", "items": { "type": "string" }, "minItems": 1 },
        "environment": { "type": "string" },
        "impact": { "type": "string", "minLength": 1 },
        "recommended_fix": { "type": "string", "minLength": 1 },
        "proposed_test_case": { "type": "string", "minLength": 1 },
        "information_gaps": { "type": "array", "items": { "type": "string" } }
      }
    }
  }
}
```

- [ ] **Step 2: Validate the schema itself is valid JSON**

Run: `python3 -m json.tool internal/scaffold/fullsend-repo/schemas/triage-result.schema.json > /dev/null`

Expected: No error.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/fullsend-repo/schemas/triage-result.schema.json
git commit -m "feat(triage): add JSON Schema for triage result (ADR 0022)

Formal schema validated by the harness before the post-script runs.
Uses conditional requirements: 'duplicate' requires duplicate_of,
'sufficient' requires triage_summary, etc.

Refs #126"
```

---

### Task 4: Generic schema validation script (ADR 0022)

**Files:**
- Create: `internal/scaffold/fullsend-repo/scripts/validate-output-schema.sh`
- Delete: `internal/scaffold/fullsend-repo/scripts/validate-triage.sh`

This script is agent-agnostic — it validates any agent's output against a declared schema. It is used as the `validation_loop.script` in the harness. The harness sets `FULLSEND_OUTPUT_SCHEMA` to the schema path.

- [ ] **Step 1: Write the validation script**

```bash
#!/usr/bin/env bash
# validate-output-schema.sh — Validate agent output against a JSON Schema.
#
# Generic script used by the harness validation_loop (ADR 0022).
# Works for any agent — the schema path is configured in the harness.
#
# Required env vars:
#   FULLSEND_OUTPUT_SCHEMA — path to the JSON Schema file
#
# The script looks for triage-result.json (or any .json file) in the
# iteration output directory. The working directory is the iteration dir
# (set by run.go).

set -euo pipefail

: "${FULLSEND_OUTPUT_SCHEMA:?FULLSEND_OUTPUT_SCHEMA must be set}"

# Find the output JSON file in this iteration's output directory.
OUTPUT_DIR="output"
if [[ ! -d "${OUTPUT_DIR}" ]]; then
  echo "FAIL: output directory not found"
  exit 1
fi

# Find all JSON files in the output directory.
JSON_FILES=()
while IFS= read -r -d '' f; do
  JSON_FILES+=("$f")
done < <(find "${OUTPUT_DIR}" -maxdepth 1 -name '*.json' -print0)

if [[ ${#JSON_FILES[@]} -eq 0 ]]; then
  echo "FAIL: no JSON files found in ${OUTPUT_DIR}/"
  exit 1
fi

RESULT_FILE="${JSON_FILES[0]}"
echo "Validating: ${RESULT_FILE} against ${FULLSEND_OUTPUT_SCHEMA}"

# Validate JSON is parseable.
if ! python3 -m json.tool "${RESULT_FILE}" > /dev/null 2>&1; then
  echo "FAIL: ${RESULT_FILE} is not valid JSON"
  exit 1
fi

# Validate against schema using Python's jsonschema.
# jsonschema is required — fail hard if not installed.
if ! python3 -c "import jsonschema" 2>/dev/null; then
  echo "FAIL: python3 jsonschema package is not installed (required by ADR 0022)"
  exit 1
fi

if ! python3 -c "
import json, sys
from jsonschema import validate, ValidationError

with open(sys.argv[1]) as f:
    instance = json.load(f)
with open(sys.argv[2]) as f:
    schema = json.load(f)
try:
    validate(instance=instance, schema=schema)
    print('PASS: output validated against schema')
except ValidationError as e:
    print(f'FAIL: schema validation error: {e.message}')
    if e.path:
        print(f'  at: {\".\".join(str(p) for p in e.path)}')
    sys.exit(1)
" "${RESULT_FILE}" "${FULLSEND_OUTPUT_SCHEMA}"; then
  exit 1
fi
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x internal/scaffold/fullsend-repo/scripts/validate-output-schema.sh`

- [ ] **Step 3: Delete the old validate-triage.sh**

Run: `git rm internal/scaffold/fullsend-repo/scripts/validate-triage.sh`

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/fullsend-repo/scripts/validate-output-schema.sh
git commit -m "feat(harness): add generic output schema validator (ADR 0022)

Agent-agnostic script that validates any agent's JSON output against
a declared schema. Used as the validation_loop script in the harness.
Replaces the triage-specific validate-triage.sh.

Refs #126"
```

---

### Task 4b: Schema validation tests

**Files:**
- Create: `internal/scaffold/fullsend-repo/scripts/validate-output-schema-test.sh`

Test the generic schema validator with fixture JSON and schemas, including conditional schema requirements (e.g., `duplicate` action requires `duplicate_of`).

- [ ] **Step 1: Write the test script**

```bash
#!/usr/bin/env bash
# validate-output-schema-test.sh — Test validate-output-schema.sh with fixtures.
#
# Run from the repo root:
#   bash internal/scaffold/fullsend-repo/scripts/validate-output-schema-test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VALIDATOR="${SCRIPT_DIR}/validate-output-schema.sh"
SCHEMA="${SCRIPT_DIR}/../schemas/triage-result.schema.json"
FAILURES=0

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

run_test() {
  local test_name="$1"
  local json_content="$2"
  local expect_pass="$3"  # "true" or "false"

  local test_dir="${TMPDIR}/${test_name}"
  mkdir -p "${test_dir}/output"
  echo "${json_content}" > "${test_dir}/output/triage-result.json"

  local exit_code=0
  FULLSEND_OUTPUT_SCHEMA="${SCHEMA}" \
    bash -c "cd '${test_dir}' && bash '${VALIDATOR}'" > "${TMPDIR}/stdout.log" 2>&1 || exit_code=$?

  if [[ "${expect_pass}" == "true" && ${exit_code} -ne 0 ]]; then
    echo "FAIL: ${test_name} — expected PASS but got exit ${exit_code}"
    cat "${TMPDIR}/stdout.log"
    FAILURES=$((FAILURES + 1))
  elif [[ "${expect_pass}" == "false" && ${exit_code} -eq 0 ]]; then
    echo "FAIL: ${test_name} — expected FAIL but got PASS"
    FAILURES=$((FAILURES + 1))
  else
    echo "PASS: ${test_name}"
  fi
}

# --- Valid inputs ---

run_test "valid-insufficient" \
  '{"action":"insufficient","reasoning":"missing repro","clarity_scores":{"symptom":0.6,"cause":0.3,"reproduction":0.1,"impact":0.5,"overall":0.39},"comment":"Can you share repro steps?"}' \
  "true"

run_test "valid-sufficient" \
  '{"action":"sufficient","reasoning":"clear","clarity_scores":{"symptom":0.9,"cause":0.8,"reproduction":0.9,"impact":0.7,"overall":0.85},"triage_summary":{"title":"Bug","severity":"high","category":"bug","problem":"crash","root_cause_hypothesis":"null ptr","reproduction_steps":["step 1"],"impact":"all users","recommended_fix":"fix ptr","proposed_test_case":"test_fix"},"comment":"Triage complete."}' \
  "true"

run_test "valid-duplicate" \
  '{"action":"duplicate","reasoning":"same as #10","duplicate_of":10,"comment":"Duplicate of #10."}' \
  "true"

# --- Conditional requirement failures ---

run_test "insufficient-missing-clarity-scores" \
  '{"action":"insufficient","reasoning":"missing info","comment":"Need more info."}' \
  "false"

run_test "duplicate-missing-duplicate-of" \
  '{"action":"duplicate","reasoning":"dupe","comment":"Duplicate."}' \
  "false"

run_test "sufficient-missing-triage-summary" \
  '{"action":"sufficient","reasoning":"ok","clarity_scores":{"symptom":0.9,"cause":0.8,"reproduction":0.9,"impact":0.7,"overall":0.85},"comment":"Done."}' \
  "false"

# --- Structural failures ---

run_test "missing-action" \
  '{"reasoning":"test","comment":"test"}' \
  "false"

run_test "missing-comment" \
  '{"action":"sufficient","reasoning":"test"}' \
  "false"

run_test "invalid-action-value" \
  '{"action":"not_a_bug","reasoning":"test","comment":"test"}' \
  "false"

run_test "invalid-json" \
  'not json at all' \
  "false"

# --- Summary ---

echo ""
if [[ ${FAILURES} -gt 0 ]]; then
  echo "${FAILURES} test(s) failed"
  exit 1
fi
echo "All tests passed"
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x internal/scaffold/fullsend-repo/scripts/validate-output-schema-test.sh`

- [ ] **Step 3: Run the tests**

Run: `bash internal/scaffold/fullsend-repo/scripts/validate-output-schema-test.sh`

Expected: `All tests passed`

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/fullsend-repo/scripts/validate-output-schema-test.sh
git commit -m "test(harness): add schema validation tests with conditional requirements

Tests valid inputs for all three actions plus conditional requirement
failures (insufficient without clarity_scores, duplicate without
duplicate_of, sufficient without triage_summary) and structural
failures (missing action, invalid JSON).

Refs #126"
```

---

### Task 4c: Expose FULLSEND_DIR in run.go

**Files:**
- Modify: `internal/cli/run.go`

The harness `runner_env` uses `${FULLSEND_DIR}` to reference files relative to the fullsend directory (e.g., `${FULLSEND_DIR}/schemas/triage-result.schema.json`). The `os.ExpandEnv` call at `run.go:75` already expands env vars in `runner_env` values, but `FULLSEND_DIR` isn't set anywhere. We need to export it before the expansion happens.

The `absFullsendDir` variable is already computed at `run.go:61`. We just need to set it as an env var.

- [ ] **Step 1: Set FULLSEND_DIR before RunnerEnv expansion**

Add after `run.go:68` (after `ValidateRunnerEnv`) and before line 74 (the `ExpandEnv` loop):

```go
	// Expose the fullsend directory so runner_env values can reference it
	// (e.g., ${FULLSEND_DIR}/schemas/triage-result.schema.json).
	os.Setenv("FULLSEND_DIR", absFullsendDir)
```

The resulting code block should be:

```go
	if err := h.ValidateRunnerEnv(); err != nil {
		printer.StepFail("Environment validation failed")
		return fmt.Errorf("validating env: %w", err)
	}
	// Expose the fullsend directory so runner_env values can reference it
	// (e.g., ${FULLSEND_DIR}/schemas/triage-result.schema.json).
	os.Setenv("FULLSEND_DIR", absFullsendDir)
	for k, v := range h.RunnerEnv {
		h.RunnerEnv[k] = os.ExpandEnv(v)
	}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/cli/ -v -count=1`

Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/run.go
git commit -m "feat(harness): expose FULLSEND_DIR env var for runner_env expansion

runner_env values can now reference \${FULLSEND_DIR} to build
absolute paths to files in the fullsend directory (e.g., schemas).
Set before os.ExpandEnv so it's available during expansion.

Refs #126"
```

---

### Task 5: Update harness to use pre-script + schema validation + post-script

**Files:**
- Modify: `internal/scaffold/fullsend-repo/harness/triage.yaml`

The harness now uses all three mechanisms:
- `pre_script` — label reset before the agent runs (Story 2)
- `validation_loop` — schema validation per ADR 0022, retries agent on non-compliance
- `post_script` — deterministic GitHub mutations after validation passes

- [ ] **Step 1: Update triage.yaml**

```yaml
agent: agents/triage.md
model: opus
image: quay.io/manonru/fullsend-exp:latest
policy: policies/triage.yaml

host_files:
  - src: env/gcp-vertex.env
    dest: /tmp/workspace/.env.d/gcp-vertex.env
    expand: true
  - src: ${GOOGLE_APPLICATION_CREDENTIALS}
    dest: /tmp/workspace/.gcp-credentials.json
  - src: env/triage.env
    dest: /tmp/workspace/.env.d/triage.env
    expand: true

skills: []

pre_script: scripts/pre-triage.sh

validation_loop:
  script: scripts/validate-output-schema.sh
  max_iterations: 2

post_script: scripts/post-triage.sh

runner_env:
  GITHUB_ISSUE_URL: ${GITHUB_ISSUE_URL}
  GH_TOKEN: ${GH_TOKEN}
  FULLSEND_OUTPUT_SCHEMA: ${FULLSEND_DIR}/schemas/triage-result.schema.json

timeout_minutes: 10
```

- [ ] **Step 2: Verify harness loads**

Run: `cd /home/rbean/code/fullsend && go test ./internal/harness/ -run TestLoad -v`

Expected: Existing harness tests still pass (we're not changing the harness Go code, just a YAML file).

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/fullsend-repo/harness/triage.yaml
git commit -m "feat(triage): wire pre-script, schema validation, and post-script

pre_script resets triage labels (Story 2). validation_loop validates
output against JSON Schema (ADR 0022) with 1 retry. post_script
applies GitHub mutations. runner_env passes GH_TOKEN and schema path.

Refs #125, #126"
```

---

### Task 6: Pre-script for label reset

**Files:**
- Create: `internal/scaffold/fullsend-repo/scripts/pre-triage.sh`

Per Story 2 (#125): *"The label reset logic runs as the first action of the entry point before launching the agent, not inside the agent itself."* The pre-script runs on the host via the harness `pre_script` mechanism (`run.go:149-160`), after the app token is generated but before the sandbox is created. It has `GH_TOKEN` (the app token with `issues: write` on the source repo) via `runner_env`.

Why not a workflow step in `triage.yml`: the workflow's `GITHUB_TOKEN` is scoped to the `.fullsend` repo and cannot modify labels on the source repo. The app token is what has cross-repo permissions, and it's only available after the "Generate app token" step — which means it flows through `setup-agent-env.sh` → `GH_TOKEN` → harness `runner_env` → pre-script.

- [ ] **Step 1: Write the pre-script**

```bash
#!/usr/bin/env bash
# pre-triage.sh — Strip triage-related labels before the agent runs.
#
# Runs on the host via the harness pre_script mechanism. Ensures every
# triage invocation starts from a clean label baseline, preventing
# mutual-exclusion violations (Story 2, #125).
#
# Required env vars:
#   GITHUB_ISSUE_URL — HTML URL of the issue
#   GH_TOKEN         — GitHub token with issues read/write scope

set -euo pipefail

REPO=$(echo "${GITHUB_ISSUE_URL}" | sed 's|https://github.com/||; s|/issues/.*||')
ISSUE_NUMBER=$(basename "${GITHUB_ISSUE_URL}")

echo "Resetting triage labels on ${REPO}#${ISSUE_NUMBER}"

for label in needs-info ready-to-code duplicate not-ready not-reproducible; do
  gh issue edit "${ISSUE_NUMBER}" --repo "${REPO}" --remove-label "${label}" 2>/dev/null || true
done

echo "Label reset complete."
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x internal/scaffold/fullsend-repo/scripts/pre-triage.sh`

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/fullsend-repo/scripts/pre-triage.sh
git commit -m "feat(triage): add pre-script for label reset (Story 2)

Strip all triage-related labels before the agent runs, ensuring
every invocation starts from a clean baseline. Runs on the host
via the harness pre_script mechanism with the app token (GH_TOKEN)
which has issues:write on the source repo.

Refs #125, #126"
```

---

### Task 7: Update shim workflow for triage re-entry

**Files:**
- Modify: `internal/scaffold/fullsend-repo/templates/shim-workflow.yaml`

The triage loop requires re-dispatching when a human replies to a `needs-info` question. The shim workflow already handles `issue_comment.created` for `/triage` slash commands. We need to add a condition: also dispatch triage when a non-bot user comments on an issue that has the `needs-info` label.

- [ ] **Step 1: Update the dispatch-triage job condition**

The current condition (from `shim-workflow.yaml:27-35`):

```yaml
    if: >-
      (github.event_name == 'issues' && (
        github.event.action == 'opened' ||
        github.event.action == 'edited'
      )) ||
      (github.event_name == 'issue_comment' && (
        github.event.comment.body == '/triage' ||
        startsWith(github.event.comment.body, '/triage ')
      ))
```

Replace with:

```yaml
    if: >-
      (github.event_name == 'issues' && (
        github.event.action == 'opened' ||
        github.event.action == 'edited'
      )) ||
      (github.event_name == 'issue_comment' && (
        github.event.comment.body == '/triage' ||
        startsWith(github.event.comment.body, '/triage ') ||
        (
          github.event.comment.author_association != 'NONE' &&
          !endsWith(github.event.comment.user.login, '[bot]') &&
          contains(toJSON(github.event.issue.labels.*.name), 'needs-info')
        )
      ))
```

This adds a third `issue_comment` condition: dispatch triage when:
- The commenter is associated with the repo (not a random passerby)
- The commenter is not a bot (prevents the triage agent's own comments from triggering a loop)
- The issue currently has the `needs-info` label (meaning triage previously asked a question)

- [ ] **Step 2: Verify the YAML is valid**

Run: `python3 -c "import yaml; yaml.safe_load(open('internal/scaffold/fullsend-repo/templates/shim-workflow.yaml'))"`

Expected: No error.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/fullsend-repo/templates/shim-workflow.yaml
git commit -m "feat(triage): re-dispatch triage when human replies to needs-info

When a non-bot repo member comments on an issue labelled needs-info,
the shim workflow re-dispatches the triage agent. This creates the
asynchronous triage loop: agent asks question → human replies →
agent re-reads and re-assesses.

Refs #126"
```

---

### Task 8: Post-script tests

**Files:**
- Create: `internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`

Test the post-script with fixture JSON inputs. Since the post-script calls `gh issue comment`, `gh issue edit`, and `gh issue close`, the tests mock these commands with a shell function that records calls.

- [ ] **Step 1: Write the test script**

```bash
#!/usr/bin/env bash
# post-triage-test.sh — Test post-triage.sh with fixture JSON inputs.
#
# Uses a mock gh command to capture calls without hitting GitHub.
# Run from the repo root: bash internal/scaffold/fullsend-repo/scripts/post-triage-test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POST_SCRIPT="${SCRIPT_DIR}/post-triage.sh"
FAILURES=0

# Create a temp directory for test fixtures and mock state.
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

# Mock gh: record all calls to a log file.
GH_LOG="${TMPDIR}/gh-calls.log"
MOCK_BIN="${TMPDIR}/bin"
mkdir -p "${MOCK_BIN}"
cat > "${MOCK_BIN}/gh" <<MOCKEOF
#!/usr/bin/env bash
echo "gh \$*" >> "${GH_LOG}"
MOCKEOF
chmod +x "${MOCK_BIN}/gh"

export PATH="${MOCK_BIN}:${PATH}"
export GITHUB_ISSUE_URL="https://github.com/test-org/test-repo/issues/42"
export GH_TOKEN="fake-token"

run_test() {
  local test_name="$1"
  local json_content="$2"
  local expected_pattern="$3"
  local expect_failure="${4:-false}"

  # Create iteration output structure.
  local run_dir="${TMPDIR}/run-${test_name}"
  mkdir -p "${run_dir}/iteration-1/output"
  echo "${json_content}" > "${run_dir}/iteration-1/output/triage-result.json"

  # Clear gh call log.
  > "${GH_LOG}"

  # Run the post-script.
  local exit_code=0
  (cd "${run_dir}" && bash "${POST_SCRIPT}") > "${TMPDIR}/stdout.log" 2>&1 || exit_code=$?

  if [[ "${expect_failure}" == "true" ]]; then
    if [[ ${exit_code} -eq 0 ]]; then
      echo "FAIL: ${test_name} — expected failure but got success"
      FAILURES=$((FAILURES + 1))
      return
    fi
    echo "PASS: ${test_name} (expected failure, got exit code ${exit_code})"
    return
  fi

  if [[ ${exit_code} -ne 0 ]]; then
    echo "FAIL: ${test_name} — exit code ${exit_code}"
    cat "${TMPDIR}/stdout.log"
    FAILURES=$((FAILURES + 1))
    return
  fi

  if ! grep -qF "${expected_pattern}" "${GH_LOG}"; then
    echo "FAIL: ${test_name} — expected gh call pattern '${expected_pattern}' not found"
    echo "Actual calls:"
    cat "${GH_LOG}"
    FAILURES=$((FAILURES + 1))
    return
  fi

  echo "PASS: ${test_name}"
}

# --- Test cases ---

run_test "insufficient-posts-comment-and-labels" \
  '{"action":"insufficient","reasoning":"missing repro","clarity_scores":{"symptom":0.6,"cause":0.3,"reproduction":0.1,"impact":0.5,"overall":0.39},"comment":"Could you share the exact steps to reproduce this?"}' \
  "gh issue comment 42 --repo test-org/test-repo"

run_test "sufficient-posts-summary-and-labels" \
  '{"action":"sufficient","reasoning":"all clear","clarity_scores":{"symptom":0.9,"cause":0.85,"reproduction":0.9,"impact":0.8,"overall":0.87},"triage_summary":{"title":"Fix crash on save","severity":"high","category":"bug","problem":"Crash","root_cause_hypothesis":"Buffer overflow","reproduction_steps":["step 1"],"environment":"Linux","impact":"All users","recommended_fix":"Fix buffer","proposed_test_case":"test_save_crash","information_gaps":[]},"comment":"## Triage Summary\n\nThis is ready."}' \
  "gh issue edit 42 --repo test-org/test-repo --add-label ready-to-code"

run_test "duplicate-closes-issue" \
  '{"action":"duplicate","reasoning":"same as #10","duplicate_of":10,"comment":"This appears to be a duplicate of #10."}' \
  "gh issue close 42 --repo test-org/test-repo"

run_test "unknown-action-fails" \
  '{"action":"not_a_bug","reasoning":"working as intended","comment":"This is working as intended."}' \
  "" \
  "true"

run_test "missing-json-fails" \
  "" \
  "" \
  "true"

run_test "invalid-json-fails" \
  "this is not json" \
  "" \
  "true"

# --- Summary ---

echo ""
if [[ ${FAILURES} -gt 0 ]]; then
  echo "${FAILURES} test(s) failed"
  exit 1
fi
echo "All tests passed"
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`

- [ ] **Step 3: Run the tests**

Run: `bash internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`

Expected: `All tests passed`

- [ ] **Step 4: Fix any failures, then re-run**

Iterate until all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/fullsend-repo/scripts/post-triage-test.sh
git commit -m "test(triage): add post-triage.sh test harness with fixture JSON

Tests all three MVP actions (insufficient, sufficient, duplicate)
plus error cases (unknown action, missing file, invalid JSON)
using a mock gh command.

Refs #126"
```

---

### Task 9: Scaffold embedding test

**Files:**
- Modify: `internal/scaffold/scaffold_test.go`

Verify the new files are correctly embedded and accessible via `FullsendRepoFile` and `WalkFullsendRepo`.

- [ ] **Step 1: Read the existing test file**

Read `internal/scaffold/scaffold_test.go` to understand the existing test patterns.

- [ ] **Step 2: Add test cases for the new triage files**

Add test cases that verify:
- `FullsendRepoFile("agents/triage.md")` returns content containing `"action": "insufficient"` (the JSON schema example)
- `FullsendRepoFile("scripts/pre-triage.sh")` returns content containing `--remove-label`
- `FullsendRepoFile("scripts/post-triage.sh")` returns content containing `case "${ACTION}" in`
- `FullsendRepoFile("schemas/triage-result.schema.json")` returns content containing `"$schema"`
- `FullsendRepoFile("scripts/validate-output-schema.sh")` returns content containing `FULLSEND_OUTPUT_SCHEMA`
- `WalkFullsendRepo` includes all new files
- `FullsendRepoFile("scripts/validate-triage.sh")` returns an error (file was deleted)

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/scaffold/ -v`

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/scaffold_test.go
git commit -m "test(scaffold): verify triage agent files are embedded correctly

Refs #126"
```

---

### Task 10: Add `ListIssueComments` to forge interface

**Files:**
- Modify: `internal/forge/forge.go`
- Modify: `internal/forge/github/github.go`
- Modify: `internal/forge/fake.go`

The e2e test needs to read issue comments to verify the triage agent posted its output. The forge `Client` interface currently has `CreateIssue` and `CloseIssue` but no way to read comments. Add `ListIssueComments`.

- [ ] **Step 1: Add the `IssueComment` type and interface method to `forge.go`**

Add after the `Issue` struct (around line 56):

```go
// IssueComment represents a comment on an issue.
type IssueComment struct {
	ID        int
	Body      string
	Author    string
	CreatedAt string
}
```

Add to the `Client` interface in the "Issue operations" section (after `CloseIssue`):

```go
	ListIssueComments(ctx context.Context, owner, repo string, number int) ([]IssueComment, error)
```

- [ ] **Step 2: Implement for GitHub in `github.go`**

Add after the existing `CloseIssue` implementation:

```go
// ListIssueComments returns all comments on an issue.
func (c *LiveClient) ListIssueComments(ctx context.Context, owner, repo string, number int) ([]forge.IssueComment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments?per_page=100", owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing issue comments: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseErrorResponse(resp)
	}

	var raw []struct {
		ID        int    `json:"id"`
		Body      string `json:"body"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	comments := make([]forge.IssueComment, len(raw))
	for i, r := range raw {
		comments[i] = forge.IssueComment{
			ID:        r.ID,
			Body:      r.Body,
			Author:    r.User.Login,
			CreatedAt: r.CreatedAt,
		}
	}
	return comments, nil
}
```

- [ ] **Step 3: Implement for FakeClient in `fake.go`**

Add a `Comments` field to `FakeClient` or implement a stub that returns nil:

```go
func (f *FakeClient) ListIssueComments(_ context.Context, _, _ string, _ int) ([]forge.IssueComment, error) {
	if e := f.err("ListIssueComments"); e != nil {
		return nil, e
	}
	return nil, nil
}
```

- [ ] **Step 4: Run tests to verify compilation**

Run: `go test ./internal/forge/... -v -count=1`

Expected: All tests pass, no compilation errors.

- [ ] **Step 5: Commit**

```bash
git add internal/forge/forge.go internal/forge/github/github.go internal/forge/fake.go
git commit -m "feat(forge): add ListIssueComments to Client interface

Needed by the e2e test to verify the triage agent posted its
assessment as an issue comment.

Refs #126"
```

---

### Task 11: E2E test — verify triage agent produces expected GitHub output

**Files:**
- Modify: `e2e/admin/admin_test.go`

The existing `runTriageDispatchSmokeTest` (Phase 2.5 in `TestAdminInstallUninstall`) creates a test issue, waits for the triage workflow to be dispatched, and asserts the workflow run exists. Extend it to:

1. Wait for the triage workflow run to **complete** (not just be dispatched)
2. Verify the triage agent posted a **comment** on the test issue
3. Verify the comment contains expected triage output (either a clarifying question or a triage summary)
4. Verify the expected **label** was applied (`needs-info` or `ready-to-code`)

The test issue body should be a well-formed bug report so the agent is likely to produce a `sufficient` result with `ready-to-code`, but the test should accept either `insufficient` (posts a question, applies `needs-info`) or `sufficient` (posts a summary, applies `ready-to-code`) — both are valid triage outcomes.

- [ ] **Step 1: Read the existing smoke test**

Read `e2e/admin/admin_test.go:486-559` to understand `runTriageDispatchSmokeTest`.

- [ ] **Step 2: Extend the smoke test**

Replace the current `runTriageDispatchSmokeTest` with an extended version. The key changes:

1. Make the issue body a realistic bug report (so the agent has something to triage).
2. After finding the dispatched workflow run, wait for it to complete (poll `GetWorkflowRun` until status is `completed`, up to 10 minutes since the agent has a 10-minute timeout plus sandbox setup overhead).
3. After the workflow completes, use `ListIssueComments` to read the issue comments and assert at least one comment was posted.
4. Check the issue labels for either `needs-info` or `ready-to-code`.
5. If the workflow failed, fetch the run logs with `GetWorkflowRunLogs` and log them for debugging.

```go
func runTriageDispatchSmokeTest(t *testing.T, env *e2eEnv) {
	t.Helper()
	ctx := context.Background()

	// Find and merge the enrollment PR so the shim workflow becomes active.
	prs, err := env.client.ListRepoPullRequests(ctx, testOrg, testRepo)
	require.NoError(t, err, "listing PRs for %s", testRepo)

	var enrollmentPR *forge.ChangeProposal
	for _, pr := range prs {
		if strings.Contains(pr.Title, "fullsend") {
			cp := pr
			enrollmentPR = &cp
			break
		}
	}
	require.NotNil(t, enrollmentPR, "enrollment PR should exist for %s", testRepo)

	t.Logf("Merging enrollment PR #%d: %s", enrollmentPR.Number, enrollmentPR.URL)
	err = env.client.MergeChangeProposal(ctx, testOrg, testRepo, enrollmentPR.Number)
	require.NoError(t, err, "merging enrollment PR")

	// Wait for GitHub to process the merge.
	time.Sleep(5 * time.Second)

	// File a test issue with a realistic bug report body so the triage agent
	// has enough context to produce a meaningful triage result.
	issueTitle := fmt.Sprintf("e2e-triage-test-%s", env.runID)
	issueBody := `## Bug Report

**What happened:**
The application crashes with a segmentation fault when saving a file larger than 64KB
that contains UTF-8 multibyte characters (e.g., emoji or CJK characters).

**Expected behavior:**
The file should save successfully regardless of size or character encoding.

**Steps to reproduce:**
1. Open the application (v2.3.1)
2. Create a new document
3. Paste approximately 70KB of text containing emoji characters
4. Click File > Save
5. Application crashes immediately

**Environment:**
- OS: Ubuntu 22.04 LTS
- Application version: 2.3.1 (installed via apt)
- RAM: 16GB

**Error output:**
` + "```" + `
Segmentation fault (core dumped)
` + "```" + `

**Additional context:**
This started happening after the v2.3.0 -> v2.3.1 upgrade. Files under 64KB save fine.
Files over 64KB save fine if they contain only ASCII characters.`

	issue, err := env.client.CreateIssue(ctx, testOrg, testRepo, issueTitle, issueBody)
	require.NoError(t, err, "creating test issue")
	t.Logf("Created test issue #%d: %s", issue.Number, issue.URL)
	t.Cleanup(func() {
		t.Log("Closing test issue...")
		if closeErr := env.client.CloseIssue(ctx, testOrg, testRepo, issue.Number); closeErr != nil {
			t.Logf("warning: could not close test issue: %v", closeErr)
		}
	})

	// Wait for the triage workflow to be dispatched in .fullsend.
	issueCreatedAt := time.Now()
	t.Log("Waiting for triage workflow to be dispatched...")
	var triageRun *forge.WorkflowRun
	for attempt := 0; attempt < 12; attempt++ {
		time.Sleep(5 * time.Second)
		runs, listErr := env.client.ListWorkflowRuns(ctx, testOrg, forge.ConfigRepoName, "triage.yml")
		if listErr != nil {
			t.Logf("Attempt %d: error listing workflow runs: %v", attempt+1, listErr)
			continue
		}
		for _, run := range runs {
			runTime, parseErr := time.Parse(time.RFC3339, run.CreatedAt)
			if parseErr != nil {
				continue
			}
			if runTime.Before(issueCreatedAt) {
				continue
			}
			t.Logf("Attempt %d: found run %d (status: %s, conclusion: %s)", attempt+1, run.ID, run.Status, run.Conclusion)
			r := run
			triageRun = &r
			break
		}
		if triageRun != nil {
			break
		}
		t.Logf("Attempt %d: no triage workflow runs found yet", attempt+1)
	}
	require.NotNil(t, triageRun, "triage workflow should have been dispatched")

	// Wait for the workflow run to complete (up to 12 minutes: 10-minute agent
	// timeout + sandbox setup overhead).
	t.Logf("Waiting for triage workflow run %d to complete...", triageRun.ID)
	var finalRun *forge.WorkflowRun
	deadline := time.Now().Add(12 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(15 * time.Second)
		run, getErr := env.client.GetWorkflowRun(ctx, testOrg, forge.ConfigRepoName, triageRun.ID)
		if getErr != nil {
			t.Logf("Error polling workflow run: %v", getErr)
			continue
		}
		t.Logf("Run %d: status=%s conclusion=%s", run.ID, run.Status, run.Conclusion)
		if run.Status == "completed" {
			finalRun = run
			break
		}
	}
	require.NotNil(t, finalRun, "triage workflow run should have completed within deadline")

	// If the run failed, fetch logs for debugging.
	if finalRun.Conclusion != "success" {
		logs, logErr := env.client.GetWorkflowRunLogs(ctx, testOrg, forge.ConfigRepoName, finalRun.ID)
		if logErr != nil {
			t.Logf("Could not fetch run logs: %v", logErr)
		} else {
			t.Logf("Workflow run logs (last 2000 chars):\n%s", truncateEnd(logs, 2000))
		}
		t.Fatalf("Triage workflow run %d concluded with %q, expected success", finalRun.ID, finalRun.Conclusion)
	}

	// Verify the triage agent posted a comment on the issue.
	t.Log("Verifying triage agent posted a comment...")
	comments, err := env.client.ListIssueComments(ctx, testOrg, testRepo, issue.Number)
	require.NoError(t, err, "listing issue comments")
	assert.NotEmpty(t, comments, "triage agent should have posted at least one comment on the issue")

	if len(comments) > 0 {
		lastComment := comments[len(comments)-1]
		t.Logf("Triage comment by %s (first 200 chars): %.200s", lastComment.Author, lastComment.Body)

		// The comment should be from the bot (ends with [bot]).
		assert.True(t, strings.HasSuffix(lastComment.Author, "[bot]"),
			"triage comment should be from a bot, got author %q", lastComment.Author)
	}

	// Verify labels: either needs-info (insufficient) or ready-to-code (sufficient).
	// Re-fetch the issue to check labels. The forge Client doesn't have GetIssue with
	// labels, so use the gh API directly via the token.
	t.Log("Verifying triage labels...")
	labelURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/labels", testOrg, testRepo, issue.Number)
	labelReq, err := http.NewRequestWithContext(ctx, http.MethodGet, labelURL, nil)
	require.NoError(t, err)
	labelReq.Header.Set("Authorization", "Bearer "+env.token)
	labelReq.Header.Set("Accept", "application/vnd.github+json")
	labelResp, err := http.DefaultClient.Do(labelReq)
	require.NoError(t, err)
	defer labelResp.Body.Close()

	var labels []struct {
		Name string `json:"name"`
	}
	err = json.NewDecoder(labelResp.Body).Decode(&labels)
	require.NoError(t, err, "decoding labels response")

	labelNames := make([]string, len(labels))
	for i, l := range labels {
		labelNames[i] = l.Name
	}
	t.Logf("Issue labels after triage: %v", labelNames)

	hasTriageLabel := false
	for _, name := range labelNames {
		if name == "needs-info" || name == "ready-to-code" || name == "duplicate" {
			hasTriageLabel = true
			break
		}
	}
	assert.True(t, hasTriageLabel,
		"issue should have a triage label (needs-info, ready-to-code, or duplicate), got: %v", labelNames)
}

// truncateEnd returns the last n characters of s, or all of s if shorter.
func truncateEnd(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...\n" + s[len(s)-n:]
}
```

- [ ] **Step 3: Update the `verifyInstalled` file list**

In `verifyInstalled`, the file list checks for `scripts/validate-triage.sh` which no longer exists. Replace it with `scripts/post-triage.sh`:

Change `admin_test.go:376`:
```go
		"scripts/validate-triage.sh",
```
to:
```go
		"scripts/post-triage.sh",
```

- [ ] **Step 4: Add the `net/http` import if not already present**

The label verification uses `http.NewRequestWithContext`. Check if `net/http` is already imported in `admin_test.go` — if not, add it.

- [ ] **Step 5: Verify compilation**

Run: `go build -tags e2e ./e2e/admin/...`

Expected: Clean compilation, no errors.

- [ ] **Step 6: Commit**

```bash
git add e2e/admin/admin_test.go
git commit -m "test(e2e): extend triage smoke test to verify agent output

Wait for the triage workflow to complete, then verify:
- Agent posted a comment on the issue
- Comment is from the bot account
- A triage label was applied (needs-info, ready-to-code, or duplicate)

Also updates the scaffold file check to expect post-triage.sh
instead of the deleted validate-triage.sh.

Refs #126"
```

---

### Task 12: Lint and final verification

- [ ] **Step 1: Run make lint**

Run: `make lint`

Expected: Clean pass. Fix any issues.

- [ ] **Step 2: Run Go tests (unit + forge)**

Run: `make go-test`

Expected: All tests pass, including the new forge `ListIssueComments` and scaffold embedding tests.

- [ ] **Step 3: Run the post-triage test harness**

Run: `bash internal/scaffold/fullsend-repo/scripts/post-triage-test.sh`

Expected: `All tests passed`

- [ ] **Step 3b: Run the schema validation test harness**

Run: `bash internal/scaffold/fullsend-repo/scripts/validate-output-schema-test.sh`

Expected: `All tests passed`

- [ ] **Step 4: Verify e2e test compiles**

Run: `go build -tags e2e ./e2e/admin/...`

Expected: Clean compilation. (Actually running `make e2e-test` requires live credentials and is done in CI or manually with `E2E_GITHUB_SESSION_FILE` set.)

- [ ] **Step 5: Verify the JSON schema is consistent between prompt and post-script**

Manual check: the `action` values in `triage.md` (`insufficient`, `duplicate`, `sufficient`) match the `case` branches in `post-triage.sh`. The `comment` field is used by all three actions. The `duplicate_of` field is used only by `duplicate`. The `triage_summary` field is used only by `sufficient`.

- [ ] **Step 6: Final commit if any fixes were needed**

```bash
git add -A
git commit -m "fix: address lint and test issues from triage agent implementation

Refs #126"
```

---

## Out of Scope (documented for future work)

These items are explicitly deferred per the story's "Out of scope (MVP)" section and the user's guidance:

1. **`not_a_bug` action** — The post-script has an explicit error for unknown actions. When this action is added, add a `case` branch that posts a comment and applies a `wont-fix` label.

2. **`feature_request` action** — Same pattern. Apply a `feature-request` label and optionally transfer to a different issue template.

3. **`stale` action** — For issues where the reporter never replied to a `needs-info` question. Would require a scheduled workflow, not an event-triggered one.

4. **Reproducibility check** — Running the reproduction steps in a sandbox to verify them. Deferred to a future story.

5. **`/triage` re-run behavior** — Reopening closed issues, re-triaging after major edits. The current implementation handles `issues.edited` but doesn't reopen closed issues.

6. **ADR amendment** — Story #126 calls for an ADR amending ADR 0002 to reflect that triage sees the whole issue. This is a docs-only task that should be done separately.

7. **ADR 0002 label naming update** — ADR 0002 uses `not-ready` and `ready-to-implement` throughout. This plan uses `needs-info` and `ready-to-code` instead (see "Label naming: deviation from ADR 0002" in Design Decisions). ADR 0002 should be updated in a separate documentation sweep to replace `not-ready` → `needs-info` and `ready-to-implement` → `ready-to-code`.

8. **Prompt injection defense** — Story 6 (#129) handles this. The triage prompt includes a note that issue content is untrusted, but the actual input scanning infrastructure is Story 6's scope.
