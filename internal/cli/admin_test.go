package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/layers"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func TestAdminCommand_HasSubcommands(t *testing.T) {
	cmd := newAdminCmd()
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Use] = true
	}
	assert.True(t, names["install <org>"], "expected install subcommand")
	assert.True(t, names["uninstall <org>"], "expected uninstall subcommand")
	assert.True(t, names["analyze <org>"], "expected analyze subcommand")
	assert.True(t, names["enable"], "expected enable subcommand")
	assert.True(t, names["disable"], "expected disable subcommand")
}

func TestInstallCmd_RequiresOrg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"admin", "install"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s)")
}

func TestInstallCmd_Flags(t *testing.T) {
	cmd := newInstallCmd()

	agentsFlag := cmd.Flags().Lookup("agents")
	require.NotNil(t, agentsFlag, "expected --agents flag")
	assert.Equal(t, "fullsend,triage,coder,review", agentsFlag.DefValue)

	dryRunFlag := cmd.Flags().Lookup("dry-run")
	require.NotNil(t, dryRunFlag, "expected --dry-run flag")

	skipAppSetupFlag := cmd.Flags().Lookup("skip-app-setup")
	require.NotNil(t, skipAppSetupFlag, "expected --skip-app-setup flag")

	vendorBinaryFlag := cmd.Flags().Lookup("vendor-fullsend-binary")
	require.NotNil(t, vendorBinaryFlag, "expected --vendor-fullsend-binary flag")
	assert.Equal(t, "false", vendorBinaryFlag.DefValue)

	wifProviderFlag := cmd.Flags().Lookup("gcp-wif-provider")
	require.NotNil(t, wifProviderFlag, "expected --gcp-wif-provider flag")

	wifSAEmailFlag := cmd.Flags().Lookup("gcp-wif-sa-email")
	require.NotNil(t, wifSAEmailFlag, "expected --gcp-wif-sa-email flag")

	// --repo flag should not exist (issue #495)
	repoFlag := cmd.Flags().Lookup("repo")
	assert.Nil(t, repoFlag, "--repo flag should have been removed")
}

func TestUninstallCmd_RequiresOrg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"admin", "uninstall"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s)")
}

func TestUninstallCmd_Flags(t *testing.T) {
	cmd := newUninstallCmd()

	yoloFlag := cmd.Flags().Lookup("yolo")
	require.NotNil(t, yoloFlag, "expected --yolo flag")
}

func TestAnalyzeCmd_RequiresOrg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"admin", "analyze"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s)")
}

func TestValidateOrgName_Valid(t *testing.T) {
	valid := []string{"my-org", "org123", "A", "abc-def-ghi", "ORG"}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, validateOrgName(name))
		})
	}
}

