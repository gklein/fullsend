// Package gcf implements the dispatch.Dispatcher interface using a GCP
// Cloud Function as the token mint. The mint validates GitHub OIDC tokens
// via Workload Identity Federation and issues scoped installation tokens
// for each agent role.
package gcf

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fullsend-ai/fullsend/internal/dispatch"
)

// DeployMode controls Cloud Function deployment behavior.
type DeployMode int

const (
	// DeployAuto compares source hash and env vars; skips deploy if unchanged.
	DeployAuto DeployMode = iota
	// DeploySkip never redeploys; reuses the existing function URL.
	DeploySkip
	// DeployForce always redeploys regardless of changes.
	DeployForce
)

// Compile-time check that Provisioner implements dispatch.Dispatcher.
var _ dispatch.Dispatcher = (*Provisioner)(nil)

// DefaultFunctionSourceDir returns the default path to the Cloud Function
// source directory. This assumes the CLI is run from the repository root.
func DefaultFunctionSourceDir() string {
	return filepath.Join("internal", "mint")
}

// githubOrgPattern validates GitHub organization names.
var githubOrgPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// gcpProjectIDPattern validates GCP project IDs (6-30 chars).
var gcpProjectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// gcpRegionPattern validates GCP region names (e.g. us-central1, europe-west4).
var gcpRegionPattern = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)

// rolePattern validates agent role names (must match Secret Manager ID constraints).
var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

const (
	saName          = "fullsend-dispatch"
	defaultPool     = "fullsend-pool"
	defaultProvider = "github-oidc"
	defaultRegion   = "us-central1"
	oidcIssuer      = "https://token.actions.githubusercontent.com"
	oidcAudience    = "fullsend-mint"
	functionName    = "fullsend-mint"
)

// Config holds the inputs for GCF mint provisioning.
type Config struct {
	ProjectID         string
	Region            string // default: "us-central1"
	WIFPoolName       string // default: "fullsend-pool"
	WIFProvider       string // default: "github-oidc"
	GitHubOrgs        []string
	FunctionSourceDir string // path to Cloud Function source directory

	// AgentPEMs maps role → PEM private key data for all agent Apps.
	AgentPEMs map[string][]byte

	// AgentAppIDs maps role → GitHub App ID for all agent Apps.
	AgentAppIDs map[string]string

	// MintURL, if set, skips infrastructure deployment and uses the
	// existing mint at this URL. Only PEMs are stored.
	MintURL string

	// DeployMode controls function deployment: auto (default), skip, or force.
	DeployMode DeployMode
}

// Provisioner creates GCP infrastructure for OIDC-based token minting.
type Provisioner struct {
	cfg        Config
	gcpAPI     GCFClient
	httpClient *http.Client // for health checks; nil uses http.DefaultClient
}

// NewProvisioner creates a new Provisioner with defaults applied.
func NewProvisioner(cfg Config, gcpAPI GCFClient) *Provisioner {
	if cfg.Region == "" {
		cfg.Region = defaultRegion
	}
	if cfg.WIFPoolName == "" {
		cfg.WIFPoolName = defaultPool
	}
	if cfg.WIFProvider == "" {
		cfg.WIFProvider = defaultProvider
	}
	return &Provisioner{cfg: cfg, gcpAPI: gcpAPI, httpClient: http.DefaultClient}
}

// Name returns the dispatcher identifier.
func (p *Provisioner) Name() string {
	return "gcf"
}

// OrgSecretNames returns nil — the mint uses Secret Manager, not org secrets.
func (p *Provisioner) OrgSecretNames() []string {
	return nil
}

// OrgVariableNames returns the org variables this dispatcher manages.
func (p *Provisioner) OrgVariableNames() []string {
	return []string{"FULLSEND_MINT_URL"}
}

// secretID returns the Secret Manager secret ID for the given role.
func secretID(role string) string {
	return fmt.Sprintf("fullsend-%s-app-pem", role)
}

// SecretExists checks whether the Secret Manager secret for the given role exists.
func (p *Provisioner) SecretExists(ctx context.Context, role string) (bool, error) {
	sid := secretID(role)
	err := p.gcpAPI.GetSecret(ctx, p.cfg.ProjectID, sid)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrSecretNotFound) {
		return false, nil
	}
	return false, fmt.Errorf("checking secret %s: %w", sid, err)
}

