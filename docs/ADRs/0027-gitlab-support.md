---
title: "27. GitLab Support Architecture"
status: Proposed
relates_to:
  - agent-infrastructure
  - agent-architecture
topics:
  - gitlab
  - forge
  - ci-cd
  - multi-platform
---

# 27. GitLab Support Architecture

Date: 2026-04-29

## Status

Proposed

## Context

Fullsend currently supports GitHub exclusively, using GitHub-specific primitives throughout the agent pipeline:
- GitHub Actions workflows for CI/CD orchestration
- GitHub Apps with fine-grained per-role permissions for authentication
- `pull_request_target` trigger for secure event handling
- `workflow_dispatch` API for cross-repository workflow triggers
- GitHub labels as state machine
- Org-level Actions secrets with repository visibility controls

Organizations using GitLab (self-hosted or GitLab.com) cannot adopt fullsend. Adding GitLab support requires:
1. Mapping GitHub primitives to GitLab equivalents
2. Maintaining security properties (untrusted MR code cannot access secrets)
3. Preserving the same agent workflow (triage → code → review → fix)
4. Keeping the architecture parallel where possible to minimize divergence

The `forge.Client` abstraction (ADR-0005) was designed for this: all forge-specific operations are isolated, making GitLab support an implementation of the interface rather than a rewrite of core logic.

## Options

### Alternative 1: GitLab CI/CD Templates at Root

Instead of `.gitlab/ci/*.yml`, use single `.gitlab-ci.yml` with includes.

**Rejected**: Less organized than GitHub's `.github/workflows/` pattern, harder to scan for stage markers.

### Alternative 2: Group Access Tokens Instead of Project Access Tokens

Use group-level tokens for all roles instead of project-level.

**Rejected**: Less secure (group-wide permissions), harder to scope per-repo. Project Access Tokens better match GitHub Apps model.

### Alternative 3: Service Accounts with Personal Access Tokens

Create GitLab user accounts for each role (fullsend-triage, fullsend-code, etc.) and use their PATs.

**Rejected**: Requires managing user accounts, consumes user licenses, PATs are user-scoped not project-scoped. Project Access Tokens are purpose-built for automation.

### Alternative 4: Unified `.fullsend-ci.yml` Format

Define a forge-neutral CI/CD format that compiles to GitHub Actions or GitLab CI.

**Rejected**: Adds complexity, requires custom compiler, loses ability to use forge-native features. Better to maintain parallel templates that map proven GitHub patterns to GitLab.

## Decision

### High-Level Architecture

GitLab support mirrors the GitHub architecture where primitives map cleanly, and adapts where GitLab's model differs. The config repo convention remains `<group>/.fullsend` (GitLab groups are equivalent to GitHub orgs).

### 1. Directory Structure

**GitHub**: `.github/workflows/*.yml`
**GitLab**: `.gitlab/ci/*.yml`

GitLab allows organizing CI/CD files in subdirectories via `include:`. The `.fullsend` config repo uses:

```
.fullsend/
  .gitlab/
    ci/
      dispatch.yml          # Main dispatcher
      triage.yml           # fullsend-stage: triage
      code.yml             # fullsend-stage: code
      review.yml           # fullsend-stage: review
      fix.yml              # fullsend-stage: fix
  templates/
    shim-pipeline.yml      # Template for enrolled repos
```

**Rationale**: GitLab supports both `.gitlab-ci.yml` at root and `.gitlab/ci/*.yml` via includes. The subdirectory approach keeps the config repo organized and parallel to GitHub's `.github/workflows/` structure.

### 2. CI/CD Pipeline Architecture

**GitHub**: Workflows triggered by events (issues, pull_request_target, issue_comment, pull_request_review)
**GitLab**: Pipelines triggered by events (issues, merge_requests, notes) with `workflow:rules`

Each stage workflow (triage, code, review, fix) is a separate `.gitlab/ci/*.yml` file with a `# fullsend-stage: <name>` comment marker (same pattern as GitHub).

