package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fullsend-ai/fullsend/internal/forge"
	gh "github.com/fullsend-ai/fullsend/internal/forge/github"
	"github.com/fullsend-ai/fullsend/internal/sticky"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

const reviewMarker = "<!-- fullsend:review-agent -->"

func newPostReviewCmd() *cobra.Command {
	var (
		repo   string
		pr     int
		result string
		token  string
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "post-review",
		Short: "Post or update a sticky review comment on a PR",
		Long: `Posts review findings as a sticky issue comment on a pull request.

On first run, creates a new comment with a hidden HTML marker.
On re-runs, finds the existing comment, collapses old content into
a <details> block, and edits in-place. This prevents review comment
flooding on force-push, manual re-run, or workflow retry.

The --result flag accepts a file path containing the review body text,
or reads from stdin if set to "-".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			printer := ui.New(os.Stdout)

			if token == "" {
				token = os.Getenv("GITHUB_TOKEN")
			}
			if token == "" {
				return fmt.Errorf("--token or GITHUB_TOKEN required")
			}

			parts := strings.SplitN(repo, "/", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("--repo must be in owner/repo format, got %q", repo)
			}
			owner, repoName := parts[0], parts[1]

			raw, err := readBody(result)
			if err != nil {
				return fmt.Errorf("reading review body: %w", err)
			}

			parsed, err := parseReviewResult(raw)
			if err != nil {
				return fmt.Errorf("parsing review result: %w", err)
			}

			printer.Header("Post Review")

			client := gh.New(token)
			cfg := sticky.Config{
				Marker: reviewMarker,
				DryRun: dryRun,
			}
			if err := sticky.Post(cmd.Context(), client, owner, repoName, pr, parsed.Body, cfg, printer); err != nil {
				return err
			}

			return submitFormalReview(cmd.Context(), client, owner, repoName, pr, parsed, dryRun, printer)
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "repository in owner/repo format (required)")
	cmd.Flags().IntVar(&pr, "pr", 0, "pull request number (required)")
	cmd.Flags().StringVar(&result, "result", "-", "path to review body file, or '-' for stdin")
	cmd.Flags().StringVar(&token, "token", "", "GitHub token (default: $GITHUB_TOKEN)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be posted without making API calls")
	_ = cmd.MarkFlagRequired("repo")
	_ = cmd.MarkFlagRequired("pr")

	return cmd
}

// ReviewResult represents a parsed review result file.
type ReviewResult struct {
	Body   string `json:"body"`
	Action string `json:"action"` // "approve", "request-changes", "comment"
}

// reviewActionToEvent maps a ReviewResult action to a GitHub PR review event.
func reviewActionToEvent(action string) (string, bool) {
	switch strings.ToLower(action) {
	case "approve":
		return "APPROVE", true
	case "request-changes", "request_changes":
		return "REQUEST_CHANGES", true
	case "comment":
		return "COMMENT", true
	default:
		return "", false
	}
}

// submitFormalReview minimizes stale reviews by the same user, then
// submits a new GitHub PR review. Minimizing first avoids a TOCTOU race
// where the just-created review might not appear in the list yet.
func submitFormalReview(ctx context.Context, client forge.Client, owner, repo string, pr int, parsed ReviewResult, dryRun bool, printer *ui.Printer) error {
	event, ok := reviewActionToEvent(parsed.Action)
	if !ok {
		printer.StepInfo(fmt.Sprintf("Unknown review action %q, skipping formal review", parsed.Action))
		return nil
	}

	if dryRun {
		printer.StepInfo(fmt.Sprintf("Dry run — would submit %s review", event))
		return nil
	}

	if err := minimizeStaleReviews(ctx, client, owner, repo, pr, printer); err != nil {
		return err
	}

	printer.StepStart(fmt.Sprintf("Submitting %s review", event))
	reviewBody := "See the review comment above for full details."
	if err := client.CreatePullRequestReview(ctx, owner, repo, pr, event, reviewBody); err != nil {
		return fmt.Errorf("submitting review: %w", err)
	}
	printer.StepDone("Review submitted")
	return nil
}

// minimizeStaleReviews lists all reviews on the PR, finds previous reviews
// by the authenticated user, and minimizes them. Called before creating a
// new review, so all existing reviews by this user are stale.
func minimizeStaleReviews(ctx context.Context, client forge.Client, owner, repo string, pr int, printer *ui.Printer) error {
	user, err := client.GetAuthenticatedUser(ctx)
	if err != nil {
		printer.StepInfo("Could not determine authenticated user, skipping stale review cleanup")
		return nil
	}

	reviews, err := client.ListPullRequestReviews(ctx, owner, repo, pr)
	if err != nil {
		printer.StepInfo("Could not list reviews, skipping stale review cleanup")
		return nil
	}

	var stale []forge.PullRequestReview
	for _, r := range reviews {
		if r.User == user {
			stale = append(stale, r)
		}
	}

	if len(stale) == 0 {
		return nil
	}

	printer.StepStart(fmt.Sprintf("Minimizing %d stale review(s)", len(stale)))
	for _, r := range stale {
		if err := client.MinimizeComment(ctx, r.NodeID, "OUTDATED"); err != nil {
			printer.StepInfo(fmt.Sprintf("Warning: could not minimize review %s: %v", r.NodeID, err))
		}
	}
	printer.StepDone("Stale reviews minimized")

	return nil
}

// parseReviewResult attempts to parse the body as a JSON ReviewResult.
// If parsing fails, treats the entire input as a plain-text body.
// Returns an error if the JSON is valid but the body field is empty.
func parseReviewResult(input string) (ReviewResult, error) {
	var result ReviewResult
	if err := json.Unmarshal([]byte(input), &result); err != nil {
		return ReviewResult{Body: input, Action: "comment"}, nil
	}
	if result.Body == "" {
		return ReviewResult{}, fmt.Errorf("review result JSON has empty body field")
	}
	if result.Action == "" {
		result.Action = "comment"
	}
	return result, nil
}
