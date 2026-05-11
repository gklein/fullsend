#!/usr/bin/env bash
# Adds a pull request to a GitHub merge queue using the GraphQL API.
# Usage: enqueue-pr.sh [PR_NUMBER_OR_URL]
#
# If no argument is given, uses the current branch's PR.
# Requires: gh CLI authenticated with sufficient permissions.

set -euo pipefail

pr="${1:-}"

# Resolve PR number/URL to the full PR URL and extract owner/repo/number
if [[ -z "$pr" ]]; then
  pr_url="$(gh pr view --json url -q .url)"
elif [[ "$pr" =~ ^[0-9]+$ ]]; then
  pr_url="$(gh pr view "$pr" --json url -q .url)"
else
  pr_url="$pr"
fi

echo "Enqueuing: $pr_url"

# Get the PR's node ID (required by the GraphQL mutation)
pr_node_id="$(gh pr view "$pr_url" --json id -q .id)"

# Enqueue the PR
result="$(gh api graphql -f query='
  mutation($prId: ID!) {
    enqueuePullRequest(input: {pullRequestId: $prId}) {
      mergeQueueEntry {
        position
        estimatedTimeToMerge
      }
    }
  }
' -f prId="$pr_node_id")"

errors="$(echo "$result" | jq -r '.errors // empty')"
if [[ -n "$errors" ]]; then
  echo "GraphQL errors:" >&2
  echo "$errors" | jq . >&2
  exit 1
fi

position="$(echo "$result" | jq -r '.data.enqueuePullRequest.mergeQueueEntry.position')"
eta="$(echo "$result" | jq -r '.data.enqueuePullRequest.mergeQueueEntry.estimatedTimeToMerge // "unknown"')"

echo "PR added to merge queue at position $position (ETA: $eta)"
