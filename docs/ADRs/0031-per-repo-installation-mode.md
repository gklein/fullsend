---
title: "0031. Per-repo installation mode"
status: Proposed
relates_to:
  - agent-infrastructure
  - agent-architecture
  - security-threat-model
topics:
  - installation
  - per-repo
  - reusable-workflows
  - distribution
  - github-apps
---

# 0031. Per-repo installation mode

Date: 2026-05-06

## Status

Proposed

## Context

Fullsend's installation model is per-org: `fullsend admin install` creates a dedicated `.fullsend` config repo, per-role GitHub Apps ([ADR 0007](0007-per-role-github-apps.md)), an org-level dispatch PAT ([ADR 0008](0008-workflow-dispatch-for-cross-repo-dispatch.md)), and shim workflows in enrolled repos. This requires org admin access and assumes all enrolled repos share agent configuration, credentials, and policies.

Some users cannot or do not want to use the per-org model:

1. **No org admin access** — contributors who admin specific repos but not the GitHub org.
2. **No sharing desired** — teams who want isolated agent configs, credentials, and billing for a single repo.
3. **Quick evaluation** — users who want to try fullsend on one repo without committing to org-wide setup.
4. **Personal repos** — individual developers on personal GitHub accounts (no org at all).

Two proposed ADRs create the building blocks that make per-repo possible:

- [ADR 0030](0030-reusable-workflows-for-action-installed-distribution.md) publishes reusable workflows and a root composite action from `fullsend-ai/fullsend`, enabling any repo to call fullsend infrastructure via `workflow_call` without copying workflow files.
- [ADR 0027](0027-central-token-mint-secretless-fullsend.md) introduces a central token mint with shared GitHub Apps, eliminating PEM secrets from config repos via OIDC-based credential issuance.

Combined, these make per-repo installation viable: a single ~30-line workflow file in the target repo, calling upstream reusable workflows, with credentials stored as repo-level secrets or obtained via the token mint.

## Options

### Alternative 1: Per-repo via scaffold copy

Run `fullsend admin install` targeting a single repo instead of an org. Copy all scaffold files (agent workflows, composite action, dispatcher, scripts) into the target repo.

**Rejected**: Same maintenance burden as per-org — the repo must re-run install to pick up upstream patches. Contradicts ADR 0030's motivation to eliminate workflow drift.

### Alternative 2: Single GitHub App for all roles

Use one GitHub App for triage, code, review, and fix roles to simplify per-repo setup.

**Rejected**: GitHub suppresses `pull_request_target` events when the triggering token belongs to the same App that owns the workflow. The fix→review loop requires the coder/fix agent to push commits that trigger review — if both roles share one App, the event is silently suppressed and the feedback cycle breaks. At minimum, coder and review must be separate Apps.

### Alternative 3: Per-repo as a separate codebase

Build a standalone per-repo tool or action that does not share infrastructure with per-org fullsend.

**Rejected**: Duplicates agent logic, composite action, and security controls. Per-repo should reuse the same reusable workflows as per-org, with mode detection to adapt behavior.

### Alternative 4: Two-app minimum (coder + review)

Reduce per-repo to two Apps instead of matching the full per-org app set.

**Rejected**: Dropping the triage App forces triage to share one of the other App identities, which conflates permissions (triage only needs `issues:write`, while coder has `contents:write`). The full per-role model (ADR 0007) provides least-privilege isolation. CLI automation (`fullsend init`) makes creating the Apps straightforward.

## Decision

### Overview

Add a **per-repo installation mode** where fullsend runs entirely within a single repository — no `.fullsend` config repo, no cross-repo dispatch, no org-level secrets. The target repo IS the config repo.

Per-repo reuses the reusable workflows from ADR 0030, adding one new artifact: `reusable-fullsend.yml`, an all-in-one routing and dispatch workflow that combines event-to-stage routing (currently in the ~380-line shim) with per-stage dispatch into a single `workflow_call` entry point.

### 1. Architecture

```
Per-org (current):

ENROLLED REPO                    .FULLSEND CONFIG REPO
─────────────                    ─────────────────────
fullsend.yml (shim, ~380 lines)  dispatch.yml → stage workflows
  │ workflow_dispatch (PAT)              │
  └──────────────────────────────────────┘

Per-repo (proposed):

TARGET REPO (self-contained)
────────────────────────────
.github/workflows/fullsend.yml (~30 lines, thin caller)
  │
  │ workflow_call (native, no PAT)
  └──> fullsend-ai/fullsend/.github/workflows/reusable-fullsend.yml@v1
         ├── routes event to stage
         ├── skips enrollment validation (per-repo mode)
         ├──> reusable-triage.yml  ─┐
         ├──> reusable-code.yml    ─┤── reusable workflows (ADR 0030)
         ├──> reusable-review.yml  ─┤
         └──> reusable-fix.yml     ─┘
                   │
         uses: fullsend-ai/fullsend@v1  (composite action)
         config: .fullsend/ directory in target repo
```

