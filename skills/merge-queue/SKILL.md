---
name: merge-queue
description: >
  Use when you need to add a PR to a GitHub merge queue. The gh CLI has no
  built-in merge-queue command, so this skill provides a script that uses the
  GraphQL API.
allowed-tools: Bash(bash skills/merge-queue/scripts/enqueue-pr.sh:*)
---

Run `bash skills/merge-queue/scripts/enqueue-pr.sh [PR_NUMBER_OR_URL]` to enqueue a PR.

Omit the argument to enqueue the current branch's PR.