// StoreAgentPEM persists a single role's PEM in Secret Manager.
// Called during App setup so each PEM is stored immediately after creation.
func (p *Provisioner) StoreAgentPEM(ctx context.Context, role string, pemData []byte) error {
	if p.cfg.ProjectID == "" {
		return fmt.Errorf("GCP project ID is required")
	}
	if !rolePattern.MatchString(role) {
		return fmt.Errorf("invalid role name %q: must match %s", role, rolePattern.String())
	}

	sid := secretID(role)

	secretErr := p.gcpAPI.GetSecret(ctx, p.cfg.ProjectID, sid)
	if secretErr != nil {
		if !errors.Is(secretErr, ErrSecretNotFound) {
			return fmt.Errorf("checking secret %s: %w", sid, secretErr)
		}
		if err := p.gcpAPI.CreateSecret(ctx, p.cfg.ProjectID, sid); err != nil {
			return fmt.Errorf("creating secret %s: %w", sid, err)
		}
	}

	if err := p.gcpAPI.AddSecretVersion(ctx, p.cfg.ProjectID, sid, pemData); err != nil {
		return fmt.Errorf("adding secret version for %s: %w", sid, err)
	}

	saEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", saName, p.cfg.ProjectID)
	secretResource := fmt.Sprintf("projects/%s/secrets/%s", p.cfg.ProjectID, sid)
	if err := p.gcpAPI.SetSecretIAMBinding(ctx, secretResource,
		"serviceAccount:"+saEmail, "roles/secretmanager.secretAccessor"); err != nil {
		return fmt.Errorf("granting secret access for %s: %w", sid, err)
	}

	return nil
}

// Provision creates the GCP infrastructure for the token mint.
//
// When MintURL is empty, deploys the full mint infrastructure:
//  1. Look up project number
//  2. Create/verify service account
//  3. Create/verify WIF pool + provider
//  4. Store all agent PEMs in Secret Manager
//  5. Grant SA access to all role secrets
//  6. Deploy Cloud Function
//  7. Return FULLSEND_MINT_URL
//
// When MintURL is set, reuses an existing mint:
//  1. Store all agent PEMs in Secret Manager
//  2. Return the provided MintURL
func (p *Provisioner) Provision(ctx context.Context) (map[string]string, error) {
	defer p.zeroPEMs()

	if len(p.cfg.GitHubOrgs) == 0 {
		return nil, fmt.Errorf("at least one GitHub org is required")
	}
	seen := make(map[string]bool)
	for i, org := range p.cfg.GitHubOrgs {
		if !githubOrgPattern.MatchString(org) {
			return nil, fmt.Errorf("invalid GitHub org name: %q", org)
		}
		lower := strings.ToLower(org)
		if seen[lower] {
			return nil, fmt.Errorf("duplicate GitHub org after normalization: %q", org)
		}
		seen[lower] = true
		p.cfg.GitHubOrgs[i] = lower
	}
	for role := range p.cfg.AgentPEMs {
		if !rolePattern.MatchString(role) {
			return nil, fmt.Errorf("invalid role name %q: must match %s", role, rolePattern.String())
		}
	}
	for role := range p.cfg.AgentAppIDs {
		if !rolePattern.MatchString(role) {
			return nil, fmt.Errorf("invalid role name %q: must match %s", role, rolePattern.String())
		}
	}

	if p.cfg.MintURL != "" {
		return p.provisionWithExistingMint(ctx)
	}
	return p.provisionSelfManaged(ctx)
}