func TestValidateOrgName_Invalid(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"", "cannot be empty"},
		{"-leading", "cannot start or end with a hyphen"},
		{"trailing-", "cannot start or end with a hyphen"},
		{"invalid@char", "invalid character"},
		{"has space", "invalid character"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOrgName(tc.name)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateEnabledRepos_AllValid(t *testing.T) {
	err := validateEnabledRepos(
		[]string{"web-app", "api-server"},
		[]string{"web-app", "api-server", "docs"},
	)
	assert.NoError(t, err)
}

func TestValidateEnabledRepos_NoRepoFlag(t *testing.T) {
	err := validateEnabledRepos(nil, []string{"web-app", "docs"})
	assert.NoError(t, err)
}

func TestValidateEnabledRepos_MissingOne(t *testing.T) {
	err := validateEnabledRepos(
		[]string{"integration-service"},
		[]string{"web-app", "docs"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integration-service")
	assert.Contains(t, err.Error(), "forks, archived, or misspelled")
}

func TestValidateEnabledRepos_MultipleMissing(t *testing.T) {
	err := validateEnabledRepos(
		[]string{"web-app", "fork-repo", "archived-repo"},
		[]string{"web-app", "docs"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fork-repo")
	assert.Contains(t, err.Error(), "archived-repo")
	// web-app is valid, should not appear in the error.
	assert.NotContains(t, err.Error(), "web-app")
}

func TestValidateEnabledRepos_EmptyDiscovered(t *testing.T) {
	err := validateEnabledRepos(
		[]string{"some-repo"},
		[]string{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "some-repo")
}

func TestResolveToken_EnvVar(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token-123")
	t.Setenv("GITHUB_TOKEN", "")

	token, err := resolveToken()
	require.NoError(t, err)
	assert.Equal(t, "test-token-123", token)
}

func TestResolveToken_GitHubTokenFallback(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "github-token-456")

	token, err := resolveToken()
	require.NoError(t, err)
	assert.Equal(t, "github-token-456", token)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestEnsureConfigRepoExists_CreatesWhenMissing(t *testing.T) {
	client := forge.NewFakeClient()
	printer := ui.New(&discardWriter{})

	err := ensureConfigRepoExists(context.Background(), client, printer, "myorg")
	require.NoError(t, err)
	require.Len(t, client.CreatedRepos, 1)
	assert.Equal(t, ".fullsend", client.CreatedRepos[0].Name)
	assert.True(t, client.CreatedRepos[0].Private)
}

func TestEnsureConfigRepoExists_NoOpWhenExists(t *testing.T) {
	client := forge.NewFakeClient()
	client.Repos = []forge.Repository{
		{Name: ".fullsend", FullName: "myorg/.fullsend"},
	}
	printer := ui.New(&discardWriter{})

	err := ensureConfigRepoExists(context.Background(), client, printer, "myorg")
	require.NoError(t, err)
	assert.Empty(t, client.CreatedRepos)
}

func TestEnsureConfigRepoExists_ReturnsError(t *testing.T) {
	client := forge.NewFakeClient()
	client.Errors["GetRepo"] = fmt.Errorf("network error")
	printer := ui.New(&discardWriter{})

	err := ensureConfigRepoExists(context.Background(), client, printer, "myorg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking for config repo")
}

func TestEnableCommand_HasReposSubcommand(t *testing.T) {
	cmd := newEnableCmd()
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	assert.True(t, names["repos"], "expected repos subcommand")
}

func TestDisableCommand_HasReposSubcommand(t *testing.T) {
	cmd := newDisableCmd()
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	assert.True(t, names["repos"], "expected repos subcommand")
}

func TestReposEnableCmd_RequiresOrg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"admin", "enable", "repos"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires at least 1 arg")
}

func TestReposEnableCmd_RequiresReposOrAllFlag(t *testing.T) {
	cmd := newRootCmd()
	// Set GH_TOKEN to avoid token resolution error.
	t.Setenv("GH_TOKEN", "test-token")
	cmd.SetArgs([]string{"admin", "enable", "repos", "testorg"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must specify repository names or use --all flag")
}

func TestReposEnableCmd_HasAllFlag(t *testing.T) {
	cmd := newEnableReposCmd()
	allFlag := cmd.Flags().Lookup("all")
	require.NotNil(t, allFlag, "expected --all flag")
	assert.Equal(t, "false", allFlag.DefValue)
}

func TestReposDisableCmd_RequiresOrg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"admin", "disable", "repos"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires at least 1 arg")
}

func TestReposDisableCmd_RequiresReposOrAllFlag(t *testing.T) {
	cmd := newRootCmd()
	// Set GH_TOKEN to avoid token resolution error.
	t.Setenv("GH_TOKEN", "test-token")
	cmd.SetArgs([]string{"admin", "disable", "repos", "testorg"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must specify repository names or use --all flag")
}

func TestReposDisableCmd_HasAllFlag(t *testing.T) {
	cmd := newDisableReposCmd()
	allFlag := cmd.Flags().Lookup("all")
	require.NotNil(t, allFlag, "expected --all flag")
	assert.Equal(t, "false", allFlag.DefValue)
}

func TestReposEnableCmd_AllIgnoresPositionalArgs(t *testing.T) {
	// When --all is set, positional repo arguments are ignored
	cfg := setupTestConfig(map[string]bool{
		"web-app": false,
		"api":     false,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api"})
	printer := ui.New(&discardWriter{})

	// Pass "web-app" as a positional arg, but --all should ignore it and enable both repos
	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, true, true)
	require.NoError(t, err)

	// Verify both repos were enabled (--all behavior), not just web-app
	require.Len(t, client.CreatedFiles, 1)
	updatedCfg, err := config.ParseOrgConfig(client.CreatedFiles[0].Content)
	require.NoError(t, err)
	assert.True(t, updatedCfg.Repos["web-app"].Enabled)
	assert.True(t, updatedCfg.Repos["api"].Enabled)
}

func TestReposDisableCmd_AllIgnoresPositionalArgs(t *testing.T) {
	// When --all is set, positional repo arguments are ignored
	cfg := setupTestConfig(map[string]bool{
		"web-app": true,
		"api":     true,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api"})
	printer := ui.New(&discardWriter{})

	// Pass "web-app" as a positional arg, but --all should ignore it and disable both repos
	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, true, true)
	require.NoError(t, err)

	// Verify both repos were disabled (--all behavior), not just web-app
	require.Len(t, client.CreatedFiles, 1)
	updatedCfg, err := config.ParseOrgConfig(client.CreatedFiles[0].Content)
	require.NoError(t, err)
	assert.False(t, updatedCfg.Repos["web-app"].Enabled)
	assert.False(t, updatedCfg.Repos["api"].Enabled)
}

// Test helpers

func setupTestConfig(repos map[string]bool) *config.OrgConfig {
	repoNames := make([]string, 0, len(repos))
	enabledRepos := make([]string, 0)
	for name, enabled := range repos {
		repoNames = append(repoNames, name)
		if enabled {
			enabledRepos = append(enabledRepos, name)
		}
	}
	// Sort to ensure deterministic order despite map iteration being non-deterministic.
	sort.Strings(repoNames)
	sort.Strings(enabledRepos)
	return config.NewOrgConfig(repoNames, enabledRepos, []string{"triage"}, nil, "")
}

func setupTestClient(org string, cfg *config.OrgConfig, orgRepos []string) *forge.FakeClient {
	client := forge.NewFakeClient()
	client.Repos = []forge.Repository{
		{Name: ".fullsend", FullName: org + "/.fullsend"},
	}
	for _, name := range orgRepos {
		client.Repos = append(client.Repos, forge.Repository{
			Name:     name,
			FullName: org + "/" + name,
		})
	}
	if cfg != nil {
		cfgData, _ := cfg.Marshal()
		client.FileContents[org+"/.fullsend/config.yaml"] = cfgData
	}
	return client
}

// Business logic tests for runEnableRepos

func TestRunEnableRepos_EnableSingleRepo(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": false,
		"api":     false,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api"})
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, false, true)
	require.NoError(t, err)

	// Verify config was updated.
	require.Len(t, client.CreatedFiles, 1)
	updatedCfg, err := config.ParseOrgConfig(client.CreatedFiles[0].Content)
	require.NoError(t, err)
	assert.True(t, updatedCfg.Repos["web-app"].Enabled)
	assert.False(t, updatedCfg.Repos["api"].Enabled)
}

func TestRunEnableRepos_EnableMultipleRepos(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": false,
		"api":     false,
		"docs":    false,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api", "docs"})
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{"web-app", "docs"}, false, true)
	require.NoError(t, err)

	// Verify config was updated.
	require.Len(t, client.CreatedFiles, 1)
	updatedCfg, err := config.ParseOrgConfig(client.CreatedFiles[0].Content)
	require.NoError(t, err)
	assert.True(t, updatedCfg.Repos["web-app"].Enabled)
	assert.True(t, updatedCfg.Repos["docs"].Enabled)
	assert.False(t, updatedCfg.Repos["api"].Enabled)
}

func TestRunEnableRepos_EnableAllRepos(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": false,
		"api":     false,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api", "new-repo"})
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", nil, true, true)
	require.NoError(t, err)

	// Verify all repos were enabled (excluding .fullsend).
	require.Len(t, client.CreatedFiles, 1)
	updatedCfg, err := config.ParseOrgConfig(client.CreatedFiles[0].Content)
	require.NoError(t, err)
	assert.True(t, updatedCfg.Repos["web-app"].Enabled)
	assert.True(t, updatedCfg.Repos["api"].Enabled)
	assert.True(t, updatedCfg.Repos["new-repo"].Enabled)
	// .fullsend should not be in repos map.
	_, hasFullsend := updatedCfg.Repos[".fullsend"]
	assert.False(t, hasFullsend)
}

func TestRunEnableRepos_NoOpWhenAlreadyEnabled(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": true,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app"})
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, false, true)
	require.NoError(t, err)

	// Verify no file was created (no changes).
	assert.Empty(t, client.CreatedFiles)
}

func TestRunEnableRepos_ErrorWhenFullsendRepoMissing(t *testing.T) {
	client := forge.NewFakeClient()
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".fullsend repository not found")
}

func TestRunEnableRepos_ErrorWhenConfigMissing(t *testing.T) {
	client := setupTestClient("testorg", nil, []string{"web-app"})
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config.yaml")
}

func TestRunEnableRepos_ErrorWhenEnablingFullsend(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": false,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app"})
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{".fullsend"}, false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot enable .fullsend repository")
}

func TestRunEnableRepos_ErrorWhenRepoNotFound(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": false,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app"})
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{"nonexistent"}, false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository nonexistent not found")
}

