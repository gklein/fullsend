---
title: "33. Per-repo installation mode"
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

# 33. Per-repo installation mode

Date: 2026-05-06

## Status

Proposed

## Context

Fullsend's installation model is per-org: `fullsend admin install` creates a dedicated `.fullsend` config repo, per-role GitHub Apps ([ADR 0007](0007-per-role-github-apps.md)), shim workflows in enrolled repos, and a central token mint for OIDC-based credential issuance ([ADR 0027](0027-central-token-mint-secretless-fullsend.md)). This requires org admin access and assumes all enrolled repos share agent configuration, credentials, and policies.

Some users cannot or do not want to use the per-org model:

1. **No org-wide setup desired** — teams who want fullsend on specific repos without the full `fullsend admin install` org setup (org admin is still needed to approve GitHub App installation on the repo).
2. **Private repos** — private repos cannot call `workflow_call` into a separate `.fullsend` config repo unless that repo is also visible to the caller. Per-repo avoids cross-repo visibility constraints by calling upstream `fullsend-ai/fullsend` (public) directly.
3. **No sharing desired** — teams who want isolated agent configs, credentials, and billing for a single repo.
4. **Quick evaluation** — users who want to try fullsend on one repo without committing to org-wide setup.
5. **Personal repos** — individual developers on personal GitHub accounts (no org at all).

Two ADRs create the building blocks that make per-repo possible:

- [ADR 0031](0031-reusable-workflows-for-action-installed-distribution.md) publishes reusable workflows and four composite actions (`fullsend`, `mint-token`, `validate-enrollment`, `setup-gcp`) from `fullsend-ai/fullsend`, enabling any repo to call fullsend infrastructure via `workflow_call` without copying workflow files.
- [ADR 0027](0027-central-token-mint-secretless-fullsend.md) replaces PEM secrets and dispatch PATs with OIDC-based credential issuance via a central token mint. The `mint-token` composite action takes a role name (triage, coder, review, fix) and returns a scoped GitHub App installation token — no PEMs or client IDs in the calling repo.

Combined, these make per-repo installation viable: a single ~30-line workflow file in the target repo, calling upstream reusable workflows, with credentials issued by the token mint.

## Options

### Alternative 1: Per-repo via scaffold copy

Run `fullsend admin install` targeting a single repo instead of an org. Copy all scaffold files (agent workflows, composite action, dispatcher, scripts) into the target repo.

**Rejected**: Same maintenance burden as per-org — the repo must re-run install to pick up upstream patches. Contradicts ADR 0031's motivation to eliminate workflow drift.

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

Per-repo reuses the reusable workflows from ADR 0031, adding one new artifact: `reusable-fullsend.yml`, an all-in-one routing and dispatch workflow that combines event-to-stage routing (currently in the ~380-line shim) with per-stage dispatch into a single `workflow_call` entry point.

### 1. Architecture

```
Per-org (current):

ENROLLED REPO                    .FULLSEND CONFIG REPO
─────────────                    ─────────────────────
fullsend.yml (shim)              dispatch.yml → thin caller stage workflows
  │ workflow_call                        │ workflow_call
  └──────────────────────────────────────┘
                                         └──> reusable workflows (ADR 0031)
                                               uses: fullsend-ai/fullsend@v1
                                               uses: mint-token, validate-enrollment, setup-gcp

Per-repo (proposed):

TARGET REPO (self-contained)
────────────────────────────
.github/workflows/fullsend.yml (~30 lines, thin caller)
  │
  │ workflow_call
  └──> fullsend-ai/fullsend/.github/workflows/reusable-fullsend.yml@v1
         ├── routes event to stage
         ├── skips enrollment validation (per-repo mode)
         ├──> reusable-triage.yml  ─┐
         ├──> reusable-code.yml    ─┤── reusable workflows (ADR 0031)
         ├──> reusable-review.yml  ─┤
         └──> reusable-fix.yml     ─┘
                   │
         uses: fullsend-ai/fullsend@v1          (run agent)
         uses: ./.github/actions/mint-token      (OIDC → scoped token)
         uses: ./.github/actions/setup-gcp       (GCP auth)
         config: .fullsend/ directory in target repo
```

