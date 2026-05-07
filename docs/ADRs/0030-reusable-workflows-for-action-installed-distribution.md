---
title: "30. Reusable workflows for action-installed distribution"
status: Proposed
relates_to:
  - agent-infrastructure
topics:
  - workflows
  - distribution
  - reusable-workflows
  - composite-action
---

# 30. Reusable workflows for action-installed distribution

Date: 2026-05-06

## Status

Proposed

## Context

`fullsend admin install` copies ~50 files from the Go binary's embedded scaffold
(`internal/scaffold/fullsend-repo/`) into each org's `.fullsend` repo. This
includes agent workflows (125–354 lines each), a composite action, setup
scripts, and a dispatcher. When a bug is fixed or a security patch lands in the
scaffold, every org must re-run `fullsend admin install` to pick up the change.
Workflow drift across orgs is the norm.

The dispatch chain established in
[ADR 0026](0026-stage-based-dispatch-for-agent-workflow-decoupling.md) —
shim → `dispatch.yml` → agent workflows — is preserved. Only the agent
workflows themselves change from full copies to thin callers.

## Options

### Option A: Scaffold copies (status quo)

`fullsend admin install` writes full agent workflows into `.fullsend`. Each org
gets its own copy. Updates require re-running install in every org.

### Option B: Published composite action only

Publish the composite action at `fullsend-ai/fullsend@v1`. Agent workflows in
`.fullsend` replace `uses: ./.github/actions/fullsend` with the published
reference. Infrastructure logic (checkout, token generation, GCP auth, sandbox
setup) stays duplicated in each org's workflow files.

### Option C: Reusable workflows + published composite action

Publish reusable workflows (`workflow_call`) and a root composite action from
`fullsend-ai/fullsend`. Agent workflows in `.fullsend` shrink to ~20 line thin
callers that delegate infrastructure logic upstream via `workflow_call` with
`secrets: inherit`. Org-specific content (agents, harness, env, policies,
scripts) stays local.

## Decision

Use Option C. Publish reusable workflows
(`fullsend-ai/fullsend/.github/workflows/reusable-{agent}.yml`) and a root
composite action (`fullsend-ai/fullsend@v1`).

Thin callers in `.fullsend` use `workflow_call` to invoke upstream reusable
workflows. Since `workflow_call` runs the callee in the caller's repo context,
the reusable workflow has access to `.fullsend` secrets and checks out the
config repo directly. Secrets pass via `secrets: inherit`. Org-specific `vars.*`
values (client IDs, GCP region, auth mode) pass as explicit `inputs.*` because
`vars` do not cross the `workflow_call` boundary.

`dispatch.yml` stays unchanged — thin callers retain `# fullsend-stage:`
markers, so stage-based dispatch
([ADR 0026](0026-stage-based-dispatch-for-agent-workflow-decoupling.md))
continues to work without modification.

The dispatch chain uses 1 level of `workflow_call` nesting (limit is 4):

```
shim ──workflow_dispatch──> .fullsend/dispatch.yml
        ──workflow_dispatch──> .fullsend/code.yml (thin caller)
            ──workflow_call──> reusable-code.yml (level 1)
                ──uses──> fullsend-ai/fullsend@v1 (composite action)
```

## Consequences

- Infrastructure patches (checkout, token generation, GCP auth, sandbox setup)
  ship once upstream and propagate to all orgs on next workflow run — no
  re-install required.
- `fullsend-ai/fullsend` must remain public for `workflow_call` and `uses:`
  references to resolve (it already is).
- Each org must map `vars.*` to explicit `inputs.*` in thin callers, since
  `vars` do not cross the `workflow_call` boundary.
- Thin callers pin upstream by tag (`@v1`) or SHA — orgs control when they
  adopt upstream changes.
- Stage-based dispatch ([ADR 0026](0026-stage-based-dispatch-for-agent-workflow-decoupling.md)),
  shim workflows, and org-specific content (agents, harness, policies, scripts)
  are unchanged.
- **Trust boundary shift:** `secrets: inherit` passes all caller secrets to
  reusable workflow code hosted in `fullsend-ai/fullsend` (a public repo).
  Under the scaffold-copy model, secrets are consumed by code in the org's
  own repo. Under the reusable-workflow model, secrets flow to upstream code.
  SHA pinning (not just tag pinning) gives orgs full control over which
  upstream code runs with their secrets. This aligns with the project's
  threat model priority on external injection — a compromised upstream ref
  could affect all downstream orgs simultaneously, making SHA pinning the
  recommended default for production installations.
- **Upstream availability:** `fullsend-ai/fullsend` becomes a runtime
  dependency for all downstream orgs. If the repo is unavailable or a pinned
  ref is deleted, downstream workflow runs fail. Scaffold copies are immune
  to upstream outages after install.
- **Cross-repo debugging:** When reusable workflows fail, the call stack spans
  two repositories (the org's `.fullsend` and `fullsend-ai/fullsend`).
  Developers must inspect both the thin caller and the upstream workflow to
  diagnose failures. GitHub's workflow run UI shows reusable workflow steps
  inline, which partially mitigates this.
- **GitHub-specific mechanism:** `workflow_call` and `secrets: inherit` are
  GitHub Actions primitives with no direct equivalent in other CI systems.
  Multi-forge support (ADR 0028, [PR #601](https://github.com/fullsend-ai/fullsend/pull/601)) will need its own
  distribution mechanism (e.g., GitLab CI/CD Components or `include:`)
  independent of this ADR.
