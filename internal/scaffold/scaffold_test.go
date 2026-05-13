package scaffold

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/fullsend-ai/fullsend/internal/harness"
)

func TestFileModeMatchesFilesystem(t *testing.T) {
	scaffoldRoot := "fullsend-repo"

	var onDiskExecutable []string
	err := filepath.WalkDir(scaffoldRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		relPath := path[len(scaffoldRoot)+1:]
		if info.Mode()&0o111 != 0 {
			onDiskExecutable = append(onDiskExecutable, relPath)
		}
		return nil
	})
	require.NoError(t, err)

	for _, path := range onDiskExecutable {
		assert.Equal(t, "100755", FileMode(path),
			"file %s is executable on disk but not in executableFiles", path)
	}

	for path := range executableFiles {
		info, statErr := os.Stat(filepath.Join(scaffoldRoot, path))
		require.NoError(t, statErr, "file %s is in executableFiles but not on disk", path)
		assert.NotEqual(t, os.FileMode(0), info.Mode()&0o111,
			"file %s is in executableFiles but is not executable on disk", path)
	}
}

func TestFullsendRepoFilesExist(t *testing.T) {
	expected := []string{
		".github/workflows/dispatch.yml",
		".github/workflows/triage.yml",
		".github/workflows/code.yml",
		".github/workflows/review.yml",
		".github/workflows/fix.yml",
		".github/workflows/repo-maintenance.yml",
		".github/actions/fullsend/action.yml",
		".github/actions/setup-gcp/action.yml",
		".github/actions/validate-enrollment/action.yml",
		".github/scripts/setup-agent-env.sh",
		"agents/triage.md",
		"agents/code.md",
		"env/gcp-vertex.env",
		"env/triage.env",
		"env/code-agent.env",
		"harness/triage.yaml",
		"harness/code.yaml",
		"policies/triage.yaml",
		"policies/code.yaml",
		"schemas/triage-result.schema.json",
		"scripts/post-triage.sh",
		"scripts/pre-triage.sh",
		"scripts/scan-secrets",
		"scripts/pre-code.sh",
		"scripts/pre-review.sh",
		"scripts/post-code.sh",
		"scripts/reconcile-repos.sh",
		"scripts/validate-output-schema.sh",
		"scripts/validate-source-repo.sh",
		"skills/code-implementation/SKILL.md",
		"templates/shim-workflow-call.yaml",
		"agents/prioritize.md",
		"env/prioritize.env",
		"harness/prioritize.yaml",
		"policies/prioritize.yaml",
		"schemas/prioritize-result.schema.json",
		"scripts/setup-prioritize.sh",
		"scripts/pre-prioritize.sh",
		"scripts/post-prioritize.sh",
		".github/workflows/prioritize.yml",
		".github/workflows/prioritize-scheduler.yml",
	}

	for _, path := range expected {
		content, err := FullsendRepoFile(path)
		require.NoError(t, err, "reading %s", path)
		assert.NotEmpty(t, content, "%s should not be empty", path)
	}
}

