package gcf

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// healthyClient returns an *http.Client whose transport always responds 200 OK.
// Used in provisioner tests to satisfy the post-deploy health check without
// hitting a real endpoint.
func healthyClient() *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
			}, nil
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// newTestProvisioner wraps NewProvisioner with a healthy HTTP client so
// the post-deploy health check doesn't hit a real endpoint.
func newTestProvisioner(cfg Config, gcpAPI GCFClient) *Provisioner {
	p := NewProvisioner(cfg, gcpAPI)
	p.httpClient = healthyClient()
	return p
}

// fakeGCFClient records calls and returns preset responses.
type fakeGCFClient struct {
	calls []string
	errs  map[string]error

	// Return values
	projectNumber string
	functionInfo  *FunctionInfo
	functionURL   string

	// Track GetFunction call count to return different results.
	getFunctionCalls int
	// functionInfoAfterCreate is returned on the second GetFunction call
	// (after CreateFunction). If nil, functionInfo is always returned.
	functionInfoAfterCreate *FunctionInfo

	// Captured WIF provider config for assertion.
	lastWIFProviderConfig OIDCProviderConfig

	// WIF provider state for GetWIFProvider.
	wifProvider *WIFProviderInfo

	// Track secret names written via AddSecretVersion.
	secretVersionNames []string

	// Captured env vars from the last CreateFunction or UpdateFunction call.
	lastCreateFunctionEnvVars map[string]string

	// Captured project IAM binding arguments.
	projectIAMBindings []projectIAMBinding
}

type projectIAMBinding struct {
	ProjectID string
	Member    string
	Role      string
}

func newFakeGCFClient() *fakeGCFClient {
	return &fakeGCFClient{
		errs:          make(map[string]error),
		projectNumber: "123456789",
	}
}

func (f *fakeGCFClient) record(method string) error {
	f.calls = append(f.calls, method)
	return f.errs[method]
}

func (f *fakeGCFClient) CreateServiceAccount(_ context.Context, _, _, _ string) error {
	return f.record("CreateServiceAccount")
}
func (f *fakeGCFClient) CreateWIFPool(_ context.Context, _, _, _ string) error {
	return f.record("CreateWIFPool")
}
func (f *fakeGCFClient) CreateWIFProvider(_ context.Context, _, _, _ string, cfg OIDCProviderConfig) error {
	f.lastWIFProviderConfig = cfg
	return f.record("CreateWIFProvider")
}
func (f *fakeGCFClient) GetWIFProvider(_ context.Context, _, _, _ string) (*WIFProviderInfo, error) {
	f.calls = append(f.calls, "GetWIFProvider")
	if err := f.errs["GetWIFProvider"]; err != nil {
		return nil, err
	}
	return f.wifProvider, nil
}
func (f *fakeGCFClient) UpdateWIFProvider(_ context.Context, _, _, _ string, cfg OIDCProviderConfig) error {
	f.lastWIFProviderConfig = cfg
	return f.record("UpdateWIFProvider")
}
func (f *fakeGCFClient) GetSecret(_ context.Context, _, _ string) error {
	f.calls = append(f.calls, "GetSecret")
	if err := f.errs["GetSecret"]; err != nil {
		return err
	}
	return nil
}
func (f *fakeGCFClient) CreateSecret(_ context.Context, _, _ string) error {
	return f.record("CreateSecret")
}
func (f *fakeGCFClient) AddSecretVersion(_ context.Context, _ string, secretID string, _ []byte) error {
	f.secretVersionNames = append(f.secretVersionNames, secretID)
	return f.record("AddSecretVersion")
}
func (f *fakeGCFClient) SetSecretIAMBinding(_ context.Context, _, _, _ string) error {
	return f.record("SetSecretIAMBinding")
}
func (f *fakeGCFClient) SetProjectIAMBinding(_ context.Context, projectID, member, role string) error {
	f.projectIAMBindings = append(f.projectIAMBindings, projectIAMBinding{projectID, member, role})
	return f.record("SetProjectIAMBinding")
}
func (f *fakeGCFClient) SetCloudRunInvoker(_ context.Context, _, _, _ string) error {
	return f.record("SetCloudRunInvoker")
}
func (f *fakeGCFClient) GetFunction(_ context.Context, _, _, _ string) (*FunctionInfo, error) {
	f.calls = append(f.calls, "GetFunction")
	f.getFunctionCalls++
	if err := f.errs["GetFunction"]; err != nil {
		return nil, err
	}
	// On the second call (after CreateFunction), return the post-deploy info.
	if f.getFunctionCalls > 1 && f.functionInfoAfterCreate != nil {
		return f.functionInfoAfterCreate, nil
	}
	return f.functionInfo, nil
}
func (f *fakeGCFClient) UploadFunctionSource(_ context.Context, _, _ string, _ []byte) (json.RawMessage, error) {
	f.calls = append(f.calls, "UploadFunctionSource")
	if err := f.errs["UploadFunctionSource"]; err != nil {
		return nil, err
	}
	return json.RawMessage(`{"bucket":"test-bucket","object":"source.zip"}`), nil
}
func (f *fakeGCFClient) CreateFunction(_ context.Context, _, _, _ string, cfg FunctionConfig) (string, error) {
	f.calls = append(f.calls, "CreateFunction")
	f.lastCreateFunctionEnvVars = cfg.EnvVars
	if err := f.errs["CreateFunction"]; err != nil {
		return "", err
	}
	return "operations/123", nil
}
func (f *fakeGCFClient) UpdateFunction(_ context.Context, _, _, _ string, cfg FunctionConfig) (string, error) {
	f.calls = append(f.calls, "UpdateFunction")
	f.lastCreateFunctionEnvVars = cfg.EnvVars
	if err := f.errs["UpdateFunction"]; err != nil {
		return "", err
	}
	return "operations/update-456", nil
}
func (f *fakeGCFClient) WaitForOperation(_ context.Context, _ string) error {
	return f.record("WaitForOperation")
}
func (f *fakeGCFClient) GetProjectNumber(_ context.Context, _ string) (string, error) {
	f.calls = append(f.calls, "GetProjectNumber")
	if err := f.errs["GetProjectNumber"]; err != nil {
		return "", err
	}
	return f.projectNumber, nil
}