// provisionWithExistingMint stores PEMs in an existing mint's Secret Manager.
func (p *Provisioner) provisionWithExistingMint(ctx context.Context) (map[string]string, error) {
	if p.cfg.ProjectID == "" {
		return nil, fmt.Errorf("GCP project ID is required for PEM storage")
	}
	if !gcpProjectIDPattern.MatchString(p.cfg.ProjectID) {
		return nil, fmt.Errorf("invalid GCP project ID: %q", p.cfg.ProjectID)
	}

	parsedURL, err := url.Parse(p.cfg.MintURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return nil, fmt.Errorf("MintURL %q must be a valid HTTPS URL", p.cfg.MintURL)
	}

	for _, role := range sortedByteMapKeys(p.cfg.AgentPEMs) {
		if err := p.StoreAgentPEM(ctx, role, p.cfg.AgentPEMs[role]); err != nil {
			return nil, fmt.Errorf("storing PEM for role %s: %w", role, err)
		}
	}

	for _, role := range sortedStringMapKeys(p.cfg.AgentAppIDs) {
		if _, hasPEM := p.cfg.AgentPEMs[role]; hasPEM {
			continue
		}
		sid := secretID(role)
		if err := p.gcpAPI.GetSecret(ctx, p.cfg.ProjectID, sid); err != nil {
			if errors.Is(err, ErrSecretNotFound) {
				return nil, fmt.Errorf("role %q has no PEM and secret %s not found in project %s",
					role, sid, p.cfg.ProjectID)
			}
			return nil, fmt.Errorf("checking secret %s for role %q: %w", sid, role, err)
		}
	}

	return map[string]string{
		"FULLSEND_MINT_URL": p.cfg.MintURL,
	}, nil
}

