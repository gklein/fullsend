package function

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	defaults := map[string]string{
		"ALLOWED_ORGS":       "test-org",
		"GCP_PROJECT_NUMBER": "123456",
		"WIF_POOL_NAME":      "test-pool",
		"WIF_PROVIDER_NAME":  "github-oidc",
		"OIDC_AUDIENCE":      "fullsend-mint",
		"ALLOWED_ROLES":      "triage,coder,review,fix,fullsend",
		"ROLE_APP_IDS":       `{"triage":"100","coder":"200","review":"300","fix":"400","fullsend":"500"}`,
	}
	for k, v := range defaults {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	os.Exit(m.Run())
}