**Dispatch pattern**: The `dispatch.yml` pipeline:
1. Receives trigger API call from enrolled repos
2. Scans `.gitlab/ci/*.yml` files for `# fullsend-stage:` markers
3. Uses GitLab's downstream pipeline API to trigger matching stage pipelines
4. Passes event payload and context via pipeline variables

**Key difference from GitHub**: GitLab uses parent/child pipeline relationships and pipeline trigger tokens instead of `workflow_dispatch`. The dispatch pipeline triggers child pipelines via `trigger:` keyword or API calls.

### 3. Authentication Model

**GitHub**: Per-role GitHub Apps with fine-grained repository permissions
**GitLab**: Per-role Project Access Tokens with role-based permissions

GitLab doesn't have an exact GitHub Apps equivalent, but Project Access Tokens (PATs) provide similar functionality:
- Scoped to specific projects (not user-based)
- Support role-based permissions (Guest, Reporter, Developer, Maintainer)
- Can be created programmatically via GitLab API
- Expire after configurable period (max 1 year, renewable)

**Role mapping**:
| Role    | GitLab Permission | Capabilities |
|---------|------------------|--------------|
| fullsend (orchestrator) | Maintainer | Read/write .fullsend config repo, trigger pipelines, manage project access tokens |
| triage  | Reporter | Read target repos, comment on issues |
| code    | Developer | Read/write target repos, create MRs, push branches |
| review  | Developer | Read repos, create MR reviews/comments |
| fix     | Developer | Read/write target repos, push to MR branches |

**Storage**: Project Access Token values stored as CI/CD variables:
- Group-level masked variable: `FULLSEND_DISPATCH_TOKEN` (visible to all enrolled projects)
- Project-level masked variables in `.fullsend`:
  - `FULLSEND_TRIAGE_TOKEN`
  - `FULLSEND_CODE_TOKEN`
  - `FULLSEND_REVIEW_TOKEN`
  - `FULLSEND_FIX_TOKEN`