// --- helpers ---

func fakeFunctionSourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.23\n"), 0644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package function\n"), 0644)
	return dir
}

func singleRolePEMs() map[string][]byte {
	return map[string][]byte{"coder": []byte("test-pem-data")}
}

func singleRoleAppIDs() map[string]string {
	return map[string]string{"coder": "12345"}
}

func multiRolePEMs() map[string][]byte {
	return map[string][]byte{
		"coder":   []byte("coder-pem"),
		"triage":  []byte("triage-pem"),
	}
}

func multiRoleAppIDs() map[string]string {
	return map[string]string{
		"coder":  "12345",
		"triage": "67890",
	}
}

// --- unit tests ---

func TestProvisioner_Name(t *testing.T) {
	p := newTestProvisioner(Config{}, nil)
	assert.Equal(t, "gcf", p.Name())
}

func TestProvisioner_OrgSecretNames(t *testing.T) {
	p := newTestProvisioner(Config{}, nil)
	assert.Nil(t, p.OrgSecretNames())
}

func TestProvisioner_OrgVariableNames(t *testing.T) {
	p := newTestProvisioner(Config{}, nil)
	assert.Equal(t, []string{"FULLSEND_MINT_URL"}, p.OrgVariableNames())
}

func TestProvisioner_DefaultConfig(t *testing.T) {
	p := newTestProvisioner(Config{}, nil)
	assert.Equal(t, "us-central1", p.cfg.Region)
	assert.Equal(t, "fullsend-pool", p.cfg.WIFPoolName)
	assert.Equal(t, "github-oidc", p.cfg.WIFProvider)
}

func TestProvisioner_CustomConfig(t *testing.T) {
	p := newTestProvisioner(Config{
		Region:      "europe-west1",
		WIFPoolName: "custom-pool",
		WIFProvider: "custom-prov",
	}, nil)
	assert.Equal(t, "europe-west1", p.cfg.Region)
	assert.Equal(t, "custom-pool", p.cfg.WIFPoolName)
	assert.Equal(t, "custom-prov", p.cfg.WIFProvider)
}

func TestSecretID(t *testing.T) {
	assert.Equal(t, "fullsend-test-org--coder-app-pem", secretID("test-org", "coder"))
	assert.Equal(t, "fullsend-acme--triage-app-pem", secretID("acme", "triage"))
}

// --- StoreAgentPEM tests ---

func TestStoreAgentPEM_CreatesNewSecret(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound

	p := newTestProvisioner(Config{ProjectID: "my-project"}, fake)
	err := p.StoreAgentPEM(context.Background(), "test-org", "coder", []byte("pem-data"))
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GetSecret",
		"CreateSecret",
		"AddSecretVersion",
		"SetSecretIAMBinding",
	}, fake.calls)
}

