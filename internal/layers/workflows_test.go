package layers

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/scaffold"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func newWorkflowsLayer(t *testing.T, client *forge.FakeClient) (*WorkflowsLayer, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	printer := ui.New(&buf)
	layer := NewWorkflowsLayer("test-org", client, printer, "admin-user", "v0.2.0")
	return layer, &buf
}

func TestWorkflowsLayer_Name(t *testing.T) {
	layer, _ := newWorkflowsLayer(t, forge.NewFakeClient())
	assert.Equal(t, "workflows", layer.Name())
}

func TestWorkflowsLayer_Install_WritesAllFiles(t *testing.T) {
	client := forge.NewFakeClient()
	layer, _ := newWorkflowsLayer(t, client)

	err := layer.Install(context.Background())
	require.NoError(t, err)

	// Scaffold files go through CommitFiles as a single batch.
	require.Len(t, client.CommittedFiles, 1, "expected exactly one CommitFiles call")
	batch := client.CommittedFiles[0]
	assert.Equal(t, "test-org", batch.Owner)
	assert.Equal(t, ".fullsend", batch.Repo)

	paths := make(map[string]string)
	for _, f := range batch.Files {
		paths[f.Path] = string(f.Content)
	}

	assert.Contains(t, paths, ".github/workflows/triage.yml")
	assert.Contains(t, paths, ".github/workflows/code.yml")
	assert.Contains(t, paths, ".github/workflows/review.yml")
	assert.Contains(t, paths, ".github/workflows/fix.yml")
	assert.Contains(t, paths, ".github/workflows/repo-maintenance.yml")

	// CODEOWNERS is included in the same batch.
	assert.Contains(t, paths, "CODEOWNERS")
	assert.Contains(t, paths["CODEOWNERS"], "admin-user")
}

func TestWorkflowsLayer_Install_TriageWorkflowContent(t *testing.T) {
	client := forge.NewFakeClient()
	layer, _ := newWorkflowsLayer(t, client)

	err := layer.Install(context.Background())
	require.NoError(t, err)

	var triageContent string
	for _, f := range client.CommittedFiles[0].Files {
		if f.Path == ".github/workflows/triage.yml" {
			triageContent = string(f.Content)
			break
		}
	}
	require.NotEmpty(t, triageContent, "triage.yml should have been written")

	expected, err := scaffold.FullsendRepoFile(".github/workflows/triage.yml")
	require.NoError(t, err)
	assert.Equal(t, string(expected), triageContent)
}

func TestWorkflowsLayer_Install_RepoMaintenanceContent(t *testing.T) {
	client := forge.NewFakeClient()
	layer, _ := newWorkflowsLayer(t, client)

	err := layer.Install(context.Background())
	require.NoError(t, err)

	var maintenanceContent string
	for _, f := range client.CommittedFiles[0].Files {
		if f.Path == ".github/workflows/repo-maintenance.yml" {
			maintenanceContent = string(f.Content)
			break
		}
	}
	require.NotEmpty(t, maintenanceContent, "repo-maintenance.yml should have been written")

	expected, err := scaffold.FullsendRepoFile(".github/workflows/repo-maintenance.yml")
	require.NoError(t, err)
	assert.Equal(t, string(expected), maintenanceContent)
}


func TestWorkflowsLayer_Install_PinsCliVersion(t *testing.T) {
	client := forge.NewFakeClient()
	layer, _ := newWorkflowsLayer(t, client)

	err := layer.Install(context.Background())
	require.NoError(t, err)

	var actionContent string
	for _, f := range client.CommittedFiles[0].Files {
		if f.Path == actionYMLPath {
			actionContent = string(f.Content)
			break
		}
	}
	require.NotEmpty(t, actionContent, "action.yml should have been written")
	assert.Contains(t, actionContent, "default: v0.2.0",
		"action.yml should pin CLI version")
	assert.NotContains(t, actionContent, "default: latest",
		"action.yml should not contain latest")
}