func TestShimWorkflowCallTemplateContent(t *testing.T) {
	content, err := FullsendRepoFile("templates/shim-workflow-call.yaml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "dispatch-triage")
	assert.Contains(t, s, "dispatch-code")
	assert.Contains(t, s, "dispatch-review")
	assert.Contains(t, s, "dispatch-fix-bot")
	assert.Contains(t, s, "dispatch-fix-human")
	assert.Contains(t, s, "dispatch-prioritize")
	assert.Contains(t, s, "dispatch-retro")
	assert.Contains(t, s, "dispatch-stop-fix")
	assert.Contains(t, s, "id-token: write")
	assert.Contains(t, s, "workflow_call")
	assert.Contains(t, s, "__ORG__/.fullsend/.github/workflows/dispatch.yml@main")
	assert.Contains(t, s, "stage: triage")
	assert.Contains(t, s, "stage: code")
	assert.Contains(t, s, "stage: review")
	assert.Contains(t, s, "stage: fix")
	assert.Contains(t, s, "stage: retro")
	assert.Contains(t, s, "stage: prioritize")
	assert.Contains(t, s, "trigger_source:")
	assert.Contains(t, s, "secrets: {}")
	assert.Contains(t, s, "post-run-link:")
	assert.NotContains(t, s, "FULLSEND_DISPATCH_TOKEN")
	assert.NotContains(t, s, "FULLSEND_DISPATCH_URL")
	assert.NotContains(t, s, "curl")
}

func TestShimWorkflowCallPostRunLink(t *testing.T) {
	content, err := FullsendRepoFile("templates/shim-workflow-call.yaml")
	require.NoError(t, err)
	s := string(content)

	// Verify post-run-link job exists
	assert.Contains(t, s, "post-run-link:")

	// Extract the post-run-link job block
	idx := strings.Index(s, "post-run-link:")
	require.NotEqual(t, -1, idx, "post-run-link job must exist")
	block := s[idx:]

	// Verify write permissions for issues and pull-requests
	assert.Contains(t, block, "issues: write")
	assert.Contains(t, block, "pull-requests: write")

	// Verify it runs after all dispatch jobs
	assert.Contains(t, block, "dispatch-triage")
	assert.Contains(t, block, "dispatch-code")
	assert.Contains(t, block, "dispatch-review")
	assert.Contains(t, block, "dispatch-fix-bot")
	assert.Contains(t, block, "dispatch-fix-human")
	assert.Contains(t, block, "dispatch-retro")
	assert.Contains(t, block, "dispatch-retro-command")
	assert.Contains(t, block, "dispatch-prioritize")

	// Verify it posts a comment with the run URL
	assert.Contains(t, block, "gh issue comment")
	assert.Contains(t, block, "fullsend ${AGENT}")
	assert.Contains(t, block, "view logs")
	assert.Contains(t, block, "SHIM_URL")
	assert.Contains(t, block, "ITEM_NUMBER")
}

func TestShimWorkflowCallCodeExcludesPRContext(t *testing.T) {
	content, err := FullsendRepoFile("templates/shim-workflow-call.yaml")
	require.NoError(t, err)
	s := string(content)

	codeIdx := strings.Index(s, "dispatch-code:")
	require.NotEqual(t, -1, codeIdx, "dispatch-code job must exist")

	rest := s[codeIdx+len("dispatch-code:"):]
	nextJob := strings.Index(rest, "\n  dispatch-")
	if nextJob == -1 {
		nextJob = len(rest)
	}
	codeBlock := rest[:nextJob]

	assert.Contains(t, codeBlock, "!github.event.issue.pull_request",
		"dispatch-code job must exclude PR contexts with !github.event.issue.pull_request guard")
}

func TestDispatchWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/dispatch.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "workflow_call:")
	assert.NotContains(t, s, "workflow_dispatch:")
	assert.Contains(t, s, "stage:")
	assert.Contains(t, s, "# fullsend-stage:")
	assert.Contains(t, s, "gh workflow run")
	assert.Contains(t, s, "permissions: {}")
	assert.Contains(t, s, "permissions:")
	assert.Contains(t, s, "actions: write")
	assert.Contains(t, s, "contents: read")
	assert.Contains(t, s, "id-token: write")
	assert.Contains(t, s, "set -euo pipefail")
	assert.Contains(t, s, "dispatched=0")
	assert.Contains(t, s, "No workflows found for stage")
	assert.Contains(t, s, "|| true")
	assert.Contains(t, s, "Validate inputs")
	assert.Contains(t, s, "Invalid stage name")
	assert.Contains(t, s, `\([a-z][a-z0-9_-]*\)`)
	assert.Contains(t, s, `^[a-z][a-z0-9_-]*$`)
	assert.Contains(t, s, "trigger_source:")
	assert.Contains(t, s, "required: false")
	assert.Contains(t, s, "dispatch.yml")
	assert.Contains(t, s, "self-dispatch guard")
	assert.Contains(t, s, "Scanned")
	assert.Contains(t, s, "skipped")
	// Verify OIDC mint is the sole token path
	assert.Contains(t, s, "FULLSEND_MINT_URL")
	assert.Contains(t, s, "oidc-mint")
	assert.Contains(t, s, "/v1/token")
	assert.Contains(t, s, "fullsend-mint")
	assert.Contains(t, s, "job.workflow_repository")
	// Verify both OIDC token and minted token are masked
	assert.Contains(t, s, "::add-mask::$OIDC_TOKEN")
	assert.Contains(t, s, "::add-mask::$TOKEN")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_FULLSEND_APP_PRIVATE_KEY")
	assert.NotContains(t, s, "FULLSEND_FULLSEND_CLIENT_ID")
}