func TestRunEnableRepos_CommitMessageFormat(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": false,
		"api":     false,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api"})
	printer := ui.New(&discardWriter{})

	err := runEnableRepos(context.Background(), client, printer, "testorg", []string{"web-app", "api"}, false, true)
	require.NoError(t, err)

	require.Len(t, client.CreatedFiles, 1)
	assert.Contains(t, client.CreatedFiles[0].Message, "chore: enable 2 repositories")
}

// Business logic tests for runDisableRepos

func TestRunDisableRepos_DisableSingleRepo(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": true,
		"api":     true,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api"})
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, false, true)
	require.NoError(t, err)

	// Verify config was updated.
	require.Len(t, client.CreatedFiles, 1)
	updatedCfg, err := config.ParseOrgConfig(client.CreatedFiles[0].Content)
	require.NoError(t, err)
	assert.False(t, updatedCfg.Repos["web-app"].Enabled)
	assert.True(t, updatedCfg.Repos["api"].Enabled)
}

func TestRunDisableRepos_DisableMultipleRepos(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": true,
		"api":     true,
		"docs":    true,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api", "docs"})
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{"web-app", "docs"}, false, true)
	require.NoError(t, err)

	// Verify config was updated.
	require.Len(t, client.CreatedFiles, 1)
	updatedCfg, err := config.ParseOrgConfig(client.CreatedFiles[0].Content)
	require.NoError(t, err)
	assert.False(t, updatedCfg.Repos["web-app"].Enabled)
	assert.False(t, updatedCfg.Repos["docs"].Enabled)
	assert.True(t, updatedCfg.Repos["api"].Enabled)
}