Per-repo requirements: repo admin, 3 GitHub Apps (triage + coder + review), GCP project for inference. No org admin, no dispatch PAT, no dedicated config repo.

### 2. Repo layout

```
target-repo/
├── .github/workflows/fullsend.yml    ← single workflow file (~30 lines)
├── .fullsend/                        ← in-repo config (optional)
│   ├── agents/                      ← agent prompt overrides
│   ├── harness/                     ← harness config overrides
│   ├── policies/                    ← sandbox policies
│   ├── skills/                      ← repo-specific skills
│   ├── scripts/                     ← pre/post scripts
│   └── config.yaml                  ← repo-level config
├── AGENTS.md
└── ... (source code)
```

The `.fullsend/` directory is optional. Without it, upstream defaults apply. Users add files to `.fullsend/` only to customize agent behavior.

### 3. Config layering

Per-repo collapses the three-tier config model to two tiers:

```
fullsend-ai/fullsend defaults  <  .fullsend/ directory  <  AGENTS.md
(base)                            (customize)              (instructions)
```

The org-level `.fullsend` config repo tier is skipped — the in-repo `.fullsend/` directory serves as both org and repo config.

### 4. The `reusable-fullsend.yml` workflow

This is the key new artifact, published in `fullsend-ai/fullsend/.github/workflows/`. It accepts event metadata via `workflow_call` inputs, routes events to stages using the same logic currently embedded in the shim, and conditionally dispatches to per-stage reusable workflows.

The routing logic maps:
- `issues` + `labeled` → stage based on label name (`ready-to-code` → code, `ready-for-review` → review)
- `issue_comment` + slash commands → `/triage`, `/code`, `/review`, `/fix`
- `pull_request_target` → review (or retro on close)
- `pull_request_review` + `changes_requested` from bot → fix

This workflow serves both per-repo and per-org simplified shims. Per-org thin shims (from ADR 0030) can also use it to replace the ~380-line shim + dispatcher.

**Nesting depth**: target-repo workflow → `reusable-fullsend.yml` → `reusable-code.yml` = 2 levels of `workflow_call` (GitHub limit is 4).

### 5. Per-repo mode detection

Reusable workflows detect per-repo mode when `source_repo == github.repository` — the calling repo IS the target repo.

In per-repo mode:
- Enrollment validation is skipped (always self-enrolled).
- A single checkout retrieves both config (`.fullsend/` subdirectory) and code (repo root).
- `fullsend run` receives `--fullsend-dir=.fullsend` and `--target-repo=.`.

In per-org mode:
- Enrollment is validated against `config.yaml`.
- Two checkouts: `.fullsend` repo (config), then target repo into `target-repo/`.
- `fullsend run` receives `--fullsend-dir=.` and `--target-repo=target-repo`.

### 6. Credential models

Per-repo supports two credential models:

**Model A: Per-role Apps (own)**

Same as per-org ([ADR 0007](0007-per-role-github-apps.md)), but Apps are user-owned and installed on specific repos. Each role gets its own GitHub App:

| App | Role | Key permissions |
|-----|------|-----------------|
| `{user}-fullsend` | Orchestrator | `actions:write`, `contents:write`, `workflows:write`, `administration:write` |
| `{user}-triage` | Triage | `issues:write` |
| `{user}-coder` | Code + fix | `contents:write`, `pull-requests:write`, `issues:read`, `checks:read` |
| `{user}-review` | Review | `contents:read`, `pull-requests:write`, `issues:read`, `checks:read` |

The orchestrator App is optional for per-repo (it handles enrollment reconciliation, which does not apply). Per-repo users need at minimum triage, coder, and review.

PEMs stored as repo secrets, client IDs as repo variables.

**Model B: Token mint + shared Apps ([ADR 0027](0027-central-token-mint-secretless-fullsend.md))**

Shared public fullsend Apps installed on the repo. Token mint handles credential issuance via OIDC. No PEMs in the repo — the mint holds them.

The mint's `job_workflow_ref` validation accepts both patterns:
- `{org}/.fullsend/.github/workflows/*.yml@*` (per-org)
- `fullsend-ai/fullsend/.github/workflows/reusable-*.yml@*` (per-repo)

The `repository_owner` claim still scopes tokens to the calling org/user.

**Credential auto-detection**: Reusable workflows detect the credential model automatically:
- `FULLSEND_MINT_URL` present → mint mode (OIDC token exchange)
- `FULLSEND_CODER_APP_PRIVATE_KEY` + `FULLSEND_REVIEW_APP_PRIVATE_KEY` present → per-role App mode
- Neither → error with setup instructions

### 7. CLI support: `fullsend init`

A new CLI command for per-repo setup:

```
fullsend init [--mint-url URL] [--skip-orchestrator]
```