func TestStoreAgentPEM_ExistingSecretSkipsCreate(t *testing.T) {
	fake := newFakeGCFClient()

	p := newTestProvisioner(Config{ProjectID: "my-project"}, fake)
	err := p.StoreAgentPEM(context.Background(), "test-org", "coder", []byte("pem-data"))
	require.NoError(t, err)

	assert.Contains(t, fake.calls, "GetSecret")
	assert.NotContains(t, fake.calls, "CreateSecret")
	assert.Contains(t, fake.calls, "AddSecretVersion")
	assert.Contains(t, fake.calls, "SetSecretIAMBinding")
}

func TestStoreAgentPEM_MissingProjectID(t *testing.T) {
	p := newTestProvisioner(Config{}, newFakeGCFClient())
	err := p.StoreAgentPEM(context.Background(), "test-org", "coder", []byte("pem"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GCP project ID is required")
}

func TestStoreAgentPEM_InvalidRole(t *testing.T) {
	p := newTestProvisioner(Config{ProjectID: "my-project"}, newFakeGCFClient())
	for _, role := range []string{"CODER", "co der", "../escape", "role;drop"} {
		err := p.StoreAgentPEM(context.Background(), "test-org", role, []byte("pem"))
		require.Error(t, err, "role %q should be rejected", role)
		assert.Contains(t, err.Error(), "invalid role name")
	}
	err := p.StoreAgentPEM(context.Background(), "test-org", "coder", []byte("pem"))
	require.NoError(t, err)
}

func TestStoreAgentPEM_GetSecretNonNotFoundError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = fmt.Errorf("permission denied")

	p := newTestProvisioner(Config{ProjectID: "my-project"}, fake)
	err := p.StoreAgentPEM(context.Background(), "test-org", "coder", []byte("pem"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

// --- self-managed provision tests ---

func TestProvisioner_Provision_FullFlow(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound
	fake.functionInfoAfterCreate = &FunctionInfo{
		Name:  "projects/my-project/locations/us-central1/functions/fullsend-mint",
		State: "ACTIVE",
		URI:   "https://fullsend-mint-abc123.run.app",
	}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)

	expected := []string{
		"GetProjectNumber",
		"CreateServiceAccount",
		"CreateWIFPool",
		"GetWIFProvider",
		"CreateWIFProvider",
		"SetProjectIAMBinding",
		"GetSecret",
		"CreateSecret",
		"AddSecretVersion",
		"SetSecretIAMBinding",
		"GetFunction",
		"UploadFunctionSource",
		"CreateFunction",
		"WaitForOperation",
		"GetFunction",
		"SetCloudRunInvoker",
	}
	assert.Equal(t, expected, fake.calls)

	require.Contains(t, vars, "FULLSEND_MINT_URL")
	assert.Equal(t, "https://fullsend-mint-abc123.run.app", vars["FULLSEND_MINT_URL"])

	// Verify project IAM binding arguments.
	require.Len(t, fake.projectIAMBindings, 1)
	assert.Equal(t, "my-project", fake.projectIAMBindings[0].ProjectID)
	assert.Equal(t, "roles/aiplatform.user", fake.projectIAMBindings[0].Role)
	assert.Contains(t, fake.projectIAMBindings[0].Member, "principalSet://iam.googleapis.com/")
	assert.Contains(t, fake.projectIAMBindings[0].Member, "attribute.repository/test-org/.fullsend")

	// Verify PEMs were zeroed.
	for role, pem := range p.cfg.AgentPEMs {
		for _, b := range pem {
			if b != 0 {
				t.Fatalf("PEM for role %s was not zeroed after provisioning", role)
			}
		}
	}
}

func TestProvisioner_Provision_MultiRole(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound
	fake.functionInfoAfterCreate = &FunctionInfo{
		URI: "https://fullsend-mint-abc123.run.app",
	}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         multiRolePEMs(),
		AgentAppIDs:       multiRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://fullsend-mint-abc123.run.app", vars["FULLSEND_MINT_URL"])

	// Each role should trigger GetSecret+CreateSecret+AddSecretVersion+SetSecretIAMBinding.
	getSecretCount := 0
	createSecretCount := 0
	addVersionCount := 0
	iamCount := 0
	for _, call := range fake.calls {
		switch call {
		case "GetSecret":
			getSecretCount++
		case "CreateSecret":
			createSecretCount++
		case "AddSecretVersion":
			addVersionCount++
		case "SetSecretIAMBinding":
			iamCount++
		}
	}
	assert.Equal(t, 2, getSecretCount)
	assert.Equal(t, 2, createSecretCount)
	assert.Equal(t, 2, addVersionCount)
	assert.Equal(t, 2, iamCount)
}

func TestProvisioner_Provision_ExistingFunction(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfo = &FunctionInfo{
		Name:  "projects/my-project/locations/us-central1/functions/fullsend-mint",
		State: "ACTIVE",
		URI:   "https://us-central1-my-project.cloudfunctions.net/fullsend-mint",
	}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.Contains(t, fake.calls, "UpdateFunction")
	assert.NotContains(t, fake.calls, "CreateFunction")
	assert.NotContains(t, fake.calls, "CreateSecret")
	assert.Contains(t, fake.calls, "AddSecretVersion")
	assert.Contains(t, fake.calls, "SetSecretIAMBinding")
	assert.Contains(t, fake.calls, "SetCloudRunInvoker")

	assert.Equal(t, "https://us-central1-my-project.cloudfunctions.net/fullsend-mint", vars["FULLSEND_MINT_URL"])
}

func TestProvisioner_Provision_SkipsRedeployWhenUnchanged(t *testing.T) {
	srcDir := fakeFunctionSourceDir(t)
	sourceZip, err := bundleFunctionSource(srcDir)
	require.NoError(t, err)
	srcHash := sha256Hex(sourceZip)

	fake := newFakeGCFClient()
	fake.functionInfo = &FunctionInfo{
		Name:  "projects/my-project/locations/us-central1/functions/fullsend-mint",
		State: "ACTIVE",
		URI:   "https://fullsend-mint-abc123.run.app",
		EnvVars: map[string]string{
			"GCP_PROJECT_NUMBER":   "123456789",
			"WIF_POOL_NAME":       "fullsend-pool",
			"WIF_PROVIDER_NAME":   "github-oidc",
			"ALLOWED_ORGS":        "test-org",
			"OIDC_AUDIENCE":       "fullsend-mint",
			"ALLOWED_ROLES":       "coder",
			"ROLE_APP_IDS":        `{"test-org/coder":"12345"}`,
			"FULLSEND_SOURCE_HASH": srcHash,
		},
	}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: srcDir,
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.NotContains(t, fake.calls, "UploadFunctionSource")
	assert.NotContains(t, fake.calls, "CreateFunction")
	assert.NotContains(t, fake.calls, "UpdateFunction")
	assert.NotContains(t, fake.calls, "WaitForOperation")

	assert.Equal(t, "https://fullsend-mint-abc123.run.app", vars["FULLSEND_MINT_URL"])
}

func TestProvisioner_Provision_ForceDeployAlwaysDeploys(t *testing.T) {
	srcDir := fakeFunctionSourceDir(t)
	sourceZip, err := bundleFunctionSource(srcDir)
	require.NoError(t, err)
	srcHash := sha256Hex(sourceZip)

	fake := newFakeGCFClient()
	fake.functionInfo = &FunctionInfo{
		Name:  "projects/my-project/locations/us-central1/functions/fullsend-mint",
		State: "ACTIVE",
		URI:   "https://fullsend-mint-abc123.run.app",
		EnvVars: map[string]string{
			"GCP_PROJECT_NUMBER":   "123456789",
			"WIF_POOL_NAME":       "fullsend-pool",
			"WIF_PROVIDER_NAME":   "github-oidc",
			"ALLOWED_ORGS":        "test-org",
			"OIDC_AUDIENCE":       "fullsend-mint",
			"ALLOWED_ROLES":       "coder",
			"ROLE_APP_IDS":        `{"test-org/coder":"12345"}`,
			"FULLSEND_SOURCE_HASH": srcHash,
		},
	}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: srcDir,
		DeployMode:        DeployForce,
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.Contains(t, fake.calls, "UploadFunctionSource")
	assert.Contains(t, fake.calls, "UpdateFunction")
	assert.Contains(t, fake.calls, "WaitForOperation")
	assert.Equal(t, "https://fullsend-mint-abc123.run.app", vars["FULLSEND_MINT_URL"])
}

func TestProvisioner_Provision_SkipDeployReusesExisting(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfo = &FunctionInfo{
		Name:  "projects/my-project/locations/us-central1/functions/fullsend-mint",
		State: "ACTIVE",
		URI:   "https://fullsend-mint-abc123.run.app",
	}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
		DeployMode:        DeploySkip,
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.NotContains(t, fake.calls, "UploadFunctionSource")
	assert.NotContains(t, fake.calls, "CreateFunction")
	assert.NotContains(t, fake.calls, "UpdateFunction")
	assert.NotContains(t, fake.calls, "WaitForOperation")
	assert.Equal(t, "https://fullsend-mint-abc123.run.app", vars["FULLSEND_MINT_URL"])
}

func TestProvisioner_Provision_SkipDeployNoExistingFunction(t *testing.T) {
	fake := newFakeGCFClient()

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
		DeployMode:        DeploySkip,
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--skip-mint-deploy")
}

func TestProvisioner_Provision_SecretExistsSkipsCreation(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfo = &FunctionInfo{
		URI: "https://fullsend-mint-abc123.run.app",
	}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.Contains(t, fake.calls, "GetSecret")
	assert.NotContains(t, fake.calls, "CreateSecret")
	assert.Contains(t, fake.calls, "AddSecretVersion")
	assert.Contains(t, fake.calls, "SetSecretIAMBinding")
}

func TestProvisioner_Provision_SecretNotFoundCreatesNew(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound
	fake.functionInfo = &FunctionInfo{
		URI: "https://fullsend-mint-abc123.run.app",
	}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.Contains(t, fake.calls, "GetSecret")
	assert.Contains(t, fake.calls, "CreateSecret")
	assert.Contains(t, fake.calls, "AddSecretVersion")
	assert.Contains(t, fake.calls, "SetSecretIAMBinding")
}

// --- bundled mode tests ---

func TestProvisioner_Provision_BundledMode(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound

	p := newTestProvisioner(Config{
		ProjectID: "shared-project",
		GitHubOrgs: []string{"test-org"},
		AgentPEMs: singleRolePEMs(),
		MintURL:   "https://fullsend-mint-shared.run.app",
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "https://fullsend-mint-shared.run.app", vars["FULLSEND_MINT_URL"])

	// Bundled mode should store PEMs but skip all infra calls.
	assert.NotContains(t, fake.calls, "GetProjectNumber")
	assert.NotContains(t, fake.calls, "CreateServiceAccount")
	assert.NotContains(t, fake.calls, "CreateWIFPool")
	assert.NotContains(t, fake.calls, "CreateFunction")
	assert.Contains(t, fake.calls, "GetSecret")
	assert.Contains(t, fake.calls, "CreateSecret")
	assert.Contains(t, fake.calls, "AddSecretVersion")
}

func TestProvisioner_Provision_BundledMode_MissingProjectID(t *testing.T) {
	p := newTestProvisioner(Config{
		GitHubOrgs: []string{"test-org"},
		AgentPEMs: singleRolePEMs(),
		MintURL:   "https://fullsend-mint-shared.run.app",
	}, newFakeGCFClient())

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GCP project ID is required")
}

func TestProvisioner_Provision_BundledMode_InvalidMintURL(t *testing.T) {
	tests := []struct {
		name    string
		mintURL string
	}{
		{"HTTP not HTTPS", "http://mint.example.com"},
		{"no scheme", "mint.example.com"},
		{"empty host", "https://"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProvisioner(Config{
				ProjectID: "shared-project",
				GitHubOrgs: []string{"test-org"},
				AgentPEMs: singleRolePEMs(),
				MintURL:   tc.mintURL,
			}, newFakeGCFClient())

			_, err := p.Provision(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "must be a valid HTTPS URL")
		})
	}
}

// --- validation error tests ---

func TestProvisioner_Provision_MissingProjectID(t *testing.T) {
	p := newTestProvisioner(Config{
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, newFakeGCFClient())

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GCP project ID is required")
}

func TestProvisioner_Provision_MissingGitHubOrg(t *testing.T) {
	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, newFakeGCFClient())

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one GitHub org is required")
}

func TestProvisioner_Provision_NoPEMs_SecretsExist(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfo = &FunctionInfo{URI: "https://fullsend-mint-abc123.run.app"}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         nil,
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://fullsend-mint-abc123.run.app", vars["FULLSEND_MINT_URL"])

	assert.NotContains(t, fake.calls, "AddSecretVersion")
	assert.Contains(t, fake.calls, "GetSecret")
	assert.Contains(t, fake.calls, "UpdateFunction")
}

func TestProvisioner_Provision_NoPEMs_SecretsMissing(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         nil,
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no PEM and secret")
}

func TestProvisioner_Provision_PartialPEMs(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfo = &FunctionInfo{URI: "https://fullsend-mint-abc123.run.app"}

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         map[string][]byte{"coder": []byte("coder-pem")},
		AgentAppIDs:       multiRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://fullsend-mint-abc123.run.app", vars["FULLSEND_MINT_URL"])

	addVersionCount := 0
	getSecretCount := 0
	for _, call := range fake.calls {
		switch call {
		case "AddSecretVersion":
			addVersionCount++
		case "GetSecret":
			getSecretCount++
		}
	}
	assert.Equal(t, 1, addVersionCount, "only coder PEM should be stored")
	assert.GreaterOrEqual(t, getSecretCount, 2, "GetSecret for coder PEM store + triage secret verify")
}

func TestProvisioner_Provision_NoPEMs_APIError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = fmt.Errorf("permission denied")

	p := newTestProvisioner(Config{
		ProjectID:         "my-project",
		GitHubOrgs:        []string{"test-org"},
		AgentPEMs:         nil,
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking secret")
	assert.Contains(t, err.Error(), "permission denied")
}

func TestProvisioner_Provision_BundledMode_NoPEMs_SecretsExist(t *testing.T) {
	fake := newFakeGCFClient()

	p := newTestProvisioner(Config{
		ProjectID:  "shared-project",
		GitHubOrgs: []string{"test-org"},
		AgentPEMs:  nil,
		AgentAppIDs: singleRoleAppIDs(),
		MintURL:    "https://fullsend-mint-shared.run.app",
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://fullsend-mint-shared.run.app", vars["FULLSEND_MINT_URL"])

	assert.NotContains(t, fake.calls, "AddSecretVersion")
	assert.Contains(t, fake.calls, "GetSecret")
}

func TestProvisioner_Provision_BundledMode_NoPEMs_SecretsMissing(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound

	p := newTestProvisioner(Config{
		ProjectID:  "shared-project",
		GitHubOrgs: []string{"test-org"},
		AgentPEMs:  nil,
		AgentAppIDs: singleRoleAppIDs(),
		MintURL:    "https://fullsend-mint-shared.run.app",
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no PEM and secret")
}

func TestProvisioner_Provision_BundledMode_NoPEMs_APIError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = fmt.Errorf("permission denied")

	p := newTestProvisioner(Config{
		ProjectID:  "shared-project",
		GitHubOrgs: []string{"test-org"},
		AgentPEMs:  nil,
		AgentAppIDs: singleRoleAppIDs(),
		MintURL:    "https://fullsend-mint-shared.run.app",
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking secret")
	assert.Contains(t, err.Error(), "permission denied")
}

func TestProvisioner_Provision_BundledMode_PartialPEMs(t *testing.T) {
	fake := newFakeGCFClient()

	p := newTestProvisioner(Config{
		ProjectID:   "shared-project",
		GitHubOrgs:  []string{"test-org"},
		AgentPEMs:   map[string][]byte{"coder": []byte("coder-pem")},
		AgentAppIDs: multiRoleAppIDs(),
		MintURL:     "https://fullsend-mint-shared.run.app",
	}, fake)

	vars, err := p.Provision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://fullsend-mint-shared.run.app", vars["FULLSEND_MINT_URL"])

	addVersionCount := 0
	getSecretCount := 0
	for _, call := range fake.calls {
		switch call {
		case "AddSecretVersion":
			addVersionCount++
		case "GetSecret":
			getSecretCount++
		}
	}
	assert.Equal(t, 1, addVersionCount, "only coder PEM should be stored")
	assert.GreaterOrEqual(t, getSecretCount, 2, "GetSecret for coder PEM store + triage secret verify")
}

func TestProvisioner_Provision_MissingAppIDs(t *testing.T) {
	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, newFakeGCFClient())

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one agent App ID is required")
}

func TestProvisioner_Provision_PEMWithoutAppID(t *testing.T) {
	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         map[string][]byte{"coder": []byte("pem"), "review": []byte("pem")},
		AgentAppIDs:       map[string]string{"coder": "123"},
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, newFakeGCFClient())

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has a PEM but no corresponding App ID")
}

func TestProvisioner_Provision_DuplicateOrgs(t *testing.T) {
	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"acme", "ACME"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, newFakeGCFClient())

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate GitHub org")
}

func TestProvisioner_Provision_InvalidGitHubOrg(t *testing.T) {
	tests := []struct {
		name string
		org  string
	}{
		{"injection attempt", "org'; DROP TABLE --"},
		{"starts with hyphen", "-org"},
		{"ends with hyphen", "org-"},
		{"special chars", "org/evil"},
		{"spaces", "my org"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProvisioner(Config{
				ProjectID:         "test-project-id",
				GitHubOrgs:        []string{tc.org},
				AgentPEMs:         singleRolePEMs(),
				AgentAppIDs:       singleRoleAppIDs(),
				FunctionSourceDir: fakeFunctionSourceDir(t),
			}, newFakeGCFClient())

			_, err := p.Provision(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid GitHub org name")
		})
	}
}

// --- GCP API error tests ---

func TestProvisioner_Provision_GetProjectNumberError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetProjectNumber"] = fmt.Errorf("permission denied")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestProvisioner_Provision_CreateSAError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["CreateServiceAccount"] = fmt.Errorf("quota exceeded")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota exceeded")
}

func TestProvisioner_Provision_CreateWIFPoolError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["CreateWIFPool"] = fmt.Errorf("pool error")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pool error")
}

func TestProvisioner_Provision_CreateWIFProviderError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["CreateWIFProvider"] = fmt.Errorf("provider error")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider error")
}

func TestProvisioner_Provision_CreateSecretError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound
	fake.errs["CreateSecret"] = fmt.Errorf("secret error")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret error")
}

func TestProvisioner_Provision_AddSecretVersionError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetSecret"] = ErrSecretNotFound
	fake.errs["AddSecretVersion"] = fmt.Errorf("version error")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version error")
}

