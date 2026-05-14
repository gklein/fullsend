# How to onboard a new organization

This guide walks through installing fullsend in a GitHub organization and enrolling your first repository.

## Prerequisites

- **GitHub organization** with admin access
- **GitHub CLI** (`gh`) authenticated — no special scopes are needed upfront. The installer runs a preflight check and tells you exactly which scopes are missing before making any changes. When prompted, run the `gh auth refresh -s <scopes>` command it suggests.

  > **Note on scope breadth:** `gh auth` scopes apply to *every* organization your account belongs to — GitHub does not support per-org scoping for classic OAuth tokens. If that is a concern, create a [fine-grained personal access token](https://github.com/settings/tokens?type=beta) scoped to the target organization and export it as `GH_TOKEN` before running the installer.

- **fullsend CLI** — download the latest binary from [GitHub Releases](https://github.com/fullsend-ai/fullsend/releases)

  *Note*: If running from a local clone of the repository use `go run ./cmd/fullsend/main.go <command>`

- **GCP project** with the following APIs enabled:
  - [Vertex AI](https://console.cloud.google.com/apis/library/aiplatform.googleapis.com) (inference)
  - [Cloud Functions](https://console.cloud.google.com/apis/library/cloudfunctions.googleapis.com) (token mint)
  - [Cloud Run](https://console.cloud.google.com/apis/library/run.googleapis.com) (token mint runtime)
  - [Secret Manager](https://console.cloud.google.com/apis/library/secretmanager.googleapis.com) (PEM storage)
  - [IAM Credentials](https://console.cloud.google.com/apis/library/iamcredentials.googleapis.com) (WIF token exchange)
  - [Cloud Resource Manager](https://console.cloud.google.com/apis/library/cloudresourcemanager.googleapis.com) (project number lookup)

### OAuth scope reference

The table below lists every scope the installer may request and why. You are never asked for all of them at once — the preflight check requests only the scopes needed for the operation you are running.

| Scope | When needed | Why |
|-------|-------------|-----|
| `repo` | install, analyze | Read/write repository contents, manage repo-level secrets |
| `workflow` | install | Create and update GitHub Actions workflow files in `.github/workflows/` |
| `admin:org` | install, uninstall, analyze | Manage organization-level Actions variables (the mint URL) |
| `delete_repo` | uninstall | Delete the `.fullsend` config repository |

The `--inference-region` flag defaults to `global` for the broadest model availability. For a list of all available regions, see the [Vertex AI documentation](https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/use-claude#regions). The `--inference-wif-provider` flag is optional — when omitted, the installer auto-provisions WIF infrastructure.

## 1. Set up inference authentication with GCP Agent Platform

Fullsend uses [Workload Identity Federation (WIF)](https://cloud.google.com/iam/docs/workload-identity-federation) to authenticate GitHub Actions to [GCP Agent Platform](https://cloud.google.com/products/agent-platform) (formerly Vertex AI). WIF eliminates long-lived credentials — GitHub Actions exchange short-lived OIDC tokens for GCP access tokens. See the [google-github-actions/auth documentation](https://github.com/google-github-actions/auth#direct-workload-identity-federation) for background on direct WIF.

**1a. Create a Workload Identity Pool and OIDC Provider**

```bash
export GCP_PROJECT="<gcp-project>"
export ORG_NAME="<org-name>"

gcloud iam workload-identity-pools create github-actions \
  --location=global \
  --display-name="GitHub Actions" \
  --project="$GCP_PROJECT"

gcloud iam workload-identity-pools providers create-oidc github \
  --location=global \
  --workload-identity-pool=github-actions \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository_owner=assertion.repository_owner,attribute.repository=assertion.repository" \
  --attribute-condition="assertion.repository == '$ORG_NAME/.fullsend'" \
  --project="$GCP_PROJECT"
```

The `attribute-condition` restricts which GitHub Actions workflows can exchange OIDC tokens for GCP credentials. Scoping to `$ORG_NAME/.fullsend` ensures only the `.fullsend` config repo can authenticate — workflows in other repos cannot obtain Vertex AI credentials.

**1b. Grant Vertex AI access to the WIF principal**

```bash
export PROJECT_NUMBER=$(gcloud projects describe "$GCP_PROJECT" --format='value(projectNumber)')
export WIF_PRINCIPAL="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github-actions/attribute.repository/$ORG_NAME/.fullsend"

gcloud projects add-iam-policy-binding "$GCP_PROJECT" \
  --role="roles/aiplatform.user" \
  --member="$WIF_PRINCIPAL" \
  --condition=None
```

> **Note:** IAM policy bindings may take several minutes to propagate. If the agent workflow fails with a permission error immediately after setup, wait a few minutes and retry.

**1c. Note the WIF provider resource name**

```bash
export WIF_PROVIDER="projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github-actions/providers/github"
```

## 2. Run the installer

The installer is interactive. It will open multiple browser windows to create and install a GitHub App for each agent role. Follow the prompts in each window to complete the app setup.

During installation, you'll be prompted to choose repository enrollment:
- **[a] Enroll all repositories** — immediately enrolls all org repos (excluding `.fullsend`)
- **[n] Enroll no repositories** — skip enrollment during install; enroll repositories later using `fullsend admin enable repos`

The installer creates the `.fullsend` config repo as **public** by default. This is required for cross-repo `workflow_call` to work with enrolled repos of any visibility (public, private, or internal) across all GitHub plan tiers. If an admin later makes `.fullsend` private, only other private repos in the org will be able to trigger agent workflows — public and internal repos will fail silently.

If the installer fails partway through, run `fullsend admin uninstall "$ORG_NAME"` to clean up before retrying. The uninstall preflight will prompt you to add the `delete_repo` scope if it is missing.

```bash
fullsend admin install "$ORG_NAME" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT"
```

When `--inference-wif-provider` is omitted, the installer auto-discovers or creates WIF infrastructure (pool `fullsend-pool`, provider `github-oidc`) in the inference project. Pass `--inference-wif-provider "$WIF_PROVIDER"` to use a pre-existing provider instead.

`--mint-project` specifies the GCP project where the OIDC token mint Cloud Function is deployed. It can be the same project as `--inference-project` or a separate project. The installer automatically provisions a Cloud Function, WIF pool (`fullsend-pool`), WIF provider (`github-oidc`), and Secret Manager secrets in the mint project. A service account (`fullsend-mint`) is also created as the Cloud Function's runtime identity to access Secret Manager — this is internal infrastructure and does not require any admin setup.

Additional mint flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--mint-region` | `us-central1` | Cloud region for the token mint function |
| `--mint-url` | | Use an existing mint at this URL instead of deploying one |
| `--public` | `false` | Create public unlisted GitHub Apps (for multi-org) |
| `--skip-mint-deploy` | `false` | Skip Cloud Function deployment, reuse existing mint URL |
| `--force-mint-deploy` | `false` | Force Cloud Function redeployment even if unchanged |

`--skip-mint-deploy` and `--force-mint-deploy` are mutually exclusive.

### Multi-org setup

A single token mint can serve multiple GitHub organizations. The first org deploys the mint infrastructure and creates **public unlisted** GitHub Apps; additional orgs reuse the existing mint and install the same apps.

**First org (deploys mint + creates public apps):**

```bash
fullsend admin install "$FIRST_ORG" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT" \
  --public
```

The `--public` flag creates GitHub Apps as public unlisted — they won't appear in the marketplace but can be installed by other organizations via their installation URL.

**Additional orgs (reuse existing mint):**

```bash
fullsend admin install "$ADDITIONAL_ORG" \
  --inference-project "$GCP_PROJECT" \
  --mint-url "$MINT_URL"
```

`--mint-url` skips Cloud Function deployment and stores PEMs in the existing mint's GCP project. PEMs use org-scoped naming (`fullsend-{org}--{role}-app-pem`), so each org's secrets are stored independently. For public apps (shared across orgs), the provisioner stores the same PEM under each org's scoped key.

> **Note:** Multi-org with `--public` requires all orgs to share the same GitHub Apps. Private apps (the default) are single-org only.

## 3. Merge enrollment PRs

If you chose to enroll repositories during install, the installer dispatches a workflow that creates an enrollment PR in each enrolled repo. These PRs add a shim workflow (`.github/workflows/fullsend.yaml`) that wires events to the agent pipeline.

Review and merge each enrollment PR to complete enrollment.

## 4. Managing repository enrollment

After installation, you can enroll or unenroll repositories at any time using the `repos` subcommands.

### Enable repositories

To enroll specific repositories:

```bash
fullsend admin enable repos "$ORG_NAME" <repo-name> [repo-name...]
```

To enroll all repositories:

```bash
fullsend admin enable repos "$ORG_NAME" --all
```

The enable command:
- Updates `config.yaml` in the `.fullsend` repository
- Triggers the `repo-maintenance` workflow to create enrollment PRs
- Validates that repositories exist in the organization before making changes

### Disable repositories

To unenroll specific repositories:

```bash
fullsend admin disable repos "$ORG_NAME" <repo-name> [repo-name...]
```

To unenroll all repositories:

```bash
fullsend admin disable repos "$ORG_NAME" --all
```

The `--all` flag prompts for confirmation — you must type the exact organization name when prompted. To skip the confirmation prompt (e.g., in automated scripts):

```bash
fullsend admin disable repos "$ORG_NAME" --all --yolo
```

The disable command:
- Updates `config.yaml` to mark repositories as disabled
- Triggers the `repo-maintenance` workflow to create unenrollment PRs
- Warns (but does not reject) repository names not found in the config, allowing safe cleanup of deleted repos
- Does not delete existing shim workflows (merge the unenrollment PR to remove them)

## 5. Test the pipeline

Once a repo is enrolled (enrollment PR merged):

1. Create an issue in the enrolled repo
2. The triage agent picks it up automatically — check the Actions tab in both the target repo and `.fullsend` for workflow run logs