func TestWorkflowsLayer_Install_DevVersionFallsBackToLatest(t *testing.T) {
	client := forge.NewFakeClient()
	var buf bytes.Buffer
	printer := ui.New(&buf)
	layer := NewWorkflowsLayer("test-org", client, printer, "admin-user", "dev")

	err := layer.Install(context.Background())
	require.NoError(t, err)

	var actionContent string
	for _, f := range client.CommittedFiles[0].Files {
		if f.Path == actionYMLPath {
			actionContent = string(f.Content)
			break
		}
	}
	require.NotEmpty(t, actionContent, "action.yml should have been written")
	assert.Contains(t, actionContent, "default: latest",
		"dev version should fall back to latest")
	assert.Contains(t, buf.String(), "unpinned",
		"should warn about unpinned version")
}

func TestWorkflowsLayer_Install_EmptyVersionKeepsLatest(t *testing.T) {
	client := forge.NewFakeClient()
	var buf bytes.Buffer
	printer := ui.New(&buf)
	layer := NewWorkflowsLayer("test-org", client, printer, "admin-user", "")

	err := layer.Install(context.Background())
	require.NoError(t, err)

	var actionContent string
	for _, f := range client.CommittedFiles[0].Files {
		if f.Path == actionYMLPath {
			actionContent = string(f.Content)
			break
		}
	}
	require.NotEmpty(t, actionContent, "action.yml should have been written")
	assert.Contains(t, actionContent, "default: latest",
		"empty version should keep latest")
}

func TestWorkflowsLayer_Install_ReinstallUpdatesVersion(t *testing.T) {
	client := forge.NewFakeClient()
	var buf bytes.Buffer
	printer := ui.New(&buf)
	layer := NewWorkflowsLayer("test-org", client, printer, "admin-user", "v0.1.0")

	err := layer.Install(context.Background())
	require.NoError(t, err)

	client2 := forge.NewFakeClient()
	layer2 := NewWorkflowsLayer("test-org", client2, printer, "admin-user", "v0.2.0")

	err = layer2.Install(context.Background())
	require.NoError(t, err)

	var actionContent string
	for _, f := range client2.CommittedFiles[0].Files {
		if f.Path == actionYMLPath {
			actionContent = string(f.Content)
			break
		}
	}
	require.NotEmpty(t, actionContent, "action.yml should have been written")
	assert.Contains(t, actionContent, "default: v0.2.0",
		"re-install should update pinned version")
}

func TestWorkflowsLayer_Install_GitDescribeVersionFallsBackToLatest(t *testing.T) {
	devVersions := []string{
		"v0.7.0-58-g4273effb",
		"v0.7.0-dirty",
		"v0.7.0-3-g1234567-dirty",
		"4273effb",
	}
	for _, ver := range devVersions {
		t.Run(ver, func(t *testing.T) {
			client := forge.NewFakeClient()
			var buf bytes.Buffer
			printer := ui.New(&buf)
			layer := NewWorkflowsLayer("test-org", client, printer, "admin-user", ver)

			err := layer.Install(context.Background())
			require.NoError(t, err)

			var actionContent string
			for _, f := range client.CommittedFiles[0].Files {
				if f.Path == actionYMLPath {
					actionContent = string(f.Content)
					break
				}
			}
			require.NotEmpty(t, actionContent, "action.yml should have been written")
			assert.Contains(t, actionContent, "default: latest",
				"non-release version %q should fall back to latest", ver)
			assert.Contains(t, buf.String(), "not a release",
				"should warn about non-release version")
		})
	}
}

