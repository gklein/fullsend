# How to onboard a new organization

This guide walks through installing fullsend in a GitHub organization and enrolling your first repository.

## Prerequisites

- **GitHub organization** with admin access
- **GitHub CLI** (`gh`) authenticated:

  ```bash
  gh auth refresh -s admin:org,repo,workflow,delete_repo
  ```

- **fullsend CLI** — download the latest binary from [GitHub Releases](https://github.com/fullsend-ai/fullsend/releases)

  *Note*: If running from a local clone of the repository use `go run ./cmd/fullsend/main.go <command>`

- **GCP project** with the [Vertex AI API](https://console.cloud.google.com/apis/library/aiplatform.googleapis.com) enabled

## 1. Create a GCP service account key

Create a service account with the `Vertex AI User` role and download its key:

```bash
export GCP_PROJECT="<gcp-project>"
export ORG_NAME="<org-name>"
export REPO_NAME="<repo-name>" # create a public repo for testing
gcloud iam service-accounts create "$ORG_NAME" \
  --display-name="Fullsend for $ORG_NAME" \
  --project="$GCP_PROJECT"

gcloud projects add-iam-policy-binding "$GCP_PROJECT" \
  --member="serviceAccount:$ORG_NAME@$GCP_PROJECT.iam.gserviceaccount.com" \
  --role="roles/aiplatform.user" \
  --condition=None

gcloud iam service-accounts keys create sa-key.json \
  --iam-account="$ORG_NAME@$GCP_PROJECT.iam.gserviceaccount.com"
```

Available regions for Claude on Vertex AI include `us-east5`, `europe-west1`, and `asia-southeast1`. Check the [Vertex AI documentation](https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/use-claude#regions) for the latest list.

## 2. Run the installer

The installer is interactive. It will open multiple browser windows to create and install a GitHub App for each agent role. Follow the prompts in each window to complete the app setup.

Near the end, the installer opens a browser to create a fine-grained personal access token (dispatch token). When creating it, make sure to grant **Actions: Read and write** permission scoped to the `.fullsend` repository — otherwise the verification step will fail with a 404.

If the installer fails partway through, run `fullsend admin uninstall "$ORG_NAME"` to clean up before retrying.

```bash
fullsend admin install "$ORG_NAME" \
  --repo "$REPO_NAME" \
  --gcp-project "$GCP_PROJECT" \
  --gcp-region global \
  --gcp-credentials-file sa-key.json
```

After the installer ends you need to change the `.fullsend` repository visibility to public.

## 3. Merge enrollment PRs

After install completes, the installer dispatches a workflow that creates an enrollment PR in each repo passed via `--repo`. These PRs add a shim workflow (`.github/workflows/fullsend.yaml`) that wires events to the agent pipeline.

Review and merge each enrollment PR to complete enrollment.

## 4. Test the pipeline

Once a repo is enrolled (enrollment PR merged):

1. Create an issue in the enrolled repo
2. The triage agent picks it up automatically — check the Actions tab in both the target repo and `.fullsend` for workflow run logs
