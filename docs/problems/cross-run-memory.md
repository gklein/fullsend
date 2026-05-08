# Cross-Run Memory

How do agents learn from prior run outcomes on the same repository, and how should that knowledge feed forward without violating the ephemeral sandbox invariant?

## The memory problem

Agents are stateless — each run starts with zero knowledge of prior attempts on the same repository. The sandbox is ephemeral by design: "created per-run, destroyed after extraction. No state carries between runs" (architecture doc). This is a sound security and isolation decision, but it has a compounding cost: agents rediscover the same lessons on every run.

If an agent repeatedly fails on a repo for a specific reason — "this repo requires running `make generate` after interface changes", "tests require a running database container", "the CI lint config enforces an import ordering the agent doesn't default to" — it burns through the same retry loops and escalates for the same reason across multiple issues. The knowledge exists (in transcripts, in retro agent proposals, in closed escalation issues) but is never automatically fed back to the next run.

## What exists today

Two mechanisms partially address this:

**Per-repo AGENTS.md / skills.** Humans (or the retro agent's proposals, once accepted) encode repo-specific knowledge as skills or agent instructions. This is the right long-term solution for stable, generalizable patterns. But it requires human intervention to convert a run outcome into a skill, and it doesn't capture transient or tactical information ("the last 3 runs on this repo all failed because the CI runner was misconfigured — don't retry lint failures until #142 is resolved").

**Retro agent ([§14 retro agent runtime](../architecture.md)).** Analyzes completed workflows and files improvement proposals as GitHub issues. Effective for systemic improvements, but the proposals enter the issue backlog and require human triage before they affect future runs. The latency is days to weeks, not run-to-run.

Neither mechanism provides **automatic, immediate feedback** from one run's outcome to the next run's context.

## A harness-mediated memory layer

The key constraint: memory must live outside the sandbox, injected by the harness layer — the same way skills and agent definitions are assembled today. The sandbox remains ephemeral. No inner layer reads from or writes to persistent state directly. The harness mediates all cross-run information.

### Record

After each run completes (success, failure, or escalation), the post-script writes a structured summary on the host. This runs after sandbox destruction, so no sandbox invariant is violated.

The summary includes:
- Outcome (success / failure / escalation / no-op)
- Failure reason, if any (lint failure, test failure, review rejection, timeout)
- Review feedback received (from the coordinator merge algorithm)
- Repo-specific patterns discovered ("had to run `make generate`", "test suite requires `docker compose up`")
- Issue number and agent role

### Store

Summaries are stored per-repo in the `.fullsend` config repo (e.g., `memory/<org>/<repo>.jsonl`), governed by the org's CODEOWNERS. This follows the existing configuration layering model: org-wide configuration lives in `.fullsend`, scoped per-repo.

Design constraints:
- **Per-repo scoping** — patterns learned from one codebase must not leak into unrelated repos
- **Bounded retention** — keep the last N runs (e.g., 20) to bound context size and prevent stale history from dominating
- **Append-only from post-scripts** — the memory file is written by post-scripts on the host, never by the agent inside the sandbox
- **Human-reviewable** — JSONL is human-readable and diffable, so CODEOWNERS can review what's being recorded

### Inject

On new runs, the pre-script fetches recent summaries for the target repo and injects them into the agent's context via the harness. Options:
- Generated skill file (e.g., `memory-context.md` assembled from recent summaries, placed alongside other skills)
- Environment variable (for short summaries)
- Harness template variable (if harness definitions support dynamic templating)

This follows the existing pattern: the harness assembles context on the host, the sandbox receives it read-only.

## What this is NOT

- **Not persistent sandbox state.** The sandbox remains ephemeral. Memory lives in the harness layer.
- **Not a replacement for skills/AGENTS.md.** Memory is tactical (recent run outcomes); skills are strategic (stable patterns). The retro agent should still propose skill additions for patterns that stabilize across multiple runs.
- **Not cross-repo.** Memory is strictly scoped per-repo. An org-wide "agents are bad at Go interface changes" insight belongs in an org-level skill, not in memory.
- **Not agent-writable during a run.** The agent cannot modify memory. Only post-scripts on the host can append to it.

## Relationship to other problem areas

- **Codebase context** — memory is a fourth context source alongside code, per-repo instructions, and org-level architecture docs. It follows the same principle: structured, minimal, injected by the harness.
- **Testing agents** — run outcomes are a form of eval signal. Memory recording could feed into the eval framework for measuring whether agents improve over time on a given repo.
- **Operational observability** — hemory summaries are a structured subset of the observability data already being collected (transcripts, workflow logs). The question is packaging it for agent consumption.
- **Agent architecture** — the retro agent and memory are complementary. Retro proposes systemic improvements requiring human approval. Memory provides immediate tactical context automatically. A mature system might have the retro agent *curate* the memory file, pruning stale entries and promoting stable patterns to skills.

## Open questions

- What is the right retention window? Too short and agents lose useful history; too long and stale context dominates. Should retention be time-based (last 30 days), count-based (last 20 runs), or outcome-based (keep all failures, prune successes)?
- Should the retro agent curate the memory file? It could prune resolved issues, promote stable patterns to skills, and flag contradictory entries.
- How does memory interact with the output schema (ADR 0022)? If agent output includes a structured result field, the post-script can extract richer summaries. Should the output schema require a "lessons learned" field?
- What is the privacy/security model? Memory summaries could contain information about code, test failures, or internal processes. Should they be encrypted, or is the `.fullsend` repo's access control sufficient?
- How does this interact with the multi-org deployment model? Each org's memory is fully independent (consistent with the "no shared control plane" principle), but should upstream fullsend provide a default memory schema?