func TestRunDisableRepos_DisableAllRepos(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": true,
		"api":     true,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api"})
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", nil, true, true)
	require.NoError(t, err)

	// Verify all repos were disabled.
	require.Len(t, client.CreatedFiles, 1)
	updatedCfg, err := config.ParseOrgConfig(client.CreatedFiles[0].Content)
	require.NoError(t, err)
	assert.False(t, updatedCfg.Repos["web-app"].Enabled)
	assert.False(t, updatedCfg.Repos["api"].Enabled)
}

func TestRunDisableRepos_NoOpWhenAlreadyDisabled(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": false,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app"})
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, false, true)
	require.NoError(t, err)

	// Verify no file was created (no changes).
	assert.Empty(t, client.CreatedFiles)
}

func TestRunDisableRepos_ErrorWhenFullsendRepoMissing(t *testing.T) {
	client := forge.NewFakeClient()
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".fullsend repository not found")
}

func TestRunDisableRepos_ErrorWhenConfigMissing(t *testing.T) {
	client := setupTestClient("testorg", nil, []string{"web-app"})
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{"web-app"}, false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config.yaml")
}

func TestRunDisableRepos_ErrorWhenDisablingFullsend(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": true,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app"})
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{".fullsend"}, false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot disable .fullsend repository")
}

func TestRunDisableRepos_AllowsRepoNotInConfig(t *testing.T) {
	// Disable should allow repos not in config (for cleanup of deleted repos).
	cfg := setupTestConfig(map[string]bool{
		"web-app": true,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app"})
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{"nonexistent"}, false, true)
	require.NoError(t, err)
	// Should succeed but make no changes (repo not in config, nothing to disable)
	assert.Len(t, client.CreatedFiles, 0)
}

func TestRunDisableRepos_CommitMessageFormat(t *testing.T) {
	cfg := setupTestConfig(map[string]bool{
		"web-app": true,
		"api":     true,
	})
	client := setupTestClient("testorg", cfg, []string{"web-app", "api"})
	printer := ui.New(&discardWriter{})

	err := runDisableRepos(context.Background(), client, printer, "testorg", []string{"web-app", "api"}, false, true)
	require.NoError(t, err)

	require.Len(t, client.CreatedFiles, 1)
	assert.Contains(t, client.CreatedFiles[0].Message, "chore: disable 2 repositories")
}

func TestPromptEnrollment_ChooseAll(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"lowercase a", "a\n"},
		{"uppercase A", "A\n"},
		{"word all", "all\n"},
		{"word ALL", "ALL\n"},
		{"with spaces", "  a  \n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.NewReader(tc.input)
			printer := ui.New(&discardWriter{})

			enrollAll, err := promptEnrollment(printer, input)
			require.NoError(t, err)
			assert.True(t, enrollAll)
		})
	}
}

