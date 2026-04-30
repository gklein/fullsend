package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/ui"
)

func TestRunCommand_RequiresAgentName(t *testing.T) {
	cmd := newRunCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s)")
}

func TestRunCommand_HasFullsendDirFlag(t *testing.T) {
	cmd := newRunCmd()
	flag := cmd.Flags().Lookup("fullsend-dir")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)

	annotations := flag.Annotations
	require.Contains(t, annotations, "cobra_annotation_bash_completion_one_required_flag")
}

func TestRunCommand_RegisteredOnRoot(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Name() == "run" {
			found = true
			break
		}
	}
	assert.True(t, found, "run command should be registered on root")
}

func TestRunCommand_HasOutputDirFlag(t *testing.T) {
	cmd := newRunCmd()
	flag := cmd.Flags().Lookup("output-dir")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestRunCommand_HasTargetRepoFlag(t *testing.T) {
	cmd := newRunCmd()
	flag := cmd.Flags().Lookup("target-repo")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)

	annotations := flag.Annotations
	require.Contains(t, annotations, "cobra_annotation_bash_completion_one_required_flag")
}

func TestBuildClaudeCommand_Basic(t *testing.T) {
	cmd := buildClaudeCommand("hello-world", "", "/tmp/workspace/repo")
	assert.Contains(t, cmd, "cd /tmp/workspace/repo")
	assert.Contains(t, cmd, "--agent 'hello-world'")
	assert.NotContains(t, cmd, "--model")
}

func TestBuildClaudeCommand_WithModel(t *testing.T) {
	cmd := buildClaudeCommand("hello-world", "sonnet", "/tmp/workspace/repo")
	assert.Contains(t, cmd, "--model 'sonnet'")
	assert.Contains(t, cmd, "--agent 'hello-world'")
}

func TestBuildClaudeCommand_EscapesQuotes(t *testing.T) {
	cmd := buildClaudeCommand("test'name", "", "/tmp/workspace/repo")
	assert.NotContains(t, cmd, "'test'name'")
	assert.Contains(t, cmd, "'test'\\''name'")
}

func TestBuildScanContextCommand_SourcesEnv(t *testing.T) {
	traceID := "aabbccdd-1122-4334-8556-aabbccddeeff"
	cmd := buildScanContextCommand("/tmp/workspace/repo", traceID)
	assert.Contains(t, cmd, "source /tmp/workspace/.env &&")
	assert.Contains(t, cmd, "FULLSEND_TRACE_ID='"+traceID+"'")
	assert.Contains(t, cmd, "-exec fullsend scan context")
}

func TestCollectOpenshellLogs_EmptyRunDir(t *testing.T) {
	// Should be a no-op when runDir is empty — no panic, no error.
	printer := ui.New(io.Discard)
	collectOpenshellLogs("test-sandbox", "", printer)
}

func TestCollectOpenshellLogs_CreatesLogsDir(t *testing.T) {
	// collectOpenshellLogs should create the logs/ directory and attempt
	// log collection. openshell is not available in test, so we expect
	// warnings but no panic.
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "run")
	require.NoError(t, os.MkdirAll(runDir, 0o755))

	printer := ui.New(io.Discard)
	collectOpenshellLogs("nonexistent-sandbox", runDir, printer)

	// The logs directory should be created even if collection fails.
	logsDir := filepath.Join(runDir, "logs")
	_, err := os.Stat(logsDir)
	assert.NoError(t, err, "logs directory should exist")
}

func TestEnvToList_Sorted(t *testing.T) {
	env := map[string]string{
		"Z_VAR": "z",
		"A_VAR": "a",
		"M_VAR": "m",
	}
	list := envToList(env)
	require.Len(t, list, 3)
	assert.Equal(t, "A_VAR=a", list[0])
	assert.Equal(t, "M_VAR=m", list[1])
	assert.Equal(t, "Z_VAR=z", list[2])
}
