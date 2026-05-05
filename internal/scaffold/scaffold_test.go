package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/harness"
)

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
		"templates/shim-workflow.yaml",
	}

	for _, path := range expected {
		content, err := FullsendRepoFile(path)
		require.NoError(t, err, "reading %s", path)
		assert.NotEmpty(t, content, "%s should not be empty", path)
	}
}

func TestShimTemplateContent(t *testing.T) {
	content, err := FullsendRepoFile("templates/shim-workflow.yaml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "dispatch-triage")
	assert.Contains(t, s, "dispatch-code")
	assert.Contains(t, s, "dispatch-review")
	assert.Contains(t, s, "dispatch-fix")
	assert.Contains(t, s, "permissions:")
	assert.Contains(t, s, "contents: read")
	assert.Contains(t, s, "FULLSEND_DISPATCH_TOKEN")
	assert.Contains(t, s, "gh workflow run dispatch.yml")
	assert.Contains(t, s, "stage=triage")
	assert.Contains(t, s, "stage=code")
	assert.Contains(t, s, "stage=review")
	assert.Contains(t, s, "stage=fix")
}

func TestDispatchWorkflowContent(t *testing.T) {
	content, err := FullsendRepoFile(".github/workflows/dispatch.yml")
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "workflow_dispatch")
	assert.Contains(t, s, "stage:")
	assert.Contains(t, s, "event_type:")
	assert.Contains(t, s, "source_repo:")
	assert.Contains(t, s, "event_payload:")
	assert.Contains(t, s, "# fullsend-stage:")
	assert.Contains(t, s, "gh workflow run")
	assert.Contains(t, s, "FULLSEND_FULLSEND_CLIENT_ID")
	assert.Contains(t, s, "FULLSEND_FULLSEND_APP_PRIVATE_KEY")
	assert.Contains(t, s, "permissions:")
	assert.Contains(t, s, "actions: write")
	assert.Contains(t, s, "contents: read")
	assert.Contains(t, s, "set -euo pipefail")
	assert.Contains(t, s, "dispatched=0")
	assert.Contains(t, s, "No workflows found for stage")
	assert.Contains(t, s, "|| true")
	assert.Contains(t, s, "permissions: {}")
	assert.Contains(t, s, "Validate inputs")
	assert.Contains(t, s, "Invalid source_repo format")
	assert.Contains(t, s, "Invalid stage name")
	// Verify the sed pattern restricts stage names to [a-z][a-z0-9_-]*
	assert.Contains(t, s, `\([a-z][a-z0-9_-]*\)`)
	// Verify stage name validation uses the same pattern
	assert.Contains(t, s, `^[a-z][a-z0-9_-]*$`)
	// Verify trigger_source optional input
	assert.Contains(t, s, "trigger_source:")
	assert.Contains(t, s, "required: false")
	// Verify self-dispatch guard
	assert.Contains(t, s, "dispatch.yml")
	assert.Contains(t, s, "self-dispatch guard")
	// Verify workflow scanning log
	assert.Contains(t, s, "Scanned")
	assert.Contains(t, s, "skipped")
}

func TestWalkFullsendRepo(t *testing.T) {
	var paths []string
	err := WalkFullsendRepo(func(path string, content []byte) error {
		paths = append(paths, path)
		return nil
	})
	require.NoError(t, err)
	assert.True(t, len(paths) >= 29, "expected at least 29 files, got %d", len(paths))
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
	assert.Contains(t, s, "fullsend")
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
	assert.Contains(t, s, "FULLSEND_CODER_CLIENT_ID")
	assert.Contains(t, s, "pre-code.sh")
	assert.Contains(t, s, "PUSH_TOKEN")
	assert.Contains(t, s, "github-app")
	assert.Contains(t, s, "sandbox-token")
	assert.Contains(t, s, "push-token")
	assert.Contains(t, s, "permission-contents: read")
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
	assert.Contains(t, s, "FULLSEND_REVIEW_CLIENT_ID")
	assert.Contains(t, s, "sandbox-token")
	assert.Contains(t, s, "review-token")
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
	assert.Contains(t, s, "FULLSEND_CODER_CLIENT_ID")
	assert.Contains(t, s, "sandbox-token")
	assert.Contains(t, s, "push-token")
	assert.Contains(t, s, "./.github/actions/setup-gcp")
	assert.Contains(t, s, "./.github/actions/validate-enrollment")
	// Verify concurrency group prevents overlapping runs
	assert.Contains(t, s, "concurrency:")
	assert.Contains(t, s, "fullsend-fix-")
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

func TestValidateTriageDeleted(t *testing.T) {
	_, err := FullsendRepoFile("scripts/validate-triage.sh")
	assert.Error(t, err, "validate-triage.sh should have been deleted")
}