func TestPromptEnrollment_ChooseNone(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"lowercase n", "n\n"},
		{"uppercase N", "N\n"},
		{"word none", "none\n"},
		{"word NONE", "NONE\n"},
		{"with spaces", "  n  \n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.NewReader(tc.input)
			printer := ui.New(&discardWriter{})

			enrollAll, err := promptEnrollment(printer, input)
			require.NoError(t, err)
			assert.False(t, enrollAll)
		})
	}
}

func TestPromptEnrollment_RetryOnInvalidInput(t *testing.T) {
	// First input is invalid, second is valid.
	input := strings.NewReader("invalid\na\n")
	printer := ui.New(&discardWriter{})

	enrollAll, err := promptEnrollment(printer, input)
	require.NoError(t, err)
	assert.True(t, enrollAll)
}

func TestPromptEnrollment_MultipleRetriesBeforeValid(t *testing.T) {
	// Multiple invalid inputs before valid one.
	input := strings.NewReader("y\nyes\nmaybe\nn\n")
	printer := ui.New(&discardWriter{})

	enrollAll, err := promptEnrollment(printer, input)
	require.NoError(t, err)
	assert.False(t, enrollAll)
}

func TestPromptEnrollment_ErrorOnEOF(t *testing.T) {
	// EOF without any valid input.
	input := strings.NewReader("")
	printer := ui.New(&discardWriter{})

	_, err := promptEnrollment(printer, input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading enrollment choice")
}

func TestPromptEnrollment_ErrorOnReadFailure(t *testing.T) {
	// errorReader always returns an error.
	input := &errorReader{}
	printer := ui.New(&discardWriter{})

	_, err := promptEnrollment(printer, input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading enrollment choice")
}

// errorReader is a test helper that always returns an error on Read.
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("simulated read error")
}

func TestCheckInstallScopes_AllPresent(t *testing.T) {
	client := &forge.FakeClient{
		TokenScopes: []string{"repo", "workflow", "admin:org", "read:org"},
	}
	printer := ui.New(&discardWriter{})

	err := checkInstallScopes(context.Background(), client, printer)
	require.NoError(t, err)
}

func TestCheckInstallScopes_Missing(t *testing.T) {
	client := &forge.FakeClient{
		TokenScopes: []string{"repo"},
	}
	printer := ui.New(&discardWriter{})

	err := checkInstallScopes(context.Background(), client, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow")
	assert.Contains(t, err.Error(), "admin:org")
}

func TestCheckInstallScopes_FineGrainedToken(t *testing.T) {
	client := &forge.FakeClient{
		TokenScopes: nil,
	}
	printer := ui.New(&discardWriter{})

	err := checkInstallScopes(context.Background(), client, printer)
	require.NoError(t, err)
}

func TestCheckInstallScopes_GetTokenScopesError(t *testing.T) {
	client := &forge.FakeClient{
		Errors: map[string]error{"GetTokenScopes": errors.New("network error")},
	}
	printer := ui.New(&discardWriter{})

	err := checkInstallScopes(context.Background(), client, printer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking token scopes")
	assert.Contains(t, err.Error(), "network error")
}

func TestCheckInstallScopes_SyncWithLayers(t *testing.T) {
	emptyCfg := &config.OrgConfig{}
	stack := layers.NewStack(
		layers.NewConfigRepoLayer("test-org", nil, emptyCfg, ui.New(&discardWriter{}), false),
		layers.NewWorkflowsLayer("test-org", nil, ui.New(&discardWriter{}), "", ""),
		layers.NewSecretsLayer("test-org", nil, nil, ui.New(&discardWriter{})),
		layers.NewInferenceLayer("test-org", nil, nil, ui.New(&discardWriter{})),
		layers.NewDispatchTokenLayer("test-org", nil, "", nil, ui.New(&discardWriter{}), nil),
		layers.NewEnrollmentLayer("test-org", nil, nil, nil, ui.New(&discardWriter{})),
		layers.NewVendorBinaryLayer("test-org", nil, ui.New(&discardWriter{}), false, nil),
	)
	layerScopes := stack.CollectRequiredScopes(layers.OpInstall)

	assert.ElementsMatch(t, installRequiredScopes, layerScopes,
		"installRequiredScopes must match the union of RequiredScopes(OpInstall) from all layers; update the variable if a layer's scopes change")
}