func TestWorkflowsLayer_Install_Error(t *testing.T) {
	client := &forge.FakeClient{
		Errors: map[string]error{
			"CommitFiles": errors.New("write failed"),
		},
	}
	layer, _ := newWorkflowsLayer(t, client)

	err := layer.Install(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write failed")
}

func TestWorkflowsLayer_Install_ExecutableModes(t *testing.T) {
	client := forge.NewFakeClient()
	layer, _ := newWorkflowsLayer(t, client)

	err := layer.Install(context.Background())
	require.NoError(t, err)

	modes := make(map[string]string)
	for _, f := range client.CommittedFiles[0].Files {
		modes[f.Path] = f.Mode
	}

	assert.Equal(t, "100755", modes["scripts/pre-triage.sh"])
	assert.Equal(t, "100755", modes["scripts/scan-secrets"])
	assert.Equal(t, "100644", modes["agents/triage.md"])
	assert.Equal(t, "100644", modes[".github/workflows/triage.yml"])
}


func TestWorkflowsLayer_Uninstall_Noop(t *testing.T) {
	client := forge.NewFakeClient()
	layer, _ := newWorkflowsLayer(t, client)

	err := layer.Uninstall(context.Background())
	require.NoError(t, err)

	// No repos deleted, no files created
	assert.Empty(t, client.DeletedRepos)
	assert.Empty(t, client.CreatedFiles)
}

func TestWorkflowsLayer_Analyze_AllPresent(t *testing.T) {
	fileContents := map[string][]byte{
		"test-org/.fullsend/CODEOWNERS": []byte("* @admin-user"),
	}
	// Populate all scaffold files
	_ = scaffold.WalkFullsendRepo(func(path string, content []byte) error {
		fileContents["test-org/.fullsend/"+path] = content
		return nil
	})

	client := &forge.FakeClient{
		FileContents: fileContents,
	}
	layer, _ := newWorkflowsLayer(t, client)

	report, err := layer.Analyze(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "workflows", report.Name)
	assert.Equal(t, StatusInstalled, report.Status)
	assert.Len(t, report.Details, len(managedFiles))
}

func TestWorkflowsLayer_Analyze_NonePresent(t *testing.T) {
	client := &forge.FakeClient{
		FileContents: map[string][]byte{},
	}
	layer, _ := newWorkflowsLayer(t, client)

	report, err := layer.Analyze(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "workflows", report.Name)
	assert.Equal(t, StatusNotInstalled, report.Status)
	assert.Len(t, report.WouldInstall, len(managedFiles))
}

func TestWorkflowsLayer_Analyze_Partial(t *testing.T) {
	client := &forge.FakeClient{
		FileContents: map[string][]byte{
			"test-org/.fullsend/.github/workflows/triage.yml": []byte("triage workflow"),
		},
	}
	layer, _ := newWorkflowsLayer(t, client)

	report, err := layer.Analyze(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "workflows", report.Name)
	assert.Equal(t, StatusDegraded, report.Status)
	// Details should list what exists
	joined := strings.Join(report.Details, " ")
	assert.Contains(t, joined, "triage.yml")
	// WouldFix should list what's missing
	assert.NotEmpty(t, report.WouldFix)
	fixJoined := strings.Join(report.WouldFix, " ")
	assert.Contains(t, fixJoined, "CODEOWNERS")
}

func TestManagedFilesMatchScaffold(t *testing.T) {
	var scaffoldPaths []string
	err := scaffold.WalkFullsendRepo(func(path string, _ []byte) error {
		scaffoldPaths = append(scaffoldPaths, path)
		return nil
	})
	require.NoError(t, err)

	for _, path := range scaffoldPaths {
		found := false
		for _, managed := range managedFiles {
			if managed == path {
				found = true
				break
			}
		}
		assert.True(t, found, "managedFiles should include scaffold file %s", path)
	}
}

func TestManagedFilesDoNotIncludeOldPlaceholders(t *testing.T) {
	for _, path := range managedFiles {
		assert.NotEqual(t, ".github/workflows/agent.yaml", path,
			"managedFiles should not include old agent.yaml placeholder")
		assert.NotEqual(t, ".github/workflows/repo-onboard.yaml", path,
			"managedFiles should not include old repo-onboard.yaml placeholder")
	}
}