func TestWalkFullsendRepo(t *testing.T) {
	var paths []string
	err := WalkFullsendRepo(func(path string, content []byte) error {
		paths = append(paths, path)
		return nil
	})
	require.NoError(t, err)
	assert.True(t, len(paths) >= 28, "expected at least 28 files, got %d", len(paths))
}

func TestTriageWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/triage.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "# fullsend-stage: triage")
	assert.Contains(t, s, "workflow_dispatch")
	assert.Contains(t, s, "event_type")
	assert.Contains(t, s, "source_repo")
	assert.Contains(t, s, "event_payload")
	assert.Contains(t, s, "setup-agent-env.sh")
	assert.Contains(t, s, "./.github/actions/mint-token")
	assert.Contains(t, s, "role: triage")
	assert.Contains(t, s, "FULLSEND_MINT_URL")
	assert.Contains(t, s, "./.github/actions/setup-gcp")
	assert.Contains(t, s, "./.github/actions/validate-enrollment")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_TRIAGE_CLIENT_ID")
	// Verify concurrency group prevents overlapping runs for same issue
	assert.Contains(t, s, "concurrency:")
	assert.Contains(t, s, "fullsend-triage-")
	assert.Contains(t, s, "cancel-in-progress: true")
}

func TestCompositeActionContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/actions/fullsend/action.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "fullsend run")
	assert.Contains(t, s, "openshell")
}

func TestCodeAgentContent(t *testing.T) {
	content, err := FullsendRepoFile("agents/code.md")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "code")
	assert.Contains(t, s, "disallowedTools")
	assert.Contains(t, s, "code-implementation")
}

func TestCodeWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/code.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "workflow_dispatch")
	assert.Contains(t, s, "pre-code.sh")
	assert.Contains(t, s, "PUSH_TOKEN")
	assert.Contains(t, s, "github-app")
	assert.Contains(t, s, "./.github/actions/mint-token")
	assert.Contains(t, s, "role: coder")
	assert.Contains(t, s, "FULLSEND_MINT_URL")
	assert.Contains(t, s, "./.github/actions/setup-gcp")
	assert.Contains(t, s, "./.github/actions/validate-enrollment")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_CODER_CLIENT_ID")
	// Verify concurrency group prevents overlapping runs for same issue
	assert.Contains(t, s, "concurrency:")
	assert.Contains(t, s, "fullsend-code-")
	assert.Contains(t, s, "cancel-in-progress: true")
}

func TestReviewWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/review.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "# fullsend-stage: review")
	assert.Contains(t, s, "workflow_dispatch")
	assert.Contains(t, s, "event_type")
	assert.Contains(t, s, "source_repo")
	assert.Contains(t, s, "event_payload")
	assert.Contains(t, s, "./.github/actions/mint-token")
	assert.Contains(t, s, "role: review")
	assert.Contains(t, s, "FULLSEND_MINT_URL")
	assert.Contains(t, s, "./.github/actions/setup-gcp")
	assert.Contains(t, s, "./.github/actions/validate-enrollment")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_REVIEW_CLIENT_ID")
	// Verify concurrency group prevents overlapping runs
	assert.Contains(t, s, "concurrency:")
	assert.Contains(t, s, "fullsend-review-")
	assert.Contains(t, s, "cancel-in-progress: true")
}

func TestFixWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/fix.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "# fullsend-stage: fix")
	assert.Contains(t, s, "workflow_dispatch")
	assert.Contains(t, s, "event_type")
	assert.Contains(t, s, "source_repo")
	assert.Contains(t, s, "event_payload")
	assert.Contains(t, s, "trigger_source")
	assert.Contains(t, s, "./.github/actions/mint-token")
	assert.Contains(t, s, "role: coder")
	assert.Contains(t, s, "FULLSEND_MINT_URL")
	assert.Contains(t, s, "./.github/actions/setup-gcp")
	assert.Contains(t, s, "./.github/actions/validate-enrollment")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_CODER_CLIENT_ID")
	// Verify concurrency group prevents overlapping runs
	assert.Contains(t, s, "concurrency:")
	assert.Contains(t, s, "fullsend-fix-")
	assert.Contains(t, s, "cancel-in-progress: true")
}

func TestRetroWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/retro.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "# fullsend-stage: retro")
	assert.Contains(t, s, "workflow_dispatch")
	assert.Contains(t, s, "./.github/actions/validate-enrollment")
	assert.Contains(t, s, "./.github/actions/mint-token")
	assert.Contains(t, s, "role: retro")
	assert.Contains(t, s, "FULLSEND_MINT_URL")
	assert.Contains(t, s, ".fullsend")
	assert.Contains(t, s, "./.github/actions/setup-gcp")
	assert.Contains(t, s, "RETRO_TARGET_REPO_DIR: target-repo")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_RETRO_CLIENT_ID")
	assert.Contains(t, s, "concurrency:")
	assert.Contains(t, s, "fullsend-retro-")
	assert.Contains(t, s, "cancel-in-progress: true")
}

func TestSetupGcpActionContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/actions/setup-gcp/action.yml")
	require.NoError(t, err)
	s := string(content)
	// Verify inputs (composite actions cannot access vars/secrets directly)
	assert.Contains(t, s, "inputs:")
	assert.Contains(t, s, "gcp_auth_mode:")
	assert.Contains(t, s, "gcp_wif_provider:")
	assert.Contains(t, s, "gcp_wif_sa_email:")
	assert.Contains(t, s, "gcp_sa_key_json:")
	// Verify pre-mask step
	assert.Contains(t, s, "Pre-mask GCP credential file path")
	assert.Contains(t, s, "GITHUB_WORKSPACE}/gha-creds-")
	// Verify WIF authentication path
	assert.Contains(t, s, "if: inputs.gcp_auth_mode == 'wif'")
	assert.Contains(t, s, "google-github-actions/auth@v3")
	assert.Contains(t, s, "workload_identity_provider:")
	assert.Contains(t, s, "service_account:")
	// Verify SA key authentication path
	assert.Contains(t, s, "if: inputs.gcp_auth_mode != 'wif'")
	assert.Contains(t, s, "credentials_json:")
	// Verify OIDC token workaround for non-WIF
	assert.Contains(t, s, "RUNNER_TEMP/empty-oidc-token")
	assert.Contains(t, s, "GCP_OIDC_TOKEN_FILE")
	// Verify credential masking
	assert.Contains(t, s, "Mask GCP credential file paths")
	assert.Contains(t, s, "::add-mask::")
	assert.Contains(t, s, "GOOGLE_GHA_CREDS_PATH")
	assert.Contains(t, s, "GOOGLE_APPLICATION_CREDENTIALS")
	assert.Contains(t, s, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE")
	// Verify sandbox preparation
	assert.Contains(t, s, "prepare-sandbox-credentials.sh")
}

func TestValidateEnrollmentActionContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/actions/validate-enrollment/action.yml")
	require.NoError(t, err)
	s := string(content)
	// Verify inputs declarations
	assert.Contains(t, s, "inputs:")
	assert.Contains(t, s, "source_repo:")
	assert.Contains(t, s, "required: true")
	// Verify outputs contract
	assert.Contains(t, s, "outputs:")
	assert.Contains(t, s, "name:")
	assert.Contains(t, s, "steps.extract.outputs.name")
	// Verify step ID matches output reference
	assert.Contains(t, s, "id: extract")
	// Verify SOURCE_REPO env var wiring
	assert.Contains(t, s, "SOURCE_REPO: ${{ inputs.source_repo }}")
	// Verify enrollment validation script
	assert.Contains(t, s, "validate-source-repo.sh")
}