func TestProvisioner_Provision_SetProjectIAMBindingError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["SetProjectIAMBinding"] = fmt.Errorf("project iam denied")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "granting Vertex AI access for org org")
	assert.Contains(t, err.Error(), "project iam denied")
}

func TestProvisioner_Provision_MultiOrg_ProjectIAMBindings(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfoAfterCreate = &FunctionInfo{URI: "https://mint.run.app"}

	p := newTestProvisioner(Config{
		ProjectID:         "shared-project",
		GitHubOrgs:        []string{"org-a", "org-b"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)

	require.Len(t, fake.projectIAMBindings, 2)
	assert.Contains(t, fake.projectIAMBindings[0].Member, "attribute.repository/org-a/.fullsend")
	assert.Contains(t, fake.projectIAMBindings[1].Member, "attribute.repository/org-b/.fullsend")
	assert.Equal(t, "roles/aiplatform.user", fake.projectIAMBindings[0].Role)
	assert.Equal(t, "roles/aiplatform.user", fake.projectIAMBindings[1].Role)
}

func TestProvisioner_Provision_SetIAMBindingError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["SetSecretIAMBinding"] = fmt.Errorf("iam error")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iam error")
}

func TestProvisioner_Provision_CreateFunctionError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["CreateFunction"] = fmt.Errorf("deploy failed")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploy failed")
}

