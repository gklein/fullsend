---
title: "26. repository_dispatch for agent workflow decoupling"
status: Proposed
relates_to:
  - agent-infrastructure
topics:
  - dispatch
  - workflows
  - decoupling
---

# 26. repository_dispatch for agent workflow decoupling

Date: 2026-04-28

## Status

Proposed

## Context

[ADR 0008](0008-workflow-dispatch-for-cross-repo-dispatch.md) established that
enrolled repos use `workflow_dispatch` to trigger agent workflows in the
`.fullsend` config repo. Currently, each shim job calls a specific agent
workflow by name (`triage.yml`, `code.yml`, `review.yml`). This creates tight
coupling: whenever the agent workflow inventory changes — adding a new agent,
removing one, or renaming a workflow file — every enrolled repo's shim must be
updated and redeployed
([#335](https://github.com/fullsend-ai/fullsend/issues/335)).

[ADR 0020](0020-composable-single-responsibility-agents-with-individual-sandboxes.md)
established that stages are composed of multiple single-responsibility agents.
As stages gain more agents, the cost of shim-to-workflow coupling increases —
adding a new agent to a stage should not require touching enrolled repos.

The shim runs in enrolled repos under `pull_request_target`, where it cannot be
modified by PRs ([ADR 0009](0009-pull-request-target-in-shim-workflows.md)).
This is a security property worth preserving — but it also means shim changes
require a privileged update to every enrolled repo.

See [PR #390](https://github.com/fullsend-ai/fullsend/pull/390) for the
implementation.

## Options

### Option A: Direct workflow_dispatch (status quo)

The shim in each enrolled repo calls agent workflows by name via
`workflow_dispatch` (`gh workflow run triage.yml`, `gh workflow run code.yml`,
etc.). Each shim job is bound to a specific workflow file in `.fullsend`.

- Simple: one hop, no indirection, easy to trace.
- Coupled: adding, removing, or renaming an agent workflow requires updating
  the shim in every enrolled repo.
- One-to-one: each shim job triggers exactly one workflow. Running multiple
  agents for the same stage requires adding more shim jobs.

### Option B: Dispatcher with repository_dispatch

A `dispatch-agent.yml` workflow in `.fullsend` receives `workflow_dispatch`
calls from the shim with a `stage` parameter. It emits a `repository_dispatch`
event (e.g., `fullsend-triage`) on the config repo. Agent workflows subscribe
via `on.repository_dispatch.types`.

- Decoupled: the shim knows about stages, not workflows. Agent changes stay
  in `.fullsend`.
- Fan-out: multiple workflows can subscribe to the same event type, running
  in parallel without coordination logic.
- Extra hop: one additional workflow execution per event, adding Actions
  minutes and latency.
- Direct `workflow_dispatch` on individual agent workflows still works for
  testing and debugging.

## Decision

Use Option B. Introduce `dispatch-agent.yml` as an indirection layer between
the shim and agent workflows. The shim calls it with a `stage` parameter
(triage, code, review). The dispatcher emits a `repository_dispatch` event on
the config repo. Agent workflows subscribe to these events via
`on.repository_dispatch.types`.

The shim knows about **stages**, not **workflows**. Adding, removing, or
replacing agent workflows within a stage requires no shim changes — only
changes to the `.fullsend` config repo.

The dispatcher authenticates the `repository_dispatch` call using an
installation token from the orchestrator GitHub App, keeping the PAT
(`FULLSEND_DISPATCH_TOKEN`) confined to the `workflow_dispatch` boundary
between enrolled repos and the config repo.

## Consequences

- Agent workflows can be added, removed, or replaced without modifying or
  redeploying shim workflows in enrolled repos.
- Multiple workflows can subscribe to the same `repository_dispatch` event type,
  enabling parallel fan-out within a stage without coordination logic.
- The credential boundary from [ADR 0008](0008-workflow-dispatch-for-cross-repo-dispatch.md)
  is preserved: enrolled repos hold only the dispatch PAT; App PEMs stay in the
  config repo.
- An additional workflow execution (the dispatcher) runs on every event,
  increasing Actions minutes and adding latency to the dispatch path.
- Adding a new agent to a stage is a single-file operation: create a workflow
  in `.fullsend` that subscribes to the relevant `repository_dispatch` event.
  This pattern is repeatable enough to be templated or tooled.
- Adding a new **stage** (as opposed to a new agent within an existing stage)
  still requires changes to the dispatcher's `stage` choice list and to the
  shim template. This decoupling applies to the agent inventory within a stage,
  not to the stage inventory itself.
- Orchestration within a stage is limited to parallel fan-out.
  Sequential execution, conditional chaining, and fan-in between agents within
  a stage are **out of scope** — those require the pipeline definition format
  deferred in [ADR 0018](0018-scripted-pipeline-for-multi-agent-orchestration.md).
