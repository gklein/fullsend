# How to onboard a new organization

This guide walks through installing fullsend in a GitHub organization and enrolling your first repository.

## Prerequisites

- **GitHub organization** with admin access
- **GitHub CLI** (`gh`) authenticated — no special scopes are needed upfront. The installer runs a preflight check and tells you exactly which scopes are missing before making any changes. When prompted, run the `gh auth refresh -s <scopes>` command it suggests.

  > **Note on scope breadth:** `gh auth` scopes apply to *every* organization your account belongs to — GitHub does not support per-org scoping for classic OAuth tokens. If that is a concern, create a [fine-grained personal access token](https://github.com/settings/tokens?type=beta) scoped to the target organization and export it as `GH_TOKEN` before running the installer.

- **fullsend CLI** — download the latest binary from [GitHub Releases](https://github.com/fullsend-ai/fullsend/releases)

  *Note*: If running from a local clone of the repository use `go run ./cmd/fullsend/main.go <command>`

- **GCP project** with the following APIs enabled:
  - [Agent Platform](https://console.cloud.google.com/apis/library/aiplatform.googleapis.com) (inference)
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

The `--inference-region` flag defaults to `global` for the broadest model availability. For a list of all available regions, see the [Agent Platform documentation](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/partner-models/claude/use-claude).

## 1. Run the installer

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

The installer automatically provisions [Workload Identity Federation (WIF)](https://cloud.google.com/iam/docs/workload-identity-federation) infrastructure (pool `fullsend-pool`, provider `github-oidc`, IAM bindings) in the inference project. WIF eliminates long-lived credentials — GitHub Actions exchange short-lived OIDC tokens for GCP access tokens. To use a pre-existing WIF provider instead, pass `--inference-wif-provider "$WIF_PROVIDER"` (see [Advanced: pre-configure WIF](#advanced-pre-configure-wif) below).

`--mint-project` specifies the GCP project where the OIDC token mint Cloud Function is deployed. It can be the same project as `--inference-project` or a separate project. The installer automatically provisions a Cloud Function, WIF pool (`fullsend-pool`), WIF provider (`github-oidc`), and Secret Manager secrets in the mint project. A service account (`fullsend-mint`) is also created as the Cloud Function's runtime identity to access Secret Manager — this is internal infrastructure and does not require any admin setup.

Additional mint flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--mint-region` | `us-central1` | Cloud region for the token mint function |
| `--mint-url` | | Use an existing mint at this URL instead of deploying one |
| `--public` | `false` | Create public unlisted GitHub Apps (for multi-org) |
| `--app-set` | `fullsend` | App set name prefix for GitHub Apps (see [Custom app sets](#custom-app-sets)) |
| `--skip-app-setup` | `false` | Skip GitHub App creation (reuse existing apps) |
| `--skip-mint-deploy` | `false` | Skip Cloud Function deployment, reuse existing mint URL |

The installer automatically detects when the deployed mint function is up-to-date (same source hash) and skips code redeployment, only updating WIF infrastructure, org registration, and PEM secrets. Use `--skip-mint-deploy` when running from a machine without the function source code.

> **Mint URL stability:** The mint URL is stable across redeploys within the same project and region — updating the Cloud Function does not change its URL. Adding a new org to an existing mint only updates env vars (`ROLE_APP_IDS`, `ALLOWED_ORGS`) without redeploying the function. Existing enrolled repos continue working with no changes. However, deploying to a **different region** (e.g., changing `--mint-region` from `us-central1` to `us-east5`) creates a new Cloud Run service with a different URL. All enrolled repos store the mint URL in a repo variable (`FULLSEND_MINT_URL`) or org variable, so changing the region requires updating every enrolled repo's variable to the new URL. Avoid changing `--mint-region` after initial deployment unless you plan to update all consumers.

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

When the first org uses a custom app set prefix, pass `--app-set` so the apps are named accordingly:

```bash
fullsend admin install "$FIRST_ORG" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT" \
  --public \
  --app-set "$FIRST_ORG"
```

This creates public apps named `{first-org}-fullsend`, `{first-org}-coder`, etc.

**Additional orgs (install existing public apps):**

```bash
fullsend admin install "$ADDITIONAL_ORG" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT"
```

The installer auto-detects shared public apps by matching installed app IDs against the mint's `ROLE_APP_IDS`. It copies PEM secrets from the source org to the new org's scoped key and records the actual app slug in `config.yaml`, so subsequent operations find the correct app regardless of naming convention.

If the public apps were created with a custom `--app-set`, pass the same value so the CLI uses the correct slug prefix for convention-based lookups:

```bash
fullsend admin install "$ADDITIONAL_ORG" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT" \
  --app-set "$FIRST_ORG"
```

You can also pass `--mint-url "$MINT_URL"` explicitly to skip the auto-discovery step. PEMs use org-scoped naming (`fullsend-{org}--{role}-app-pem`), so each org's secrets are stored independently. For public apps (shared across orgs), the provisioner copies the same PEM under each org's scoped key.

> **Note:** Multi-org with `--public` requires all orgs to share the same GitHub Apps. Private apps (the default) are single-org only.

## 2. Merge enrollment PRs

If you chose to enroll repositories during install, the installer dispatches a workflow that creates an enrollment PR in each enrolled repo. These PRs add a shim workflow (`.github/workflows/fullsend.yaml`) that wires events to the agent pipeline.

Review and merge each enrollment PR to complete enrollment.

## 3. Managing repository enrollment

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

## 4. Test the pipeline

Once a repo is enrolled (enrollment PR merged):

1. Create an issue in the enrolled repo
2. The triage agent picks it up automatically — check the Actions tab in both the target repo and `.fullsend` for workflow run logs

---

## Per-repo installation

Per-repo mode installs fullsend for a single repository without requiring an org-wide `.fullsend` config repo. It's fully self-contained — creating GitHub Apps, deploying a token mint, and configuring WIF as needed.

### First-time install (no prior infrastructure)

```bash
fullsend admin install "$ORG_NAME/$REPO_NAME" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT"
```

This discovers existing infrastructure and creates what's missing:
- If no GitHub Apps exist, opens browser windows to create them (same manifest flow as per-org)
- If no token mint exists, deploys a Cloud Function
- If both exist from a prior per-org install, reuses them

Creating apps requires `admin:org` OAuth scope (the installer prompts for it). Reusing existing apps only requires `repo` and `workflow` scopes.

### Reusing existing infrastructure

When a per-org install already exists, per-repo reuses the apps and mint:

```bash
fullsend admin install "$ORG_NAME/$REPO_NAME" \
  --inference-project "$GCP_PROJECT" \
  --mint-url "$MINT_URL"
```

Or let it auto-discover the mint from the GCP project:

```bash
fullsend admin install "$ORG_NAME/$REPO_NAME" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT"
```

### Per-repo flags

Per-repo accepts all flags except `--vendor-fullsend-binary`, `--enroll-all`, and `--enroll-none` (which only apply to org-wide enrollment).

> **`--mint-region` note:** Per-repo uses the same `--mint-region` default (`us-central1`) as per-org. When reusing a mint deployed to a non-default region, pass `--mint-region` explicitly so auto-discovery finds the correct function.

---

## Custom app sets

By default, the installer creates GitHub Apps with the `fullsend` prefix (e.g., `fullsend-fullsend`, `fullsend-coder`, `fullsend-review`). Organizations that need their own set of apps — for example, to use org-specific permissions or to register multiple app sets on the same mint — can pass `--app-set` to override the prefix.

### Creating a custom app set

```bash
fullsend admin install "$ORG_NAME" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT" \
  --app-set "$ORG_NAME"
```

This creates apps named `{org}-fullsend`, `{org}-coder`, `{org}-review`, etc. The app set prefix is stored in the `.fullsend/config.yaml` slug mappings, so subsequent operations (permission checks, PEM recovery) find the correct apps automatically.

### Using existing public apps from another app set

When a mint already has public apps registered under a custom app set (e.g., `fullsend-ai-fullsend`, `fullsend-ai-coder`), additional orgs installing those apps must pass the same `--app-set` so the CLI resolves the correct slugs:

```bash
fullsend admin install "$NEW_ORG" \
  --inference-project "$GCP_PROJECT" \
  --mint-project "$GCP_PROJECT" \
  --app-set fullsend-ai
```

The installer detects that the public apps are already installed in the org (matched by app ID from the mint's `ROLE_APP_IDS`), copies PEM secrets to the new org's scoped key, and skips app creation. The `--app-set` value ensures convention-based slug lookups match the existing apps.

### Uninstalling a custom app set

When uninstalling an org that used a custom app set, pass the same `--app-set` value so the CLI generates the correct fallback slugs if the config repo is unavailable:

```bash
fullsend admin uninstall "$ORG_NAME" --app-set "$ORG_NAME"
```

### Constraints

- App set names must be lowercase alphanumeric with optional hyphens (no leading/trailing hyphens, no consecutive hyphens), max 39 characters
- The app set prefix only affects GitHub App slugs — GCP secret naming (`fullsend-{org}--{role}-app-pem`) and mint `ROLE_APP_IDS` keys (`{org}/{role}`) are independent of the app set

---

## Advanced: pre-configure WIF

The installer auto-provisions WIF infrastructure, but you can create it manually if you need custom pool names, attribute conditions, or want to share a provider across tools.

**Create a Workload Identity Pool and OIDC Provider:**

```bash
export GCP_PROJECT="<gcp-project>"
export ORG_NAME="<org-name>"

gcloud iam workload-identity-pools create fullsend-pool \
  --location=global \
  --display-name="Fullsend" \
  --project="$GCP_PROJECT"

gcloud iam workload-identity-pools providers create-oidc github-oidc \
  --location=global \
  --workload-identity-pool=fullsend-pool \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository_owner=assertion.repository_owner,attribute.repository=assertion.repository" \
  --attribute-condition="assertion.repository_owner == '$ORG_NAME'" \
  --project="$GCP_PROJECT"
```

**Grant Agent Platform access to the WIF principal:**

```bash
export PROJECT_NUMBER=$(gcloud projects describe "$GCP_PROJECT" --format='value(projectNumber)')
export WIF_PRINCIPAL="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/fullsend-pool/attribute.repository_owner/$ORG_NAME"

gcloud projects add-iam-policy-binding "$GCP_PROJECT" \
  --role="roles/aiplatform.user" \
  --member="$WIF_PRINCIPAL" \
  --condition=None
```

> **⚠️ Warning — broad WIF scope:** The `attribute.repository_owner` condition above grants WIF access to _all_ repositories in the organization, not just `.fullsend`. This is required for orgs using per-repo mode (where multiple repos need to authenticate to GCP independently), but it significantly widens the trust boundary compared to per-org-only setups. Note that `fullsend admin install <owner/repo>` auto-provisions a **per-repo** WIF provider scoped to a single repository — the org-wide condition here is broader than what the automated path creates.
>
> **For per-org-only setups**, use the tighter `assertion.repository == '$ORG_NAME/.fullsend'` condition instead, and scope the WIF principal to `attribute.repository/$ORG_NAME/.fullsend`. See [Google Cloud WIF documentation](https://cloud.google.com/iam/docs/workload-identity-federation) for condition syntax.

**Pass the provider to the installer:**

```bash
export WIF_PROVIDER="projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/fullsend-pool/providers/github-oidc"

fullsend admin install "$ORG_NAME" \
  --inference-project "$GCP_PROJECT" \
  --inference-wif-provider "$WIF_PROVIDER" \
  --mint-project "$GCP_PROJECT"
```

> **Note:** IAM policy bindings may take several minutes to propagate. If agent workflows fail with a permission error immediately after setup, wait a few minutes and retry.