**Limitations vs GitHub Apps**:
- No installation flow (tokens created via API, no OAuth redirect)
- Less granular permissions (e.g., can't grant "issues:write but not code:write")
- Expiration requires rotation (GitHub App keys don't expire)
- No per-permission scoping within a role (e.g., Developer can push and approve, can't separate)

**Alternative considered**: OAuth Applications. Rejected because they're user-scoped (not project-scoped) and require user interaction, similar to GitHub App manifest flow but less suitable for automation.

### 4. Shim Pipeline Security

**GitHub**: `pull_request_target` ensures the shim workflow runs the base branch version, preventing untrusted PR code from modifying the workflow to exfiltrate secrets.

**GitLab**: No `pull_request_target` equivalent. The protected-branch pipeline approach (using `CI_COMMIT_REF_PROTECTED == "true"`) conflicts with MR-event triggering (which runs on unprotected MR source branches), so a different architecture is required.

**Webhook-based approach**: Instead of a shim pipeline in the enrolled repo, use GitLab webhooks to trigger `.fullsend` pipelines directly:

1. **Webhook configuration**: Enrolled repos configure webhooks that POST to `.fullsend` project's pipeline trigger endpoint
2. **Webhook authentication**: GitLab webhooks include a secret token, which `.fullsend` validates before processing
3. **Trigger pipeline on protected branch**: The webhook triggers a pipeline in `.fullsend` on the `main` branch (protected), not in the enrolled repo
4. **No untrusted code execution**: MR code never executes in a pipeline context — webhook payload is parsed by GitLab's webhook system, then triggers `.fullsend`

**Webhook payload validation** (in `.fullsend` dispatch pipeline):
```yaml
# fullsend-stage: dispatch
# Triggered by webhooks from enrolled repos

dispatch:
  stage: dispatch
  image: alpine:latest
  rules:
    # Only run on protected branch (main)
    - if: $CI_COMMIT_REF_PROTECTED == "true"
  script:
    - apk add --no-cache yq jq curl
    - |
      # Validate inputs and webhook token
      SOURCE_PROJECT="${SOURCE_PROJECT}"
      WEBHOOK_TOKEN="${WEBHOOK_TOKEN}"

      # Validate SOURCE_PROJECT format before using in variable lookup
      # NOTE: This regex should be expanded during implementation to include
      # dots (.) and nested groups, which are valid in GitLab project paths
      # (e.g., my.org/my.project or group/subgroup/project)
      if [[ ! "$SOURCE_PROJECT" =~ ^[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+$ ]]; then
        echo "ERROR: Invalid source project format"
        exit 1
      fi

      # Look up expected token from masked CI/CD variable
      # Variable name format: WEBHOOK_TOKEN_<sanitized_project_path>
      # NOTE: This sanitization can cause collisions (foo-bar/baz and foo_bar/baz
      # both map to WEBHOOK_TOKEN_foo_bar_baz). Implementation should use a
      # collision-resistant encoding (e.g., double-underscore for /, or hashing).
      SANITIZED_PROJECT=$(echo "${SOURCE_PROJECT}" | tr '/' '_' | tr '-' '_')
      EXPECTED_TOKEN_VAR="WEBHOOK_TOKEN_${SANITIZED_PROJECT}"
      EXPECTED_TOKEN="${!EXPECTED_TOKEN_VAR}"

      if [ -z "${EXPECTED_TOKEN}" ]; then
        echo "ERROR: No webhook token configured for ${SOURCE_PROJECT}"
        exit 1
      fi

      if [ "$WEBHOOK_TOKEN" != "$EXPECTED_TOKEN" ]; then
        echo "ERROR: Invalid webhook token"
        exit 1
      fi

      # Validate source project is enrolled
      PROJECT_NAME="${SOURCE_PROJECT##*/}"
      ENABLED=$(yq ".repos.\"$PROJECT_NAME\".enabled" config.yaml)
      if [ "$ENABLED" != "true" ]; then
        echo "ERROR: Project not enrolled"
        exit 1
      fi

      # Webhook payload will be base64-encoded before passing to child pipeline
      # to prevent YAML injection attacks via event content
```

**Enrollment setup**:
- `fullsend admin install` creates webhook in enrolled repo via GitLab API
- Webhook URL: `https://gitlab.com/api/v4/projects/<fullsend-project-id>/trigger/pipeline`
- Webhook triggers: Merge Request events, Issue events, Note events
- Webhook secret token: stored as masked CI/CD variable in `.fullsend` project (e.g., `WEBHOOK_TOKEN_myorg_myrepo`), validated by dispatch pipeline

**Security properties**:
- Webhook payload constructed by GitLab, not by MR author code
- Dispatch pipeline runs on `.fullsend` protected `main` branch
- Token validation prevents unauthorized repos from triggering workflows
- MR source code never executes in a pipeline with access to fullsend secrets

**Key difference from GitHub**: Webhooks replace the in-repo shim. This is architecturally cleaner for GitLab's security model but requires webhook management (creation, token rotation) in the admin install flow.

### 5. Cross-Repo Dispatch Mechanism

**End-to-end flow**: Enrolled repos send webhooks to `.fullsend` project's pipeline trigger endpoint → webhook triggers the dispatch pipeline on `.fullsend` protected `main` branch → dispatch pipeline validates the webhook token and source project → dispatch pipeline scans for stage workflows matching the requested stage → dispatch pipeline generates a child pipeline config and triggers it → child pipeline runs the stage workflow (triage, code, review, or fix) with the event payload and source project context.

**GitHub**: `workflow_dispatch` API call with input parameters
**GitLab**: Pipeline trigger API with pipeline variables + child pipelines

**Trigger token creation**:
- Created via GitLab API: `POST /projects/:id/triggers`
- Stored as group-level variable `FULLSEND_DISPATCH_TOKEN`
- Scoped to `.fullsend` project

**Dispatch workflow** (`.gitlab/ci/dispatch.yml`):
```yaml
# fullsend-stage: dispatch
# Dispatcher pipeline that fans out to stage pipelines via child pipelines
# Split into two jobs: generate-config creates the child pipeline config,
# trigger-stage triggers it as a downstream pipeline

workflow:
  rules:
    # Run dispatch logic only when not already in a child pipeline
    - if: $IS_CHILD_PIPELINE != "true"

generate-config:
  stage: prepare
  image: alpine:latest
  script:
    - apk add --no-cache yq jq
    - |
      # Validate and extract inputs
      STAGE="${STAGE}"
      SOURCE_PROJECT="${SOURCE_PROJECT}"

      # Validate SOURCE_PROJECT format before using in variable lookup
      # NOTE: This regex should be expanded during implementation to include
      # dots (.) and nested groups, which are valid in GitLab project paths
      # (e.g., my.org/my.project or group/subgroup/project)
      if [[ ! "$SOURCE_PROJECT" =~ ^[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+$ ]]; then
        echo "ERROR: Invalid source project format"
        exit 1
      fi

      # Validate source project is enrolled
      PROJECT_NAME="${SOURCE_PROJECT##*/}"
      ENABLED=$(yq ".repos.\"$PROJECT_NAME\".enabled" config.yaml)
      if [ "$ENABLED" != "true" ]; then
        echo "ERROR: Project not enrolled"
        exit 1
      fi

      # Scan for workflows with matching stage marker
      # NOTE: This assumes one file per stage (e.g., only one file has
      # "# fullsend-stage: triage"). If multiple files match, only the first
      # match is used. The architecture expects each stage workflow to be in
      # a separate file (triage.yml, code.yml, review.yml, fix.yml).
      MATCHED=false
      for pipeline_file in .gitlab/ci/*.yml; do
        STAGE_MARKER=$(grep -E '^# fullsend-stage:' "$pipeline_file" | head -1 | sed 's/^# fullsend-stage: *//')

        if [ "$STAGE_MARKER" = "$STAGE" ]; then
          echo "Generating child pipeline config for: $pipeline_file"
          # Create child pipeline config without injecting EVENT_PAYLOAD
          # Event payload passed via trigger API variables, not embedded in YAML
          cat > .gitlab-ci-child.yml <<'EOF'
      include:
        - local: '$pipeline_file'

      variables:
        IS_CHILD_PIPELINE: "true"
      EOF
          # Replace $pipeline_file placeholder
          sed -i "s|\$pipeline_file|$pipeline_file|g" .gitlab-ci-child.yml
          MATCHED=true
          break  # Exit after first match
        fi
      done

      if [ "$MATCHED" != "true" ]; then
        echo "ERROR: No workflow found for stage: $STAGE"
        exit 1
      fi
  artifacts:
    paths:
      - .gitlab-ci-child.yml
    expire_in: 1 hour
  rules:
    - if: $STAGE

trigger-stage:
  stage: deploy
  needs:
    - generate-config
  trigger:
    include:
      - artifact: .gitlab-ci-child.yml
        job: generate-config
    strategy: depend
  variables:
    # Pass event payload and source project via trigger variables (safe serialization)
    # Base64-encode EVENT_PAYLOAD to prevent YAML injection
    EVENT_PAYLOAD_B64: ${EVENT_PAYLOAD_B64}
    SOURCE_PROJECT: ${SOURCE_PROJECT}
  rules:
    - if: $STAGE
```

**Two-job pattern**: GitLab CI requires separating config generation (`script:`) from pipeline triggering (`trigger:`). The `generate-config` job creates the child pipeline YAML as an artifact without embedding untrusted event content. The `trigger-stage` bridge job then triggers the child pipeline, passing the event payload safely via base64-encoded variables. This prevents YAML injection attacks where attacker-controlled event content (issue titles, MR descriptions) could break out of the `variables:` block and inject arbitrary pipeline configuration.

**Child pipeline approach**: Uses GitLab's `trigger: include: artifact:` pattern to create child pipelines. The `IS_CHILD_PIPELINE` variable prevents the dispatch workflow from running recursively.

### 6. Stage Markers

**Pattern**: Same `# fullsend-stage: <name>` comment convention as GitHub
**Location**: Top of `.gitlab/ci/*.yml` files

This keeps the dispatch scanning logic identical across GitHub and GitLab.

### 7. Event Mapping

| GitHub Event | GitLab Event | Trigger Mechanism |
|-------------|--------------|-------------------|
| issues.labeled | issue (labels changed) | Webhook → .fullsend dispatch pipeline |
| issue_comment.created | note (on issue) | Webhook → .fullsend dispatch pipeline |
| pull_request_target | merge_request_event | Webhook → .fullsend dispatch pipeline |
| pull_request_review.submitted | Merge request approval | Webhook → .fullsend dispatch pipeline |

**GitLab webhook limitations**:
- No direct equivalent to GitHub's granular event types
- Must filter events in pipeline logic (e.g., check `$CI_MERGE_REQUEST_EVENT_TYPE`)
- Issue webhooks don't include label details in all cases (may need API call)

### 8. State Machine Primitives

**Labels**: GitLab labels work nearly identically to GitHub
- Same label names: `ready-to-code`, `ready-for-review`, `ready-for-merge`, `needs-info`
- Applied via GitLab API: `PUT /projects/:id/merge_requests/:iid/labels`
- Scoped to projects (not group-level by default)

**Approval rules**: GitLab has native approval mechanisms:
- Required approvals count
- Code owners approvals (similar to GitHub CODEOWNERS)
- Can integrate with fullsend review stage

### 9. Implementation Phases

**Phase 1: Forge abstraction**
- Implement `internal/forge/gitlab/gitlab.go` with `forge.Client` interface
- Add GitLab API client (use `go-gitlab` library)
- Implement equivalent methods for repos, MRs, labels, CI/CD variables, pipeline triggers

**Phase 2: CI/CD templates**
- Create `.gitlab/ci/dispatch.yml` dispatcher
- Create stage pipelines (triage.yml, code.yml, review.yml, fix.yml)
- Create `templates/shim-pipeline.yml` template
- Add scripts for GitLab-specific operations (parallel to `.github/scripts/`)

**Phase 3: CLI updates**
- Detect forge type (GitHub vs GitLab) from repo URL or config
- Add `--forge` flag to `fullsend admin install`
- Update `appsetup` package to create Project Access Tokens instead of GitHub Apps
- Update `layers` package to deploy `.gitlab/` directory instead of `.github/`
- Add GitLab-specific enrollment (include shim in `.gitlab-ci.yml`)

**Phase 4: Configuration**
- Add `forge: github` or `forge: gitlab` to `config.yaml`
- Support forge-specific settings (GitLab instance URL for self-hosted)
- Update config schema and validation

**Phase 5: Testing**
- E2E tests against GitLab.com or GitLab test instance
- Parallel GitHub/GitLab test suite
- Migration testing (GitHub → GitLab config equivalence)

## Consequences

### Positive

- **Multi-forge support**: Organizations on GitLab can adopt fullsend
- **Forge abstraction validated**: Implementing GitLab proves the `forge.Client` abstraction works
- **Parallel architecture**: GitLab implementation closely mirrors GitHub, reducing cognitive load
- **Same workflow**: Triage → Code → Review → Fix stages work identically from user perspective
- **Minimal CLI changes**: Forge detection mostly automatic, users specify `--forge` only during install

### Negative

- **Increased maintenance**: Two CI/CD template sets to maintain (`.github/` and `.gitlab/`)
- **Authentication complexity**: Project Access Tokens less capable than GitHub Apps, require rotation
- **Security model differences**: No `pull_request_target` equivalent requires careful protected branch configuration
- **Feature parity gaps**: Some GitHub features may not map perfectly (e.g., fine-grained permissions)
- **Testing overhead**: Need GitLab instance for E2E tests (self-hosted or GitLab.com)

### Risks

- **Protected branch misconfiguration**: If GitLab project doesn't protect `main`, MR authors could modify shim
- **Token expiration**: Project Access Tokens expire (max 1 year), need renewal automation
- **API rate limits**: GitLab.com has lower rate limits than GitHub, may need request throttling
- **Self-hosted GitLab versions**: Wide version range, feature availability varies

### Mitigations

- **Validation during install**: CLI checks that target branch is protected before enrolling repos
- **Token expiration monitoring**: Warn 30 days before expiration, provide renewal command
- **Rate limit handling**: Exponential backoff + retry in GitLab client
- **Version detection**: CLI detects GitLab version, warns about unsupported versions

## Implementation Notes

### CLI Changes Required

**Forge detection**:
```go
// internal/cli/admin.go
func detectForge(repoURL string) (string, error) {
    u, err := url.Parse(repoURL)
    if err != nil {
        return "", fmt.Errorf("invalid repo URL: %w", err)
    }
    host := strings.ToLower(u.Hostname())

    // Exact domain matching for known forges
    if host == "github.com" || strings.HasSuffix(host, ".github.com") {
        return "github", nil
    }
    if host == "gitlab.com" || strings.HasSuffix(host, ".gitlab.com") {
        return "gitlab", nil
    }

    // For self-hosted instances, require explicit --forge flag
    return "", fmt.Errorf("unknown forge: %s (use --forge flag for self-hosted instances)", repoURL)
}
```

**Install command changes**:
- Add `--forge {github|gitlab}` flag (auto-detected if not specified)
- Add `--gitlab-url` for self-hosted GitLab instances
- Update app setup flow to create Project Access Tokens for GitLab
- Update workflows layer to deploy `.gitlab/` instead of `.github/`

**Config schema changes**:
```yaml
# config.yaml
forge: gitlab  # or "github"
gitlab_instance_url: https://gitlab.example.com  # optional, defaults to gitlab.com
```

**New packages**:
- `internal/forge/gitlab/` - GitLab client implementation
- `internal/scaffold/fullsend-repo/.gitlab/` - GitLab CI/CD templates
- `internal/scaffold/fullsend-repo/.gitlab/scripts/` - GitLab-specific scripts

**Modified packages**:
- `internal/cli/admin.go` - Forge detection, conditional logic
- `internal/layers/workflows.go` - Deploy `.gitlab/` or `.github/` based on forge
- `internal/layers/enrollment.go` - GitLab enrollment uses `.gitlab-ci.yml` include
- `internal/appsetup/` - Create Project Access Tokens for GitLab
- `internal/config/config.go` - Add forge type field

### Security Considerations

**Protected branch requirement**:
- Must be enforced before enrollment
- CLI validates via GitLab API: `GET /projects/:id/protected_branches/:branch`
- Error if `main` is not protected

**Token scoping**:
- Project Access Tokens scoped to specific projects, not group-wide
- Separate token per enrolled project for code/review/fix roles
- Dispatch token is group-level variable but only triggers `.fullsend` project

**Webhook authenticity**:
- GitLab webhooks include secret token for verification
- Dispatch pipeline validates webhook token against config.yaml before processing
- Invalid tokens result in immediate pipeline failure

**MR source checkout prevention**:
- Webhook-based architecture eliminates MR code execution risk
- Dispatch pipeline runs on `.fullsend` protected branch, not enrolled repo
- MR metadata passed via webhook payload, constructed by GitLab (not MR author)

## References

- ADR-0005: Forge abstraction layer
- ADR-0007: Per-role GitHub Apps (authentication model to replicate)
- ADR-0008: workflow_dispatch for cross-repo dispatch (pattern to replicate with triggers)
- ADR-0009: pull_request_target security model (challenge to solve)
- GitLab CI/CD documentation: https://docs.gitlab.com/ee/ci/
- GitLab Project Access Tokens: https://docs.gitlab.com/ee/user/project/settings/project_access_tokens.html
- GitLab Pipeline Triggers: https://docs.gitlab.com/ee/ci/triggers/