1. Generates `.github/workflows/fullsend.yml` from template.
2. Guides user through GitHub App creation via manifest flow — creates triage, coder, and review Apps.
3. Stores PEMs as repo secrets, client IDs as repo variables.
4. Optionally creates `.fullsend/` directory with default config.

With `--mint-url`, App creation is skipped — the user installs shared Apps instead.

### 8. Coexistence

Per-repo and per-org coexist within the same org. Some repos use the org `.fullsend` config repo (per-org), others run independently (per-repo). There is no conflict — they use different dispatch paths and credential stores.

Migration between models is straightforward:
- **Per-repo → per-org**: Remove workflow file from target repo, add to `.fullsend/config.yaml` enrollment.
- **Per-org → per-repo**: Remove from enrollment, add workflow file and secrets to target repo.

## Consequences

### Positive

- **No org admin required**: Repo admins can adopt fullsend without org-level access or coordination.
- **Self-contained**: Everything fullsend needs lives in one repo — simpler mental model, easier cleanup.
- **Reuses ADR 0030 infrastructure**: Per-repo adds one workflow (`reusable-fullsend.yml`); all other reusable workflows and the composite action are shared with per-org.
- **Low entry barrier**: Copy one workflow file, create Apps (or install shared ones), set secrets — working fullsend in under 15 minutes.
- **Reduced blast radius**: Credential compromise affects only the single repo, not all enrolled repos in an org.
- **Same agent behavior**: Triage → Code → Review → Fix workflow is identical from the user's perspective.

### Negative

- **More Apps per user**: Each per-repo user creates their own Apps (unless using the token mint).
- **Config governance weaker**: In per-org, agent config lives in a separate repo with its own CODEOWNERS. In per-repo, `.fullsend/` config lives alongside code — a code contributor could modify agent behavior in a PR (mitigated by CODEOWNERS on `.fullsend/` and base-branch checkout).
- **No centralized policy**: Per-repo users set their own policies. An org cannot enforce uniform agent behavior across independently-installed repos.
- **Credential rotation burden**: Each per-repo user manages their own App PEM rotation (unless using the token mint).

### Risks

- **`pull_request_target` misconfiguration**: Per-repo workflows MUST use `pull_request_target` (not `pull_request`) to prevent PR authors from modifying the workflow to exfiltrate secrets. The workflow template enforces this, but users could edit it.
- **`event_payload` size**: The per-repo workflow passes `toJSON(github.event)` as a `workflow_call` input. GitHub's `workflow_call` inputs have a 65KB limit. Large PR event payloads could exceed this.
- **App identity confusion**: Users unfamiliar with the fix→review loop requirement may attempt a single-App setup and get silent failures (no review triggered after fix pushes).

### Mitigations

- **Template validation**: `fullsend init` generates the workflow file with `pull_request_target` — users who modify it are warned in documentation.
- **Payload trimming**: Start with full `toJSON(github.event)` for simplicity; add payload trimming if size becomes an issue in practice.
- **Clear error messages**: Credential auto-detection reports why coder and review Apps must be separate, with a link to setup documentation.
- **Migration path**: Per-repo users who outgrow the model can migrate to per-org without changing agent behavior — the same reusable workflows power both modes.

## Open Questions

### `reusable-fullsend.yml` for per-org shim simplification

This workflow is also useful as a per-org shim simplification (replacing the ~380-line shim + dispatcher with a thin caller). Should per-org thin shims also adopt it?

**Trade-off**: Sharing the routing workflow between per-repo and per-org reduces maintenance (one routing implementation), but couples per-org dispatch to the upstream workflow's release cadence. Per-org deployments currently control their own dispatch timing.

### Retro stage

The routing logic includes a retro stage (PR closed). Reusable workflows for retro (`reusable-retro.yml`) are not yet defined in ADR 0030. This stage should be added to the reusable workflow set or explicitly deferred.

### Concurrency groups

Concurrent fullsend runs for the same issue/PR should be prevented. Options: workflow-level concurrency in the caller, or per-stage concurrency inside `reusable-fullsend.yml`. Per-stage concurrency inside the reusable workflow keeps the caller simple and applies consistently across per-repo and per-org modes.

## References

- [ADR 0007: Per-role GitHub Apps](0007-per-role-github-apps.md) — authentication model replicated in per-repo
- [ADR 0008: workflow_dispatch for cross-repo dispatch](0008-workflow-dispatch-for-cross-repo-dispatch.md) — replaced by `workflow_call` in per-repo
- [ADR 0026: Stage-based dispatch](0026-stage-based-dispatch-for-agent-workflow-decoupling.md) — routing logic extracted into `reusable-fullsend.yml`
- [ADR 0027: Central token mint](0027-central-token-mint-secretless-fullsend.md) — optional credential enhancement for per-repo
- [ADR 0030: Reusable workflows](0030-reusable-workflows-for-action-installed-distribution.md) — foundation that makes per-repo possible
