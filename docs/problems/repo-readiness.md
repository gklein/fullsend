# Repo Readiness

What's the current state of test coverage and CI maturity across konflux-ci, and what needs to improve before agents can be trusted?

## Current state (as of March 2026)

Data from the [coverage dashboard](https://konflux-ci.dev/coverage-dashboard/):

### No coverage data (12 repos)

application-api, build-trusted-artifacts, ci-helper-app, coverport, devlake, dockerfile-json, kargo, konflux-ci, may, namespace-generator, renovate-log-analyzer, test-data-sast

### Coverage by tier

**Strong (>75%)**
| Repo | Coverage |
|---|---|
| segment-bridge | 90.8% |
| release-service | 87.5% |
| notification-service | 85.0% |
| repository-validator | 82.4% |
| internal-services | 78.6% |
| sprayproxy | 78.5% |
| image-rbac-proxy | 78.0% |

**Moderate (50-75%)**
| Repo | Coverage |
|---|---|
| image-controller | 71.1% |
| integration-service | 68.4% |
| caching | 62.2% |
| project-controller | 59.3% |
| smee-sidecar | 58.4% |
| operator-toolkit | 56.7% |
| tekton-kueue | 56.1% |
| multi-platform-controller | 53.6% |
| build-service | 52.6% |

**Thin (<50%)**
| Repo | Coverage |
|---|---|
| mintmaker | 45.1% |
| workspace-manager | 45.1% |
| coverage-dashboard | 34.6% |
| qe-tools | 32.3% |
| tektor | 30.5% |
| kueue-external-admission | 12.3% |

## Readiness beyond test coverage

Test coverage is necessary but not sufficient. For agents to merge autonomously, repos also need:

- **Integration and e2e tests** — unit tests catch local bugs; integration tests catch system-level regressions
- **Linting and formatting in CI** — prevents agent style drift
- **Clear CI signals** — tests must be reliable (no flaky tests that train agents to ignore failures)
- **CLAUDE.md or equivalent** — agents need codebase context to work effectively (see [codebase-context.md](codebase-context.md) for what makes context files effective)
- **CODEOWNERS** — defines the human-required approval paths
- **Language properties** — type safety, tooling ecosystem, and deployment simplicity affect how effectively agents can operate (see [agent-compatible-code.md](agent-compatible-code.md))

## Diagnostic tooling

[agentready](https://github.com/ambient-code/agentready) can assess repos against research-backed criteria for AI-assisted development readiness. Recommended as a diagnostic step to generate a baseline readiness assessment across the org, but not a dependency for the agentic system itself.

## Open questions

- What's the minimum coverage threshold for agent autonomy? Is it per-repo or per-package?
- Should agents help improve test coverage as a prerequisite to their own autonomy? (Chicken-and-egg: agents could write tests, but we need tests to trust agents.)
- How do we handle repos with significant package-level variance? (e.g., build-service: controller at 78.6%, git/github at 0%)
- Are there repos that should never be autonomous regardless of coverage? (e.g., security-critical infrastructure)
- How do we handle flaky tests? They erode confidence in the CI signal that agents rely on.
