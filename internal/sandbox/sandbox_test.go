package sandbox

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureAvailable_OpenshellNotInPath(t *testing.T) {
	// Save and clear PATH to ensure openshell is not found.
	t.Setenv("PATH", "")

	err := EnsureAvailable()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "openshell not found in PATH")
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "/tmp/workspace", SandboxWorkspace)
	assert.Equal(t, "/tmp/claude-config", SandboxClaudeConfig)
}

func TestBuildProviderArgs_BareKeyCredentials(t *testing.T) {
	t.Setenv("MY_SECRET", "super-secret-value")

	credentials := map[string]string{
		"API_KEY": "${MY_SECRET}",
	}
	config := map[string]string{
		"BASE_URL": "https://api.example.com",
	}

	args, extraEnv, secrets := buildProviderArgs("test-provider", "anthropic", credentials, config)

	// Args must use bare-key form: --credential API_KEY (no =value).
	assert.Contains(t, args, "--credential")
	for _, arg := range args {
		if strings.HasPrefix(arg, "API_KEY") {
			assert.Equal(t, "API_KEY", arg, "credential arg must be bare key, not KEY=VALUE")
		}
	}

	// Secret value must NOT appear anywhere in args.
	for _, arg := range args {
		assert.NotContains(t, arg, "super-secret-value",
			"secret value must not appear in CLI args")
	}

	// Secret value must be in extraEnv for the child process.
	require.Len(t, extraEnv, 1)
	assert.Equal(t, "API_KEY=super-secret-value", extraEnv[0])

	// Secrets list captures expanded values for redaction.
	require.Len(t, secrets, 1)
	assert.Equal(t, "super-secret-value", secrets[0])

	// Config values are not secrets — they appear as KEY=VALUE in args.
	found := false
	for _, arg := range args {
		if arg == "BASE_URL=https://api.example.com" {
			found = true
		}
	}
	assert.True(t, found, "config should appear as KEY=VALUE in args")
}

func TestBuildProviderArgs_KeyRemapping(t *testing.T) {
	// Credential key name differs from the host env var name.
	t.Setenv("HOST_VAR_NAME", "the-secret")

	credentials := map[string]string{
		"PROVIDER_KEY": "${HOST_VAR_NAME}",
	}

	args, extraEnv, _ := buildProviderArgs("p", "custom", credentials, nil)

	// Bare key uses the credential key name, not the host var name.
	for _, arg := range args {
		assert.NotContains(t, arg, "the-secret")
	}

	// The child env maps the credential key to the expanded value.
	require.Len(t, extraEnv, 1)
	assert.Equal(t, "PROVIDER_KEY=the-secret", extraEnv[0])
}

func TestBuildProviderArgs_EmptyCredential(t *testing.T) {
	t.Setenv("EMPTY_VAR", "")

	credentials := map[string]string{
		"KEY": "${EMPTY_VAR}",
	}

	_, extraEnv, secrets := buildProviderArgs("p", "custom", credentials, nil)

	// Empty values should still be set in env (openshell may accept empty).
	require.Len(t, extraEnv, 1)
	assert.Equal(t, "KEY=", extraEnv[0])

	// Empty string is not added to secrets (nothing to redact).
	assert.Empty(t, secrets)
}

func TestCollectLogs_OpenshellNotInPath(t *testing.T) {
	t.Setenv("PATH", "")

	_, err := CollectLogs("nonexistent-sandbox", "sandbox")
	assert.Error(t, err)
}

func TestCollectLogs_InvalidSource(t *testing.T) {
	// When openshell is not in PATH, any source should fail.
	t.Setenv("PATH", "")

	_, err := CollectLogs("test-sandbox", "invalid-source")
	assert.Error(t, err)
}

func TestPathTraversalContainment(t *testing.T) {
	// Simulate the containment check used in ExtractOutputFiles.
	localDir := "/tmp/output"
	cleanBase := filepath.Clean(localDir) + string(filepath.Separator)

	tests := []struct {
		name    string
		relPath string
		safe    bool
	}{
		{"normal file", "report.md", true},
		{"nested file", "subdir/report.md", true},
		{"traversal", "../../../etc/passwd", false},
		{"traversal with prefix", "../../home/runner/.bashrc", false},
		{"dot segments in middle", "subdir/../../etc/shadow", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localPath := filepath.Join(localDir, tt.relPath)
			contained := strings.HasPrefix(filepath.Clean(localPath), cleanBase)
			assert.Equal(t, tt.safe, contained, "relPath=%q localPath=%q", tt.relPath, localPath)
		})
	}
}
