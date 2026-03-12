# Experiment 002: Claude-based ADR Drift Scanner — Design Spec

**Date:** 2026-03-12

## Problem

Experiment 001 built a Python script that detects ADR-0046 drift by comparing image strings against an allowlist. It works, but it only proves that string matching works. The fullsend vision is that agents can enforce architectural invariants in general — including ADRs that express design philosophy, preferred patterns, or intent that can't be reduced to field comparisons. Experiment 001 short-circuited that question.

## Goal

Validate that an LLM (via the `claude` CLI) can read an ADR, understand its architectural intent, evaluate a code artifact against it, and produce a useful analysis — including what's wrong, why it violates the ADR, and what should be done to fix it. No hardcoded rules, no config files, no parsing logic.

## Architecture

A shell script (`scan.sh`) that:

1. Takes two arguments: an ADR source (URL or local file path) and a Tekton task YAML file path
2. If the ADR source is a URL, fetches it; if a local path, reads it
3. Constructs a prompt combining the ADR text and the task YAML content
4. Invokes `claude` with that prompt, asking it to analyze the task for ADR violations
5. Writes claude's prose analysis to stdout

All intelligence lives in claude's comprehension of the ADR. The script is a thin wrapper — no YAML parsing, no image comparison, no domain logic.

## Output format

Claude produces a prose report covering, for each violation found:

- **What** — which step violates the ADR and what image it currently uses
- **Why** — what the ADR requires and why this step doesn't comply
- **Fix** — what should be done to bring the step into compliance (e.g., swap the image, add a tool to the task runner first, or note if a legitimate exemption applies)

## Validation approach

We write a hand-crafted **expected output** before running the scanner. This is our own human analysis of what violations exist in the `modelcar-oci-ta` task against ADR-0046 and what should be done about each one. After running claude, we compare its output against our expectations in the experiment log, noting agreement, disagreement, and anything claude surfaced that we didn't anticipate.

The comparison target is human judgment, not the Python script from Experiment 001.

## File structure

```
experiments/adr46-claude-scanner/
  scan.sh                          # The shell script
  expected/
    modelcar-oci-ta.md             # Hand-written expected analysis
  results/
    modelcar-oci-ta.md             # Claude's actual output (committed after running)
```

The experiment log lives at `docs/experiments/002-adr46-claude-scanner.md`.

## What this proves (or disproves)

- Can claude read an ADR and correctly identify violations without any hardcoded rules?
- Does claude's reasoning match human judgment about why something is a violation?
- Does claude produce actionable fix recommendations?
- Does the approach generalize — could you swap in a different ADR and a different code artifact and get useful results without changing the script?

## Out of scope

- Running against the full build-definitions repo (batch scanning)
- Filing GitHub issues automatically
- Generating fix PRs
- Comparing performance or accuracy against the Python scanner
