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

**Why webhooks for GitLab but not GitHub**: ADR-0009 (pull_request_target security model for GitHub) explicitly rejected webhook-based dispatch because it "requires a hosted webhook receiver, breaking compute-platform agnosticism." GitLab's situation is similar but with a critical difference:

**Webhook-to-trigger API incompatibility**: GitLab webhooks send JSON event payloads (merge request objects, issue events), while the pipeline trigger API (`/api/v4/projects/:id/trigger/pipeline`) expects form-encoded parameters (`token`, `ref`, `variables[KEY]=value`). These are not wire-compatible — pointing a webhook URL directly at the trigger endpoint results in a malformed request. This means an intermediary is required to translate webhook payloads to trigger API calls.

**Options for webhook translation**:
1. **GitLab CI/CD webhook integration**: Use a lightweight `.gitlab-ci.yml` job in the enrolled repo that runs on webhook events (via GitLab's `CI_PIPELINE_SOURCE == "webhook"` trigger) and calls the `.fullsend` trigger API. This keeps everything within GitLab CI/CD but **does not solve the security model** — enforcing protected-branch-only execution via `workflow:rules` prevents the pipeline from reacting to merge request events (which occur on unprotected branches), defeating the purpose. Without protected-branch enforcement, MR code can modify the webhook job.
2. **GitLab serverless functions**: Use GitLab's serverless integration to deploy a function that receives webhooks and translates to trigger API calls. Maintains compute-platform agnosticism (runs within GitLab infrastructure) but requires GitLab Premium/Ultimate tier.
3. **Minimal bridge service**: Deploy a lightweight translation service (e.g., Cloud Run, Lambda) that receives webhooks and POSTs to the trigger API. This reintroduces the "hosted webhook receiver" concern from ADR-0009 but may be acceptable given GitLab's lack of a direct webhook-to-pipeline primitive.

**Open question**: The webhook-to-trigger translation requirement creates an architectural tension. Options 2 and 3 both introduce additional infrastructure (serverless functions or hosted bridge), while option 1 reintroduces the security concern that webhooks were meant to solve. This ADR does not prescribe a solution — the implementation choice depends on the acceptable security/infrastructure trade-off. For GitLab Free tier deployments, option 3 (minimal bridge) is likely the only viable path. For Premium/Ultimate, option 2 (serverless) keeps compute within GitLab infrastructure. See "Open Questions" section for full analysis.

**Webhook payload validation** (in `.fullsend` dispatch pipeline):

This snippet illustrates the security-critical validation logic. For the complete dispatch pipeline including stage fan-out, see Section 5 (Cross-Repo Dispatch Mechanism).

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
    - apk add --no-cache yq jq curl bash
    # NOTE: The script below uses bash for ${!VAR} indirect expansion. Alpine's
    # default /bin/sh (ash) does not support this bashism. Use 'bash -c' or
    # switch to 'eval' / 'printenv' for POSIX compatibility.
    - bash <<'BASH'
      # Validate inputs and webhook token
      SOURCE_PROJECT="${SOURCE_PROJECT}"
      WEBHOOK_TOKEN="${WEBHOOK_TOKEN}"

      # Validate SOURCE_PROJECT format before using in variable lookup
      # GitLab project paths support dots (.), nested groups (group/subgroup/project),
      # and standard word characters. Expanded regex for production use:
      # ^[a-zA-Z0-9._-]+(/[a-zA-Z0-9._-]+)+$
      # This illustrative code uses simplified two-segment validation for clarity.
      # Production implementations should use the expanded regex above and update
      # the yq query to use bracket notation for project names containing dots
      # (e.g., yq '.["repos"]["my.project"]["enabled"]').
      if [[ ! "$SOURCE_PROJECT" =~ ^[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+$ ]]; then
        echo "ERROR: Invalid source project format"
        exit 1
      fi

      # Look up expected token from masked CI/CD variable
      # Variable name format: WEBHOOK_TOKEN_<sanitized_project_path>
      # Token variable naming: Use SHA256 hash of project path for collision-free
      # encoding (GitLab var names must match [A-Za-z_][A-Za-z0-9_]*).
      # Alternative sanitization (/ → __, - → _H_) is collision-prone for
      # pathological names like "foo_H_bar/baz" vs "foo-bar/baz".
      # Examples:
      #   myorg/myrepo → WEBHOOK_TOKEN_<sha256_hash>
      #   my-org/my-repo → WEBHOOK_TOKEN_<different_sha256_hash> (distinct)
      PROJECT_HASH=$(echo -n "${SOURCE_PROJECT}" | sha256sum | cut -d' ' -f1)
      EXPECTED_TOKEN_VAR="WEBHOOK_TOKEN_${PROJECT_HASH}"
      EXPECTED_TOKEN="${!EXPECTED_TOKEN_VAR}"

      if [ -z "${EXPECTED_TOKEN}" ]; then
        echo "ERROR: No webhook token configured for ${SOURCE_PROJECT}"
        exit 1
      fi

      # SECURITY: Implementations MUST use constant-time comparison to prevent
      # timing side-channel attacks that could leak the token byte-by-byte.
      # Example: echo -n "$WEBHOOK_TOKEN" | openssl dgst -sha256 -hex vs
      #          echo -n "$EXPECTED_TOKEN" | openssl dgst -sha256 -hex
      # This illustrative code uses string equality for clarity; production
      # code must replace this with constant-time comparison.
      if [ "$WEBHOOK_TOKEN" != "$EXPECTED_TOKEN" ]; then
        echo "ERROR: Invalid webhook token"
        exit 1
      fi

      # Validate source project is enrolled
      # Use full project path to avoid collisions (group1/myproject vs group2/myproject)
      # For nested groups (group/subgroup/project), config.yaml uses dot-separated keys
      # (e.g., repos."group.subgroup.project".enabled)
      CONFIG_KEY=$(echo "${SOURCE_PROJECT}" | tr '/' '.')
      ENABLED=$(yq ".repos.\"${CONFIG_KEY}\".enabled" config.yaml)
      if [ "$ENABLED" != "true" ]; then
        echo "ERROR: Project not enrolled: ${SOURCE_PROJECT}"
        exit 1
      fi

      # Webhook payload will be base64-encoded before passing to child pipeline
      # to prevent YAML injection attacks via event content
      BASH
```

**Enrollment setup**:
- `fullsend admin install` creates webhook in enrolled repo via GitLab API
- Webhook URL: Points to the translation intermediary (serverless function or bridge service) that forwards to `.fullsend` trigger API. See "Webhook-to-trigger API incompatibility" above for translation options.
- Webhook triggers: Merge Request events, Issue events, Note events
- Webhook secret token: stored as masked CI/CD variable in `.fullsend` project (variable name: `WEBHOOK_TOKEN_<sha256(project_path)>` for collision-free project identification), validated by dispatch pipeline after translation

**Security properties**:
- Webhook payload constructed by GitLab, not by MR author code
- Dispatch pipeline runs on `.fullsend` protected `main` branch
- Token validation prevents unauthorized repos from triggering workflows (implementations MUST use constant-time comparison to prevent timing side-channel attacks)
- Webhook token variable names use SHA256 hashing for collision-free project identification (`WEBHOOK_TOKEN_<sha256(project_path)>`)
- MR source code never executes in a pipeline with access to fullsend secrets

**Key difference from GitHub**: Webhooks replace the in-repo shim. This is architecturally cleaner for GitLab's security model but requires webhook management (creation, token rotation) in the admin install flow.

### 5. Cross-Repo Dispatch Mechanism

**End-to-end flow**: Enrolled repos send webhooks to `.fullsend` project's pipeline trigger endpoint → webhook triggers the dispatch pipeline on `.fullsend` protected `main` branch → dispatch pipeline validates the webhook token and source project → dispatch pipeline scans for stage workflows matching the requested stage → dispatch pipeline generates a child pipeline config and triggers it → child pipeline runs the stage workflow (triage, code, review, or fix) with the event payload and source project context.

**Relationship to Section 4**: Section 4 (Shim Pipeline Security) presents the webhook validation portion of the dispatch pipeline, focusing on the security properties (protected branch execution, token validation). This section presents the complete dispatch pipeline architecture, including the stage fan-out logic. Both sections describe the same `dispatch.yml` pipeline — Section 4 shows the validation front-end, this section shows the full implementation including child pipeline generation.

**GitHub**: `workflow_dispatch` API call with input parameters
**GitLab**: Pipeline trigger API with pipeline variables + child pipelines

**Trigger token creation**:
- Created via GitLab API: `POST /projects/:id/triggers` for the `.fullsend` project
- The token itself authorizes triggering pipelines only in the `.fullsend` project
- Stored as group-level CI/CD variable `FULLSEND_DISPATCH_TOKEN` (group-level for visibility to all enrolled repos' shim workflows; the token's authorization scope is still limited to `.fullsend`)

**Security consideration - dispatch token exposure**: The webhook-based architecture (chosen approach, see Section 4) avoids exposing `FULLSEND_DISPATCH_TOKEN` to enrolled repos entirely. Enrolled repos send webhooks with webhook secret tokens for authentication, and the `.fullsend` dispatch pipeline uses its internal `FULLSEND_DISPATCH_TOKEN` to trigger child pipelines — enrolled repo code never sees the dispatch token. This is a key security advantage of the webhook approach over alternatives like in-repo shim workflows. 

**Alternative architecture note**: If using in-repo shim workflows (not the chosen approach), the group-level `FULLSEND_DISPATCH_TOKEN` would be visible to all enrolled repo pipelines, creating an exfiltration risk. Mitigations for that alternative would include: (1) marking `FULLSEND_DISPATCH_TOKEN` as a protected variable (only exposed to protected branch pipelines), or (2) using webhook secret tokens for authentication instead. However, the webhook-based architecture already implements (2) by design, so this exposure concern does not apply to the chosen architecture.

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
    - apk add --no-cache yq jq gettext bash
    # NOTE: The script below uses bash for [[ regex ]] syntax. Alpine's default
    # /bin/sh (ash) does not support this bashism.
    - bash <<'BASH'
      # Validate and extract inputs
      # NOTE: The validation logic below (SOURCE_PROJECT format, enrollment
      # check) is duplicated from Section 4's security-focused snippet. Both
      # sections describe the same dispatch.yml pipeline. See Section 4 for the
      # security rationale behind this validation.
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
      # Use full project path to avoid collisions (group1/myproject vs group2/myproject)
      # For nested groups (group/subgroup/project), config.yaml uses dot-separated keys
      # (e.g., repos."group.subgroup.project".enabled)
      CONFIG_KEY=$(echo "${SOURCE_PROJECT}" | tr '/' '.')
      ENABLED=$(yq ".repos.\"${CONFIG_KEY}\".enabled" config.yaml)
      if [ "$ENABLED" != "true" ]; then
        echo "ERROR: Project not enrolled: ${SOURCE_PROJECT}"
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
          # NOTE: include:local: resolves files relative to the same repository
          # and ref as the parent pipeline. Since the dispatch pipeline runs on
          # .fullsend's protected main branch, the child pipeline includes stage
          # files (triage.yml, code.yml, etc.) from the same protected ref.
          # Use envsubst for robust variable substitution
          # Stage files are constrained to .gitlab/ci/*.yml by the loop,
          # so $pipeline_file is safe for substitution (no shell metacharacters).
          export pipeline_file
          cat > .gitlab-ci-child.yml <<'EOF'
      include:
        - local: '$pipeline_file'

      variables:
        IS_CHILD_PIPELINE: "true"
      EOF
          # envsubst replaces $pipeline_file with its value from environment
          # Restrict substitution to $pipeline_file only (not all env vars)
          envsubst '$pipeline_file' < .gitlab-ci-child.yml > .gitlab-ci-child.yml.tmp
          mv .gitlab-ci-child.yml.tmp .gitlab-ci-child.yml
          MATCHED=true
          break  # Exit after first match
        fi
      done

      if [ "$MATCHED" != "true" ]; then
        echo "ERROR: No workflow found for stage: $STAGE"
        exit 1
      fi
      BASH
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

**Phase 3: Forge-neutral interface evolution**
- Add forge-neutral methods to `forge.Client` (`CreateRoleCredential`, `TriggerPipeline`, `CreateWebhook`)
- Implement GitLab-specific versions of these methods in `internal/forge/gitlab/`
- Update `appsetup` to use `CreateRoleCredential()` instead of GitHub App-specific code
- Update `layers` to ask forge.Client for template paths and enrollment snippets (pushes forge-specific logic into Client implementations)
- Move forge detection to `internal/forge/detect.go` per ADR-0005 boundary rule
- Add `--forge` flag to `fullsend admin install` for manual override

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
- **Forge abstraction strengthened**: Implementing GitLab reveals areas where the interface needs to evolve (credential management, pipeline triggering) and validates that forge-specific operations can be pushed into the Client implementation per ADR-0005
- **ADR-0005 compliance**: Changes to layers/CLI/appsetup are minimized by adding forge-neutral interface methods (`CreateRoleCredential`, `TriggerPipeline`) rather than adding conditional logic
- **Parallel architecture**: GitLab implementation closely mirrors GitHub, reducing cognitive load
- **Same workflow**: Triage → Code → Review → Fix stages work identically from user perspective

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

### Forge Interface Evolution

**Challenge**: ADR-0005 promises "Adding a new forge requires implementing `forge.Client` — no changes to layers, CLI, or app setup code." However, the current `forge.Client` interface contains GitHub-specific methods (`ListOrgInstallations`, `GetAppClientID`) and operations (`DispatchWorkflow`) that don't map directly to GitLab.

**Proposed forge-neutral interface additions**:

These methods follow ADR-0005's forge-neutral vocabulary convention (e.g., `ChangeProposal` instead of "pull request" or "merge request"). The term `RoleCredential` is the forge-neutral abstraction for GitHub Apps (GitHub) and Project Access Tokens (GitLab).

```go
// Credential management (replaces GitHub App-specific methods)
// CreateRoleCredential creates a scoped credential for a specific role
// (triage, code, review, fix). For GitHub, this would create/configure
// a GitHub App. For GitLab, this would create a Project Access Token.
// The forge-neutral term "RoleCredential" abstracts over forge-specific
// authentication mechanisms.
CreateRoleCredential(ctx context.Context, role, owner, repo string, permissions []string) (credentialID string, err error)

// RevokeRoleCredential removes a previously created role credential.
RevokeRoleCredential(ctx context.Context, owner, repo, credentialID string) error

// GetRoleCredentialValue retrieves the secret value for a role credential
// (for storing in CI/CD secrets). For GitHub Apps, this generates an
// installation token. For GitLab PATs, this returns the token value.
GetRoleCredentialValue(ctx context.Context, owner, repo, credentialID string) (string, error)

// Pipeline/workflow triggering (replaces DispatchWorkflow)
// TriggerPipeline initiates a CI/CD pipeline for a specific stage.
// For GitHub, this calls workflow_dispatch. For GitLab, this uses the
// pipeline trigger API with variables.
TriggerPipeline(ctx context.Context, owner, repo, stage string, variables map[string]string) error

// Webhook management (GitLab-specific for security model)
// CreateWebhook configures a webhook from source repo to .fullsend project.
// For GitHub, this is a no-op (uses in-repo shim). For GitLab, this
// creates a project webhook with a secret token.
CreateWebhook(ctx context.Context, owner, repo, targetURL, secretToken string, events []string) error
DeleteWebhook(ctx context.Context, owner, repo, webhookID string) error
```

**Existing GitHub-specific methods to be generalized or deprecated**:

- `ListOrgInstallations(ctx, org) ([]Installation, error)` — GitHub App-specific. GitLab equivalent would list Project Access Tokens, but tokens are scoped per-project not org-wide. This method may need to become forge-specific or return an empty list for non-GitHub forges.
- `GetAppClientID(ctx, slug) (string, error)` — GitHub App-specific. No GitLab equivalent. This should be deprecated or moved to a GitHub-specific extension interface.
- `DispatchWorkflow(ctx, owner, repo, workflowFile, ref, inputs)` — GitHub Actions-specific (targets a specific workflow file). Replaced by forge-neutral `TriggerPipeline` above.

**Backward compatibility and migration strategy**:

To prevent interface bloat while maintaining backward compatibility:

1. **Deprecation phase**: Mark GitHub-specific methods with deprecation comments and update callers to use forge-neutral equivalents (`TriggerPipeline` instead of `DispatchWorkflow`, etc.). This phase allows gradual migration without breaking existing code.
2. **Extension interfaces**: Move forge-specific methods that have no neutral equivalent (e.g., `GetAppClientID`) to optional extension interfaces:
   ```go
   type GitHubForgeClient interface {
       Client
       GetAppClientID(ctx context.Context, slug string) (string, error)
   }
   ```
   Callers that need GitHub-specific behavior can type-assert to the extension interface.
3. **Breaking change timeline**: After all internal callers migrate to forge-neutral methods, remove deprecated methods in a major version bump. Document this timeline in the interface godoc (e.g., "deprecated: use TriggerPipeline, will be removed in v2.0.0").

This strategy limits interface growth to forge-neutral primitives while preserving GitHub-specific functionality via opt-in extension interfaces.

**Minimizing layer/CLI/appsetup changes**:

By adding the forge-neutral methods above, the implementation phases can be revised to keep layer/CLI changes minimal:

- **appsetup**: Use `CreateRoleCredential` instead of directly creating GitHub Apps or Project Access Tokens. The forge implementation handles the forge-specific details.
- **layers/workflows**: Use forge-agnostic template deployment (the forge.Client implementation knows whether to deploy `.github/` or `.gitlab/` based on forge type).
- **CLI**: Forge detection (`detectForge`) moves to `internal/forge/detect.go` per ADR-0005's boundary rule. CLI calls `forge.DetectForge(repoURL)` instead of implementing detection itself.

**Note on interface design scope**: This ADR proposes the architectural direction for forge-neutral interface evolution (add `CreateRoleCredential`, `TriggerPipeline`, etc.) to uphold ADR-0005's promise of minimal layer/CLI changes. The detailed API signatures, error handling, and return types for these methods require separate design work and should be documented in a follow-up design document or implementation PR. The exact method signatures shown above are illustrative, not normative.

### CLI Changes Required

**Forge detection**:
```go
// NOTE: Per ADR-0005's boundary rule ("No code outside internal/forge/ imports
// forge-specific packages directly"), this function should be implemented in
// internal/forge/detect.go rather than internal/cli/admin.go, and called by the
// CLI. The location shown here (internal/cli/admin.go) is for illustration only.
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

**Modified packages** (minimized via forge.Client abstraction):
- `internal/forge/forge.go` - Add forge-neutral methods (`CreateRoleCredential`, `TriggerPipeline`, `CreateWebhook`) and deprecate GitHub-specific methods
- `internal/forge/detect.go` (new) - Forge detection logic (moved from CLI per ADR-0005 boundary rule)
- `internal/config/config.go` - Add `forge` field to config schema (minimal change: one field addition)
- `internal/appsetup/` - Use `forge.Client.CreateRoleCredential()` instead of GitHub App-specific code (forge-agnostic caller, forge-specific implementation)
- `internal/layers/workflows.go` - Ask forge.Client for template directory path instead of conditionally choosing `.github/` or `.gitlab/` (pushes forge-specific logic into Client implementation)
- `internal/layers/enrollment.go` - Ask forge.Client for enrollment snippet instead of hardcoding shim workflow syntax (pushes forge-specific logic into Client implementation)

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

## Open Questions

### Webhook-to-Trigger Translation Architecture

**Problem**: GitLab webhooks (JSON payloads) and the pipeline trigger API (form-encoded parameters) are not wire-compatible. An intermediary is required to translate webhook events to trigger API calls.

**Trade-offs**:
- **Option 1 (CI/CD webhook integration)**: Runs in enrolled repo, but cannot enforce protected-branch-only execution without blocking MR reactions entirely. Reintroduces security concern.
- **Option 2 (GitLab serverless functions)**: Keeps compute within GitLab infrastructure, but requires GitLab Premium/Ultimate tier.
- **Option 3 (Minimal bridge service)**: Works on GitLab Free tier, but reintroduces hosted webhook receiver concern from ADR-0009.

**Decision needed**: Choose between infrastructure cost (options 2/3) and security model compromise (option 1). For GitLab Free tier, option 3 appears to be the only viable path. This question should be resolved before production deployment.

### ADR Scope and Structure

**Problem**: This ADR is 650+ lines, significantly exceeding typical ADR length (30-50 lines). Much of the content (implementation details, code examples) reads more like design documentation than decision record.

**Consideration**: Should implementation details (Sections 4-7) be moved to a separate design document in `docs/problems/gitlab-implementation.md`, leaving the ADR focused on the architectural decision and high-level consequences? This would improve maintainability and align with the pattern of problem-oriented documents that can evolve independently (per CLAUDE.md).

## References

- ADR-0005: Forge abstraction layer
- ADR-0007: Per-role GitHub Apps (authentication model to replicate)
- ADR-0008: workflow_dispatch for cross-repo dispatch (pattern to replicate with triggers)
- ADR-0009: pull_request_target security model (challenge to solve)
- GitLab CI/CD documentation: https://docs.gitlab.com/ee/ci/
- GitLab Project Access Tokens: https://docs.gitlab.com/ee/user/project/settings/project_access_tokens.html
- GitLab Pipeline Triggers: https://docs.gitlab.com/ee/ci/triggers/
