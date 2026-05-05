---
title: "0027. Central token mint and shared apps for a secretless .fullsend"
status: Proposed
relates_to:
  - agent-infrastructure
  - security-threat-model
  - agent-architecture
topics:
  - identity
  - oidc
  - github-apps
  - deployment
---

# 0027. Central token mint and shared apps for a secretless .fullsend

Date: 2026-05-05

## Status

Proposed

## Context

The Fullsend run layer security model must constrain two risks: unauthorized access to model APIs, and impersonation of Fullsend agents on the forge. The current architecture keeps LLM credentials and per-role GitHub App private keys as GitHub Actions secrets in the org’s `.fullsend` config repo, relies on org admins to protect that repo, and assumes only workflows defined there can read those secrets ([ADR 0007](0007-per-role-github-apps.md), [ADR 0008](0008-workflow-dispatch-for-cross-repo-dispatch.md)).

That layout has operational costs: enrolled repos must trigger `.fullsend` via `workflow_dispatch` authenticated with a long-lived fine-grained PAT so that caller-scoped secrets do not block access to PEMs in the config repo ([ADR 0008](0008-workflow-dispatch-for-cross-repo-dispatch.md)). Because those workflows can use the App keys, each org historically needed its own agent apps to avoid cross-org permission leakage. GitHub’s controls also make fully automated PAT and App lifecycle painful, which works against hands-off deployment.

Workload identity federation and related patterns already move LLM access toward short-lived, non-repo-stored credentials (see [ADR 0025](0025-provider-credential-delivery-for-sandboxed-agents.md) and [security-threat-model.md](../problems/security-threat-model.md)). The remaining gap is GitHub agent identity: if App secrets leave the `.fullsend` repo, dispatch can revert to `workflow_call`, and orgs can stop minting their own Apps and PATs for baseline installs.

## Options

- **Status quo:** Keep PEMs and any remaining provider secrets in `.fullsend`, retain `workflow_dispatch` and the org-level dispatch PAT, and keep per-org GitHub Apps ([ADR 0007](0007-per-role-github-apps.md), [ADR 0008](0008-workflow-dispatch-for-cross-repo-dispatch.md)).
- **Central mint + shared apps (this ADR):** Operate a token mint that alone holds App credentials; `.fullsend` workflows prove workload identity via OIDC and receive short-lived, org-scoped forge tokens.

## Decision

Adopt a **central token mint** and **public, shared GitHub Apps** as the default way to give Fullsend agents forge identity, so the `.fullsend` repository needs **no long-lived secrets** for LLM access or App private keys.

1. **Shared Apps.** Define a small set of well-known GitHub Apps (per agent role or equivalent) that all adopting orgs install. Private keys live only with the mint service, not in each org’s config repo.
2. **Token mint.** The mint accepts OIDC tokens from approved workloads (GitHub Actions today), verifies that the caller is an allowed workflow from the expected `.fullsend` repository (e.g. using claims such as `job_workflow_ref`), and returns a **short-lived, org-scoped** token suitable for impersonating the correct App installation for that run.
3. **Workflow integration.** `.fullsend` workflows obtain forge tokens from the mint when they need GitHub API access instead of reading PEM secrets locally.
4. **Deployment profiles.** Multiple mint instances may exist (e.g. vendor-operated vs community-operated), each paired with its own App registrations and trust policies; orgs choose which mint to trust rather than creating bespoke Apps and PATs for the default path.
5. **Extensibility.** The same mint pattern can be extended to other CI platforms by validating that platform’s OIDC or workload tokens (e.g. Tekton pipeline service account tokens), and to other SCMs by minting the equivalent bot credentials once those forges are supported.

Non-sensitive configuration that today is stored as secrets only for convenience may move to org-level Actions variables or similar once the mint is authoritative for true secrets.

## Consequences

- `.fullsend` no longer stores App PEMs or model API secrets; enrollment and updates avoid managing those repo-scoped secrets.
- Shim workflows can call `.fullsend` via `workflow_call` instead of `workflow_dispatch`, eliminating the org-level dispatch PAT for that integration path.
- Org onboarding shifts to installing shared Apps and selecting a mint endpoint rather than creating and rotating org-specific Apps and PATs for the default case.
- The mint and its key material become a high-value target; compromise affects all orgs using that mint, requiring strong operations, monitoring, and optional regional or vendor-specific mint isolation.
- Trust in `job_workflow_ref` (or equivalent) binds tokens to approved workflow definitions; spoofing a fake `.fullsend` repo inside another org yields tokens scoped only to that org’s installations, not cross-tenant access.
- Superseding details of [ADR 0007](0007-per-role-github-apps.md) and [ADR 0008](0008-workflow-dispatch-for-cross-repo-dispatch.md) will require follow-on ADRs once this direction is accepted and implemented.