func TestProvisioner_Provision_GetFunctionError(t *testing.T) {
	fake := newFakeGCFClient()
	fake.errs["GetFunction"] = fmt.Errorf("function check failed")

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "function check failed")
}

// --- bundleFunctionSource tests ---

func TestBundleFunctionSource_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := bundleFunctionSource(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no deployable source files")
}

func TestBundleFunctionSource_MissingGoMod(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/main.go", []byte("package main"), 0644)
	_, err := bundleFunctionSource(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing go.mod")
}

func TestBundleFunctionSource_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/main.go", []byte("package main"), 0644)
	os.WriteFile(dir+"/go.mod", []byte("module test"), 0644)
	os.WriteFile(dir+"/main_test.go", []byte("package main"), 0644)
	os.WriteFile(dir+"/.hidden", []byte("hidden"), 0644)

	data, err := bundleFunctionSource(dir)
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)

	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	assert.Contains(t, names, "main.go")
	assert.Contains(t, names, "go.mod")
	assert.NotContains(t, names, "main_test.go")
	assert.NotContains(t, names, ".hidden")
}

func TestBundleFunctionSource_EmptyPath(t *testing.T) {
	_, err := bundleFunctionSource("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "function source directory not configured")
}

// --- multi-org tests ---

func TestProvisioner_Provision_MultiOrg_WIFCondition(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfoAfterCreate = &FunctionInfo{URI: "https://mint.run.app"}

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"acme", "widgetco"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "assertion.repository in ['acme/.fullsend', 'widgetco/.fullsend']",
		fake.lastWIFProviderConfig.AttributeCondition)

	expectedIAMAudience := "https://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/fullsend-pool/providers/github-oidc"
	assert.Equal(t, []string{"fullsend-mint", expectedIAMAudience},
		fake.lastWIFProviderConfig.AllowedAudiences)
}