Per-repo requirements: repo admin + org admin to install GitHub Apps on the repo, GCP project for inference. No dedicated config repo, no shim workflows, no cross-repo dispatch.

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

This workflow serves both per-repo and per-org simplified shims. Per-org thin shims (from ADR 0031) can also use it to replace the ~380-line shim + dispatcher.

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

**Model A: Token mint (default, [ADR 0027](0027-central-token-mint-secretless-fullsend.md))**

GitHub Apps managed by the mint operator are installed on the repo. The
`mint-token` composite action exchanges a GitHub OIDC token for a scoped
GitHub App installation token — no PEMs, client IDs, or App secrets in the
repo. The action takes a `role` parameter (triage, coder, review, fix) and
the mint looks up the correct App PEM in GCP Secret Manager using the
`repository_owner` claim from the OIDC token.

The mint's `job_workflow_ref` validation accepts both patterns:
- `{org}/.fullsend/.github/workflows/*.yml@*` (per-org)
- `fullsend-ai/fullsend/.github/workflows/reusable-*.yml@*` (per-repo)

The `repository_owner` claim scopes tokens to the calling org/user.
`ALLOWED_ORGS` on the mint controls which orgs may request tokens.

**Model B: Own Apps (self-managed)**

For users who want full control over their GitHub Apps — same per-role model
as per-org ([ADR 0007](0007-per-role-github-apps.md)), but Apps are
user-owned and installed on specific repos:

| App | Role | Key permissions |
|-----|------|-----------------|
| `{user}-triage` | Triage | `issues:write` |
| `{user}-coder` | Code + fix | `contents:write`, `pull-requests:write`, `issues:read`, `checks:read` |
| `{user}-review` | Review | `contents:read`, `pull-requests:write`, `issues:read`, `checks:read` |

PEMs stored in the user's own GCP Secret Manager project (accessed via the
same `mint-token` action pointed at a self-hosted mint) or as repo secrets
with direct App token generation via `actions/create-github-app-token`.

**Credential detection**: The `mint-token` composite action is the default
path. Reusable workflows call `mint-token` with the agent role; the action
handles OIDC exchange and token scoping. For self-managed Apps using direct
token generation, the reusable workflow falls back to
`actions/create-github-app-token` when `FULLSEND_MINT_URL` is not set and
App PEM secrets are present.

### 7. CLI support: `fullsend init`

A new CLI command for per-repo setup:

```
fullsend init [--mint-url URL] [--own-apps]
```

1. Generates `.github/workflows/fullsend.yml` from template.
2. Sets `FULLSEND_MINT_URL` as a repo variable (default: public mint).
3. Guides user to install the shared fullsend Apps on their repo.
4. Optionally creates `.fullsend/` directory with default config.

With `--own-apps`, the user creates their own GitHub Apps via the manifest
flow instead of using the shared mint. PEMs are stored in the user's own
GCP Secret Manager project.

### 8. Coexistence

Per-repo and per-org coexist within the same org. Some repos use the org `.fullsend` config repo (per-org), others run independently (per-repo). There is no conflict — they use different dispatch paths and credential stores.

Migration between models is straightforward:
- **Per-repo → per-org**: Remove workflow file from target repo, add to `.fullsend/config.yaml` enrollment.
- **Per-org → per-repo**: Remove from enrollment, add workflow file and secrets to target repo.

## Consequences

### Positive

- **No org admin required**: Repo admins can adopt fullsend without org-level access or coordination (though org admin is still needed to install the GitHub Apps on the repo).
- **Self-contained**: Everything fullsend needs lives in one repo — simpler mental model, easier cleanup.
- **Reuses ADR 0031 infrastructure**: Per-repo adds one workflow (`reusable-fullsend.yml`); all other reusable workflows and the four composite actions are shared with per-org.
- **Low entry barrier**: Copy one workflow file, install shared Apps, set mint URL — working fullsend in under 15 minutes. No PEMs or client IDs to manage.
- **Reduced blast radius**: Token mint scopes tokens to the requesting repo via the `repository` OIDC claim. Credential compromise affects only the single repo.
- **Same agent behavior**: Triage → Code → Review → Fix workflow is identical from the user's perspective.