// provisionSelfManaged deploys the full mint infrastructure.
func (p *Provisioner) provisionSelfManaged(ctx context.Context) (map[string]string, error) {
	if p.cfg.ProjectID == "" {
		return nil, fmt.Errorf("GCP project ID is required")
	}
	if !gcpProjectIDPattern.MatchString(p.cfg.ProjectID) {
		return nil, fmt.Errorf("invalid GCP project ID: %q", p.cfg.ProjectID)
	}
	if !gcpRegionPattern.MatchString(p.cfg.Region) {
		return nil, fmt.Errorf("invalid GCP region: %q", p.cfg.Region)
	}
	if len(p.cfg.AgentAppIDs) == 0 {
		return nil, fmt.Errorf("at least one agent App ID is required")
	}
	for role := range p.cfg.AgentPEMs {
		if _, ok := p.cfg.AgentAppIDs[role]; !ok {
			return nil, fmt.Errorf("role %q has a PEM but no corresponding App ID", role)
		}
	}

	// Bundle function source early to fail fast before provisioning GCP
	// resources. The result is reused at deployment time to avoid redundant
	// I/O and a TOCTOU window.
	earlySourceZip, err := bundleFunctionSource(p.cfg.FunctionSourceDir)
	if err != nil {
		return nil, fmt.Errorf("validating function source: %w", err)
	}

	// Step 1: Get project number.
	projectNumber, err := p.gcpAPI.GetProjectNumber(ctx, p.cfg.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("getting project number: %w", err)
	}

	// Step 2: Create/verify service account.
	if err := p.gcpAPI.CreateServiceAccount(ctx, p.cfg.ProjectID, saName, "Fullsend token mint Cloud Function"); err != nil {
		return nil, fmt.Errorf("creating service account: %w", err)
	}

	// Step 3: Create/verify WIF pool.
	if err := p.gcpAPI.CreateWIFPool(ctx, projectNumber, p.cfg.WIFPoolName, "Fullsend GitHub OIDC Pool"); err != nil {
		return nil, fmt.Errorf("creating WIF pool: %w", err)
	}

	// Step 4: Create/verify WIF provider.
	for _, org := range p.cfg.GitHubOrgs {
		if strings.ContainsAny(org, `'"`) {
			return nil, fmt.Errorf("invalid GitHub org name %q: contains quotes", org)
		}
	}
	var attrCondition string
	if len(p.cfg.GitHubOrgs) == 1 {
		attrCondition = fmt.Sprintf("assertion.repository_owner == '%s'", p.cfg.GitHubOrgs[0])
	} else {
		quoted := make([]string, len(p.cfg.GitHubOrgs))
		for i, org := range p.cfg.GitHubOrgs {
			quoted[i] = fmt.Sprintf("'%s'", org)
		}
		attrCondition = fmt.Sprintf("assertion.repository_owner in [%s]", strings.Join(quoted, ", "))
	}
	if err := p.gcpAPI.CreateWIFProvider(ctx, projectNumber, p.cfg.WIFPoolName, p.cfg.WIFProvider, OIDCProviderConfig{
		IssuerURI:          oidcIssuer,
		AttributeCondition: attrCondition,
		AllowedAudiences:   []string{oidcAudience},
	}); err != nil {
		return nil, fmt.Errorf("creating WIF provider: %w", err)
	}

	// Step 5a: Store new agent PEMs.
	for _, role := range sortedByteMapKeys(p.cfg.AgentPEMs) {
		if err := p.StoreAgentPEM(ctx, role, p.cfg.AgentPEMs[role]); err != nil {
			return nil, fmt.Errorf("storing PEM for role %s: %w", role, err)
		}
	}

	// Step 5b: Verify secrets exist for roles without PEMs (re-install).
	for _, role := range sortedStringMapKeys(p.cfg.AgentAppIDs) {
		if _, hasPEM := p.cfg.AgentPEMs[role]; hasPEM {
			continue
		}
		sid := secretID(role)
		if err := p.gcpAPI.GetSecret(ctx, p.cfg.ProjectID, sid); err != nil {
			if errors.Is(err, ErrSecretNotFound) {
				return nil, fmt.Errorf("role %q has no PEM and secret %s not found in project %s",
					role, sid, p.cfg.ProjectID)
			}
			return nil, fmt.Errorf("checking secret %s for role %q: %w", sid, role, err)
		}
	}

	// Step 6: Build env vars and deploy Cloud Function.
	roleAppIDsJSON, err := json.Marshal(p.cfg.AgentAppIDs)
	if err != nil {
		return nil, fmt.Errorf("marshaling role app IDs: %w", err)
	}

	allowedRoles := sortedStringMapKeys(p.cfg.AgentAppIDs)

	envVars := map[string]string{
		"GCP_PROJECT_NUMBER": projectNumber,
		"WIF_POOL_NAME":      p.cfg.WIFPoolName,
		"WIF_PROVIDER_NAME":  p.cfg.WIFProvider,
		"ALLOWED_ORGS":       strings.Join(p.cfg.GitHubOrgs, ","),
		"OIDC_AUDIENCE":      oidcAudience,
		"ALLOWED_ROLES":      strings.Join(allowedRoles, ","),
		"ROLE_APP_IDS":       string(roleAppIDsJSON),
	}

	existing, err := p.gcpAPI.GetFunction(ctx, p.cfg.ProjectID, p.cfg.Region, functionName)
	if err != nil {
		return nil, fmt.Errorf("checking existing function: %w", err)
	}

	if p.cfg.DeployMode == DeploySkip {
		if existing == nil || existing.URI == "" {
			return nil, fmt.Errorf("function %s not found — cannot use --skip-mint-deploy without an existing deployment", functionName)
		}
	} else {
		sourceZip := earlySourceZip
		sourceHash := sha256Hex(sourceZip)
		envVars["FULLSEND_SOURCE_HASH"] = sourceHash

		needsDeploy := p.shouldDeploy(existing, envVars)

		if needsDeploy {
			storageSource, err := p.gcpAPI.UploadFunctionSource(ctx, p.cfg.ProjectID, p.cfg.Region, sourceZip)
			if err != nil {
				return nil, fmt.Errorf("uploading function source: %w", err)
			}

			saEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", saName, p.cfg.ProjectID)
			fnCfg := FunctionConfig{
				ServiceAccount: saEmail,
				EnvVars:        envVars,
				StorageSource:  storageSource,
				EntryPoint:     "ServeHTTP",
				Runtime:        "go126",
			}

			var opName string
			if existing != nil {
				opName, err = p.gcpAPI.UpdateFunction(ctx, p.cfg.ProjectID, p.cfg.Region, functionName, fnCfg)
				if err != nil {
					return nil, fmt.Errorf("updating function: %w", err)
				}
			} else {
				opName, err = p.gcpAPI.CreateFunction(ctx, p.cfg.ProjectID, p.cfg.Region, functionName, fnCfg)
				if err != nil {
					return nil, fmt.Errorf("deploying function: %w", err)
				}
			}

			if err := p.gcpAPI.WaitForOperation(ctx, opName); err != nil {
				return nil, fmt.Errorf("waiting for function deployment: %w", err)
			}

			existing, err = p.gcpAPI.GetFunction(ctx, p.cfg.ProjectID, p.cfg.Region, functionName)
			if err != nil {
				return nil, fmt.Errorf("querying function URL: %w", err)
			}
			if existing == nil || existing.URI == "" {
				return nil, fmt.Errorf("function %s deployed but not found or has no URI", functionName)
			}
		}
	}
	mintURL := existing.URI

	parsedURL, err := url.Parse(mintURL)
	if err != nil || parsedURL.Scheme != "https" ||
		(!strings.HasSuffix(parsedURL.Host, ".run.app") &&
			!strings.HasSuffix(parsedURL.Host, ".cloudfunctions.net")) {
		return nil, fmt.Errorf("function URL %q is not a valid Cloud Run URL", mintURL)
	}

	if err := p.gcpAPI.SetCloudRunInvoker(ctx, p.cfg.ProjectID, p.cfg.Region, functionName); err != nil {
		return nil, fmt.Errorf("setting function invoker policy: %w", err)
	}

	if err := p.waitForReady(ctx, mintURL); err != nil {
		return nil, fmt.Errorf("waiting for function readiness: %w", err)
	}

	return map[string]string{
		"FULLSEND_MINT_URL": mintURL,
	}, nil
}