func TestProvisioner_Provision_SingleOrg_WIFCondition(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfoAfterCreate = &FunctionInfo{URI: "https://mint.run.app"}

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"acme"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "assertion.repository == 'acme/.fullsend'",
		fake.lastWIFProviderConfig.AttributeCondition)

	expectedIAMAudience := "https://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/fullsend-pool/providers/github-oidc"
	assert.Equal(t, []string{"fullsend-mint", expectedIAMAudience},
		fake.lastWIFProviderConfig.AllowedAudiences)
}

func TestProvisioner_Provision_WIF_AllowedAudiences(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfoAfterCreate = &FunctionInfo{URI: "https://mint.run.app"}

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"acme"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{
		"fullsend-mint",
		"https://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/fullsend-pool/providers/github-oidc",
	}, fake.lastWIFProviderConfig.AllowedAudiences)
}

func TestProvisioner_Provision_MultiOrg_PEMStorage(t *testing.T) {
	fake := newFakeGCFClient()
	fake.functionInfoAfterCreate = &FunctionInfo{URI: "https://mint.run.app"}

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"org1", "org2"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)

	// PEMs are stored per org×role (org-scoped secrets), so 2 orgs × 1 role = 2 GetSecret + 2 AddSecretVersion.
	getSecretCount := 0
	addVersionCount := 0
	for _, call := range fake.calls {
		if call == "GetSecret" {
			getSecretCount++
		}
		if call == "AddSecretVersion" {
			addVersionCount++
		}
	}
	assert.Equal(t, 2, getSecretCount, "expected GetSecret called once per org×role")
	assert.Equal(t, 2, addVersionCount, "expected AddSecretVersion called once per org×role")
}