### Negative

- **Org admin still needed for App installation**: While per-repo removes the need for org admin to run `fullsend admin install`, an org admin must still approve the GitHub App installation on the repo.
- **Config governance weaker**: In per-org, agent config lives in a separate repo with its own CODEOWNERS. In per-repo, `.fullsend/` config lives alongside code — a code contributor could modify agent behavior in a PR (mitigated by CODEOWNERS on `.fullsend/` and base-branch checkout).
- **No centralized policy**: Per-repo users set their own policies. An org cannot enforce uniform agent behavior across independently-installed repos.
- **Self-managed Apps increase burden**: Users who opt out of the token mint (Model B) manage their own App PEM rotation and GCP Secret Manager project.

### Risks

- **`pull_request_target` misconfiguration**: Per-repo workflows MUST use `pull_request_target` (not `pull_request`) to prevent PR authors from modifying the workflow to exfiltrate secrets. The workflow template enforces this, but users could edit it.
- **`event_payload` size**: The per-repo workflow passes `toJSON(github.event)` as a `workflow_call` input. GitHub's `workflow_call` inputs have a 65KB limit. Large PR event payloads could exceed this.
- **App identity confusion**: Users unfamiliar with the fix→review loop requirement may attempt a single-App setup and get silent failures (no review triggered after fix pushes).

### Mitigations

- **Template validation**: `fullsend init` generates the workflow file with `pull_request_target` — users who modify it are warned in documentation. CODEOWNERS on `.github/workflows/fullsend.yml` prevents unauthorized changes.
- **Payload trimming**: Start with full `toJSON(github.event)` for simplicity; add payload trimming if size becomes an issue in practice.
- **Clear error messages**: Credential auto-detection reports why coder and review Apps must be separate, with a link to setup documentation.
- **Migration path**: Per-repo users who outgrow the model can migrate to per-org without changing agent behavior — the same reusable workflows power both modes.

## Open Questions

### `reusable-fullsend.yml` for per-org shim simplification

This workflow is also useful as a per-org shim simplification (replacing the ~380-line shim + dispatcher with a thin caller). Should per-org thin shims also adopt it?

**Trade-off**: Sharing the routing workflow between per-repo and per-org reduces maintenance (one routing implementation), but couples per-org dispatch to the upstream workflow's release cadence. Per-org deployments currently control their own dispatch timing.

### Retro stage

The routing logic includes a retro stage (PR closed). Reusable workflows for retro (`reusable-retro.yml`) are not yet defined in ADR 0031. This stage should be added to the reusable workflow set or explicitly deferred.

### Concurrency groups

Concurrent fullsend runs for the same issue/PR should be prevented. Options: workflow-level concurrency in the caller, or per-stage concurrency inside `reusable-fullsend.yml`. Per-stage concurrency inside the reusable workflow keeps the caller simple and applies consistently across per-repo and per-org modes.

## References

- [ADR 0007: Per-role GitHub Apps](0007-per-role-github-apps.md) — authentication model replicated in per-repo
- [ADR 0008: workflow_dispatch for cross-repo dispatch](0008-workflow-dispatch-for-cross-repo-dispatch.md) — superseded by `workflow_call` (ADR 0027 removes the original constraint)
- [ADR 0026: Stage-based dispatch](0026-stage-based-dispatch-for-agent-workflow-decoupling.md) — routing logic extracted into `reusable-fullsend.yml`
- [ADR 0027: Central token mint](0027-central-token-mint-secretless-fullsend.md) — default credential model for per-repo
- [ADR 0031: Reusable workflows](0031-reusable-workflows-for-action-installed-distribution.md) — foundation that makes per-repo possible