// waitForReady polls the function until it responds with 200 OK, ensuring
// the Cloud Run backing service is warm and the function code is healthy.
// Uses exponential backoff starting at 2s, doubling each attempt up to 30s.
func (p *Provisioner) waitForReady(ctx context.Context, mintURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	const (
		initialBackoff = 2 * time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff
	var lastStatus int

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, mintURL+"/health", nil)
		if err != nil {
			return fmt.Errorf("creating health check request: %w", err)
		}
		resp, err := p.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			lastStatus = resp.StatusCode
			if resp.StatusCode == http.StatusOK {
				log.Printf("function ready after %d health check(s)", attempt+1)
				return nil
			}
			log.Printf("health check attempt %d: status %d (retry in %s)", attempt+1, resp.StatusCode, backoff)
		} else {
			log.Printf("health check attempt %d: %v (retry in %s)", attempt+1, err, backoff)
		}

		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("function not ready after 2m: %w", err)
			}
			return fmt.Errorf("function not ready after 2m (last status: %d)", lastStatus)
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (p *Provisioner) zeroPEMs() {
	for role, pem := range p.cfg.AgentPEMs {
		for i := range pem {
			pem[i] = 0
		}
		p.cfg.AgentPEMs[role] = pem
	}
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedByteMapKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// bundleFunctionSource creates a zip archive from the function source directory.
func bundleFunctionSource(dir string) ([]byte, error) {
	if dir == "" {
		return nil, fmt.Errorf("function source directory not configured")
	}

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading function source dir: %w", err)
	}

	var fileCount int
	var hasGoMod bool
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading file %s: %w", entry.Name(), err)
		}
		f, err := w.Create(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("creating zip entry %s: %w", entry.Name(), err)
		}
		if _, err := f.Write(data); err != nil {
			return nil, fmt.Errorf("writing zip entry %s: %w", entry.Name(), err)
		}
		fileCount++
		if entry.Name() == "go.mod" {
			hasGoMod = true
		}
	}

	if fileCount == 0 {
		return nil, fmt.Errorf("no deployable source files found in %s", dir)
	}
	if !hasGoMod {
		return nil, fmt.Errorf("function source directory %s is missing go.mod", dir)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("closing zip: %w", err)
	}
	return buf.Bytes(), nil
}

func envVarsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// shouldDeploy determines whether the Cloud Function needs (re)deployment.
func (p *Provisioner) shouldDeploy(existing *FunctionInfo, desiredEnvVars map[string]string) bool {
	switch p.cfg.DeployMode {
	case DeployForce:
		return true
	case DeploySkip:
		return false
	default: // DeployAuto
		if existing == nil {
			return true
		}
		if existing.State != "ACTIVE" || existing.URI == "" {
			return true
		}
		return !envVarsEqual(existing.EnvVars, desiredEnvVars)
	}
}
