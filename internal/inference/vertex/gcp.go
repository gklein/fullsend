package vertex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/fullsend-ai/fullsend/internal/gcp"
)

// gcpIDPattern validates GCP project IDs and service account names.
var gcpIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// saEmailPattern validates GCP service account email addresses.
var saEmailPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`)

// LiveGCPClient implements GCPClient using the GCP IAM REST API.
// It embeds *gcp.Client for shared ADC auth and HTTP helper logic.
type LiveGCPClient struct {
	*gcp.Client
}

// NewLiveGCPClient creates a new LiveGCPClient.
func NewLiveGCPClient() *LiveGCPClient {
	return &LiveGCPClient{
		Client: gcp.NewClient(),
	}
}

// GetServiceAccount checks that a service account exists in the project.
func (c *LiveGCPClient) GetServiceAccount(ctx context.Context, projectID, saName string) error {
	if !gcpIDPattern.MatchString(projectID) {
		return fmt.Errorf("invalid GCP project ID %q", projectID)
	}
	if !gcpIDPattern.MatchString(saName) {
		return fmt.Errorf("invalid service account name %q", saName)
	}

	email := saName + "@" + projectID + ".iam.gserviceaccount.com"
	reqURL := fmt.Sprintf("https://iam.googleapis.com/v1/projects/%s/serviceAccounts/%s",
		url.PathEscape(projectID), url.PathEscape(email))

	resp, err := c.Client.DoRequest(ctx, http.MethodGet, reqURL, "")
	if err != nil {
		return fmt.Errorf("checking service account: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("service account %s not found in project %s", saName, projectID)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("unexpected status %d checking service account: %s", resp.StatusCode, gcp.ExtractErrorMessage(body))
	}
	return nil
}

// CreateServiceAccount creates a new service account in the project.
func (c *LiveGCPClient) CreateServiceAccount(ctx context.Context, projectID, saName, displayName string) error {
	if !gcpIDPattern.MatchString(projectID) {
		return fmt.Errorf("invalid GCP project ID %q", projectID)
	}
	if !gcpIDPattern.MatchString(saName) {
		return fmt.Errorf("invalid service account name %q", saName)
	}

	reqURL := fmt.Sprintf("https://iam.googleapis.com/v1/projects/%s/serviceAccounts",
		url.PathEscape(projectID))
	payloadObj := struct {
		AccountID      string `json:"accountId"`
		ServiceAccount struct {
			DisplayName string `json:"displayName"`
		} `json:"serviceAccount"`
	}{AccountID: saName}
	payloadObj.ServiceAccount.DisplayName = displayName
	payloadBytes, err := json.Marshal(payloadObj)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}
	payload := string(payloadBytes)

	resp, err := c.Client.DoRequest(ctx, http.MethodPost, reqURL, payload)
	if err != nil {
		return fmt.Errorf("creating service account: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		// SA already exists — treat as success for idempotency.
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("unexpected status %d creating service account: %s", resp.StatusCode, gcp.ExtractErrorMessage(body))
	}
	return nil
}

// CreateServiceAccountKey generates a new JSON key for the service account.
func (c *LiveGCPClient) CreateServiceAccountKey(ctx context.Context, projectID, saEmail string) ([]byte, error) {
	if !gcpIDPattern.MatchString(projectID) {
		return nil, fmt.Errorf("invalid GCP project ID %q", projectID)
	}
	if !saEmailPattern.MatchString(saEmail) {
		return nil, fmt.Errorf("invalid service account email %q", saEmail)
	}

	reqURL := fmt.Sprintf("https://iam.googleapis.com/v1/projects/%s/serviceAccounts/%s/keys",
		url.PathEscape(projectID), url.PathEscape(saEmail))
	payload := `{"keyAlgorithm":"KEY_ALG_RSA_2048","privateKeyType":"TYPE_GOOGLE_CREDENTIALS_FILE"}`

	resp, err := c.Client.DoRequest(ctx, http.MethodPost, reqURL, payload)
	if err != nil {
		return nil, fmt.Errorf("creating service account key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("unexpected status %d creating key: %s", resp.StatusCode, gcp.ExtractErrorMessage(body))
	}

	var result struct {
		PrivateKeyData string `json:"privateKeyData"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding key response: %w", err)
	}

	// privateKeyData is base64-encoded JSON credentials.
	decoded, err := base64.StdEncoding.DecodeString(result.PrivateKeyData)
	if err != nil {
		return nil, fmt.Errorf("decoding private key data: %w", err)
	}

	return decoded, nil
}