func TestProvisioner_Provision_MultiOrg_MergeDoesNotOverwriteExistingPEMs(t *testing.T) {
	fake := newFakeGCFClient()
	// Simulate an existing deployed function from a previous org's install.
	fake.functionInfo = &FunctionInfo{
		URI:     "https://mint.run.app",
		EnvVars: map[string]string{"ROLE_APP_IDS": `{"existing-org/coder":"999"}`},
	}
	// Simulate existing WIF provider with existing-org already configured.
	fake.wifProvider = &WIFProviderInfo{
		AttributeCondition: "assertion.repository_owner == 'existing-org'",
	}

	p := newTestProvisioner(Config{
		ProjectID:         "test-project-id",
		GitHubOrgs:        []string{"new-org"},
		AgentPEMs:         singleRolePEMs(),
		AgentAppIDs:       singleRoleAppIDs(),
		FunctionSourceDir: fakeFunctionSourceDir(t),
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)

	// PEMs must only be stored for new-org, not for existing-org.
	require.NotEmpty(t, fake.secretVersionNames, "expected at least one PEM to be stored")
	for _, name := range fake.secretVersionNames {
		assert.Contains(t, name, "new-org", "PEM should only be stored for installing org")
		assert.NotContains(t, name, "existing-org", "PEM must not overwrite existing org's secrets")
	}

	// WIF condition should include both orgs.
	assert.Equal(t, "assertion.repository in ['existing-org/.fullsend', 'new-org/.fullsend']",
		fake.lastWIFProviderConfig.AttributeCondition)

	// ROLE_APP_IDS should preserve existing-org's entries and add new-org's.
	assert.Contains(t, fake.lastCreateFunctionEnvVars["ROLE_APP_IDS"], `"existing-org/coder":"999"`)
	assert.Contains(t, fake.lastCreateFunctionEnvVars["ROLE_APP_IDS"], `"new-org/coder"`)
}

// --- interface compliance ---

func TestProvisioner_ImplementsDispatcher(t *testing.T) {
	var _ interface {
		Name() string
		Provision(context.Context) (map[string]string, error)
		StoreAgentPEM(context.Context, string, string, []byte) error
		OrgSecretNames() []string
		OrgVariableNames() []string
	} = (*Provisioner)(nil)
}