func TestValidateSourceRepoContent(t *testing.T) {
	content, err := FullsendRepoFile("scripts/validate-source-repo.sh")
	require.NoError(t, err)
	s := string(content)
	// Verify security-critical format regex
	assert.Contains(t, s, "^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$")
	assert.Contains(t, s, "Invalid source_repo format")
	// Verify owner check
	assert.Contains(t, s, "REPO_OWNER=\"${SOURCE_REPO%%/*}\"")
	assert.Contains(t, s, "source_repo owner does not match org")
	// Verify allowlist check
	assert.Contains(t, s, "REPO_NAME=\"${SOURCE_REPO#*/}\"")
	assert.Contains(t, s, "repo is not enabled in config.yaml")
	// Verify required environment variables
	assert.Contains(t, s, "${SOURCE_REPO:?SOURCE_REPO is required}")
	assert.Contains(t, s, "${GITHUB_REPOSITORY_OWNER:?GITHUB_REPOSITORY_OWNER is required}")
	// Verify error messages use ::error:: format
	assert.Contains(t, s, "::error::")
	// Verify config.yaml existence check (not masked by 2>/dev/null)
	assert.Contains(t, s, "config.yaml not found")
	// Verify yq availability check
	assert.Contains(t, s, "yq command not found")
}

func TestCodeHarnessContent(t *testing.T) {
	content, err := FullsendRepoFile("harness/code.yaml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "agents/code.md")
	assert.Contains(t, s, "pre_script")
	assert.Contains(t, s, "post_script")
	assert.Contains(t, s, "runner_env")
	assert.Contains(t, s, "PUSH_TOKEN")
}

func TestScanSecretsContent(t *testing.T) {
	content, err := FullsendRepoFile("scripts/scan-secrets")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "gitleaks")
	assert.Contains(t, s, "scan-secrets")
}

func TestScanSecretsImageMatchesScaffold(t *testing.T) {
	imageContent, err := os.ReadFile("../../images/code/scan-secrets")
	require.NoError(t, err)
	scaffoldContent, err := FullsendRepoFile("scripts/scan-secrets")
	require.NoError(t, err)
	assert.Equal(t, string(imageContent), string(scaffoldContent),
		"images/code/scan-secrets must stay in sync with scaffold scripts/scan-secrets")
}

func TestSetupAgentEnvContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/scripts/setup-agent-env.sh")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "AGENT_PREFIX")
	assert.Contains(t, s, "GITHUB_ENV")
}

func TestTriageAgentPromptContent(t *testing.T) {
	content, err := FullsendRepoFile("agents/triage.md")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "agent-result.json")
	assert.Contains(t, s, "clarity_scores")
	assert.Contains(t, s, "Anti-premature-resolution")
}

func TestTriageSchemaContent(t *testing.T) {
	content, err := FullsendRepoFile("schemas/triage-result.schema.json")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "$schema")
	assert.Contains(t, s, "insufficient")
	assert.Contains(t, s, "duplicate")
	assert.Contains(t, s, "sufficient")
}

func TestHarnessesLoadAndValidate(t *testing.T) {
	// Extract the full scaffold to a temp dir so harness.Load can resolve
	// relative paths and validate that referenced files exist. This catches
	// harness validation errors (e.g., missing fields, invalid combinations)
	// the same way the runner would at startup.
	dir := t.TempDir()
	err := WalkFullsendRepo(func(path string, content []byte) error {
		dest := filepath.Join(dir, path)
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(dest, content, 0o644)
	})
	require.NoError(t, err, "extracting scaffold")

	// Find all harness YAML files.
	entries, err := os.ReadDir(filepath.Join(dir, "harness"))
	require.NoError(t, err)

	var loaded int
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			harnessPath := filepath.Join(dir, "harness", e.Name())
			h, err := harness.Load(harnessPath)
			require.NoError(t, err, "Load should succeed")

			err = h.ResolveRelativeTo(dir)
			require.NoError(t, err, "ResolveRelativeTo should succeed")

			err = h.ValidateFilesExist()
			require.NoError(t, err, "ValidateFilesExist should succeed")
		})
		loaded++
	}
	assert.True(t, loaded >= 2, "expected at least 2 harnesses, got %d", loaded)
}

func TestRepoMaintenanceWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/repo-maintenance.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "config.yaml")
	assert.Contains(t, s, "templates/shim-workflow-call.yaml",
		"push trigger must include workflow_call shim template so changes propagate to enrolled repos")
	assert.NotContains(t, s, "templates/shim-workflow.yaml",
		"PAT shim template reference should be removed")
	assert.Contains(t, s, "./.github/actions/mint-token")
	assert.Contains(t, s, "role: fullsend")
	assert.Contains(t, s, "id-token: write")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_FULLSEND_CLIENT_ID")
}

func TestMintTokenActionContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/actions/mint-token/action.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "Mint Token")
	assert.Contains(t, s, "OIDC")
	assert.Contains(t, s, "audience=fullsend-mint")
	assert.Contains(t, s, "/v1/token")
	assert.Contains(t, s, "::add-mask::$OIDC_TOKEN")
	assert.Contains(t, s, "::add-mask::$TOKEN")
	assert.Contains(t, s, "ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	assert.Contains(t, s, "ACTIONS_ID_TOKEN_REQUEST_URL")
	assert.Contains(t, s, "jq -nc --arg role")
	assert.NotContains(t, s, "create-github-app-token")
}

func TestReconcileReposContent(t *testing.T) {
	content, err := FullsendRepoFile("scripts/reconcile-repos.sh")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "shim-workflow-call.yaml")
	assert.NotContains(t, s, "shim-workflow.yaml\"",
		"reconcile-repos.sh should not reference deleted PAT shim template")
	assert.NotContains(t, s, "dispatch.mode",
		"reconcile-repos.sh should not parse dispatch mode")
	assert.Contains(t, s, "private repos cannot be enrolled",
		"reconcile-repos.sh should skip private repos to prevent log exposure")
}

func TestPrioritizeWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/prioritize.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "# fullsend-stage: prioritize")
	assert.Contains(t, s, "workflow_dispatch")
	assert.Contains(t, s, "event_type")
	assert.Contains(t, s, "source_repo")
	assert.Contains(t, s, "event_payload")
	assert.Contains(t, s, "FULLSEND_PROJECT_NUMBER")
	assert.Contains(t, s, "setup-agent-env.sh")
	assert.Contains(t, s, "agent: prioritize")
	assert.Contains(t, s, "concurrency:")
	assert.Contains(t, s, "fullsend-prioritize")
	assert.Contains(t, s, "cancel-in-progress: true")
	assert.Contains(t, s, "mkdir -p target-repo")
	assert.Contains(t, s, "GITHUB_ISSUE_URL")
	assert.Contains(t, s, "fromJSON(inputs.event_payload)")
	assert.Contains(t, s, "./.github/actions/mint-token")
	assert.Contains(t, s, "role: prioritize")
	assert.Contains(t, s, "FULLSEND_MINT_URL")
	assert.Contains(t, s, "./.github/actions/setup-gcp")
	assert.Contains(t, s, "./.github/actions/validate-enrollment")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_PRIORITIZE_CLIENT_ID")
}

func TestPrioritizeSchedulerWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/prioritize-scheduler.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "# schedule:", "cron trigger should be commented out by default (#778)")
	assert.Contains(t, s, "#   - cron:", "cron trigger should be commented out by default (#778)")
	assert.Contains(t, s, "workflow_dispatch")
	assert.Contains(t, s, "fullsend-prioritize-scheduler")
	assert.Contains(t, s, "RICE Score")
	assert.Contains(t, s, "prioritize.yml")
	assert.Contains(t, s, "FULLSEND_PROJECT_NUMBER")
	assert.Contains(t, s, "FULLSEND_PROJECT_NUMBER is not set; skipping prioritize scheduler")
	guardIndex := strings.Index(s, `if [[ -z "${PROJECT_NUMBER}" ]]; then`)
	projectViewIndex := strings.Index(s, `gh project view "${PROJECT_NUMBER}"`)
	require.NotEqual(t, -1, guardIndex)
	require.NotEqual(t, -1, projectViewIndex)
	assert.Less(t, guardIndex, projectViewIndex, "PROJECT_NUMBER must be checked before gh project view")
	assert.Contains(t, s, "./.github/actions/mint-token")
	assert.Contains(t, s, "role: fullsend")
	assert.Contains(t, s, "id-token: write")
	assert.NotContains(t, s, "create-github-app-token")
	assert.NotContains(t, s, "FULLSEND_FULLSEND_CLIENT_ID")
}

func TestPrioritizeSchedulerSkipsWhenProjectNumberUnset(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/prioritize-scheduler.yml")
	require.NoError(t, err)

	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(content, &workflow))

	dispatchJob, ok := workflow.Jobs["dispatch"]
	require.True(t, ok, "dispatch job should exist")

	var runScript string
	for _, step := range dispatchJob.Steps {
		if step.Name == "Find issues and dispatch prioritize runs" {
			runScript = step.Run
			break
		}
	}
	require.NotEmpty(t, runScript, "prioritize scheduler dispatch script should exist")

	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	require.NoError(t, os.Mkdir(binDir, 0o755))

	ghLog := filepath.Join(tmpDir, "gh-calls.log")
	fakeGH := "#!/usr/bin/env bash\n" +
		"printf 'gh called: %s\\n' \"$*\" >> " + strconv.Quote(ghLog) + "\n" +
		"exit 99\n"
	ghPath := filepath.Join(binDir, "gh")
	require.NoError(t, os.WriteFile(ghPath, []byte(fakeGH), 0o755))

	scriptPath := filepath.Join(tmpDir, "prioritize-scheduler-run.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(runScript), 0o755))

	cmd := exec.Command("bash", scriptPath)
	cmd.Env = []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"PROJECT_NUMBER=",
		"ORG=test-org",
		"GH_TOKEN=test-token",
		"WIP_LIMIT=5",
		"STALE_THRESHOLD=7d",
		"GITHUB_REPOSITORY=test-org/.fullsend",
	}

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	assert.Contains(t, string(output), "FULLSEND_PROJECT_NUMBER is not set; skipping prioritize scheduler")
	_, statErr := os.Stat(ghLog)
	assert.True(t, os.IsNotExist(statErr), "gh should not be called when PROJECT_NUMBER is unset")
}

func TestPrioritizeAgentPromptContent(t *testing.T) {
	content, err := FullsendRepoFile("agents/prioritize.md")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "agent-result.json")
	assert.Contains(t, s, "RICE")
	assert.Contains(t, s, "Reach")
	assert.Contains(t, s, "Impact")
	assert.Contains(t, s, "Confidence")
	assert.Contains(t, s, "Effort")
	assert.Contains(t, s, "customer-research skill")
}

func TestPrioritizeSchemaContent(t *testing.T) {
	content, err := FullsendRepoFile("schemas/prioritize-result.schema.json")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "$schema")
	assert.Contains(t, s, "reach")
	assert.Contains(t, s, "impact")
	assert.Contains(t, s, "confidence")
	assert.Contains(t, s, "effort")
	assert.Contains(t, s, "reasoning")
}

func TestPrioritizeHarnessContent(t *testing.T) {
	content, err := FullsendRepoFile("harness/prioritize.yaml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "agents/prioritize.md")
	assert.Contains(t, s, "pre_script")
	assert.Contains(t, s, "post_script")
	assert.Contains(t, s, "runner_env")
	assert.Contains(t, s, "PROJECT_NUMBER")
}

func TestValidateTriageDeleted(t *testing.T) {
	_, err := FullsendRepoFile("scripts/validate-triage.sh")
	assert.Error(t, err, "validate-triage.sh should have been deleted")
}
