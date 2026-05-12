---
title: "35. Layered content resolution"
status: Proposed
relates_to:
  - agent-infrastructure
  - agent-architecture
topics:
  - layering
  - defaults
  - content-resolution
  - scaffold
---

# 35. Layered content resolution

Date: 2026-05-09

## Status

Proposed

## Context

[ADR 0003](0003-org-config-repo-convention.md) designed a three-tier layering
model — `fullsend defaults < org .fullsend config < per-repo overrides` — but
the runtime never implemented it. The scaffold (`internal/scaffold/scaffold.go`)
copies all ~82 files from `internal/scaffold/fullsend-repo/` into every
`.fullsend` repo via the Git Trees API, including agents, skills, harness
configs, policies, and schemas that are identical across orgs.

This causes three problems: upstream improvements require every org to re-run
`fullsend admin install`, the installer overwrites all files on upgrade
(silently reverting org customizations), and there is no visible distinction
between upstream defaults and org-specific content.

With reusable workflows (thin callers in `.fullsend` that delegate to
`fullsend-ai/fullsend` via `workflow_call`), the pipeline checks out `.fullsend`
at runtime, enabling a workspace merge that implements the layering model.

## Decision

Three coordinated changes:

**A. Org customizations live in `customized/`.** The scaffold installs a
`customized/` directory with empty subdirs (`agents/`, `skills/`, `schemas/`,
`harness/`, `policies/`, `scripts/`) containing `.gitkeep` files. Orgs add
overrides here. The main dirs are not in the `.fullsend` repo — they are
populated at runtime from upstream.

**B. Runtime layering via reusable workflows.** Each reusable workflow adds a
"Prepare workspace" step that sparse-checkouts upstream defaults from
`fullsend-ai/fullsend@v1`, copies them into the main dirs (`agents/`, `skills/`,
etc.), then copies `customized/*` on top so org files overwrite upstream
defaults. The harness sees a single flat workspace — no changes to
`ResolveRelativeTo()` or `--fullsend-dir`.

**C. Scaffold stops writing upstream defaults.** `WalkFullsendRepo` skips files
in layered directories (`agents/`, `skills/`, `schemas/`, `harness/`,
`policies/`, `scripts/`) and upstream-only directories (`.github/actions/`,
`.github/scripts/`). The installer writes only org-specific files and
`customized/` gitkeeps.

File categories after this change:

- **Org-only** (~18 files): `env/`, `dispatch.yml`, thin callers, shim
  template, `AGENTS.md` — always installed, never overwritten by upstream.
- **Org overrides** (6 `.gitkeep` files): `customized/{agents,skills,...}/` —
  scaffold creates the structure, orgs add real files.
- **Upstream defaults** (~53 files): agents, skills, schemas, harness,
  policies, scripts — authoritative in `fullsend-ai/fullsend`, provided at
  runtime via sparse checkout of the release tag.
- **Upstream infrastructure** (~5 files): composite actions,
  `setup-agent-env.sh` — referenced directly from upstream, never in `.fullsend`.

## Consequences

- Fresh install produces a slim `.fullsend` repo (~24 files instead of ~82).
- Upgrades never overwrite org content — the installer does not touch
  customizable content.
- Upstream improvements to agents, skills, and schemas appear automatically at
  runtime without re-install.
- Org overrides are explicit and auditable in `customized/`.
- Requires a public upstream repo (`fullsend-ai/fullsend` is already public).
- Runtime availability: sparse checkout of upstream defaults requires
  github.com to be reachable at workflow execution time. GitHub Actions already
  depends on github.com, so this adds no new availability boundary.
- Migration for existing orgs: orgs that customized files in top-level dirs
  (e.g., `agents/triage.md`) must move them to `customized/` (e.g.,
  `customized/agents/triage.md`) before upgrading. The installer can detect
  and warn about this during `fullsend admin install`.
