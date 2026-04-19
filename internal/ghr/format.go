package ghr

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	gh "github.com/dutifuldev/ghreplica/internal/github"
)

func writeJSON(out io.Writer, value any, fields string) error {
	filtered, err := selectFields(value, fields)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(filtered)
}

func selectFields(value any, fields string) (any, error) {
	if strings.TrimSpace(fields) == "" {
		return value, nil
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}

	requested := parseFieldList(fields)
	if len(requested) == 0 {
		return decoded, nil
	}
	return filterValue(decoded, requested), nil
}

func parseFieldList(fields string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, field := range strings.Split(fields, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		out[field] = struct{}{}
	}
	return out
}

func filterValue(value any, fields map[string]struct{}) any {
	switch typed := value.(type) {
	case map[string]any:
		filtered := map[string]any{}
		for key, val := range typed {
			if _, ok := fields[key]; ok {
				filtered[key] = val
			}
		}
		return filtered
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, filterValue(item, fields))
		}
		return out
	default:
		return value
	}
}

func printRepoView(out io.Writer, repo gh.RepositoryResponse) {
	fmt.Fprintf(out, "%s\n", repo.FullName)
	if strings.TrimSpace(repo.Description) != "" {
		fmt.Fprintf(out, "%s\n\n", repo.Description)
	} else {
		fmt.Fprintln(out)
	}

	tw := newTabWriter(out)
	fmt.Fprintf(tw, "URL:\t%s\n", repo.HTMLURL)
	fmt.Fprintf(tw, "Visibility:\t%s\n", coalesce(repo.Visibility, boolVisibility(repo.Private)))
	fmt.Fprintf(tw, "Default branch:\t%s\n", repo.DefaultBranch)
	fmt.Fprintf(tw, "Archived:\t%t\n", repo.Archived)
	fmt.Fprintf(tw, "Updated:\t%s\n", humanTime(repo.UpdatedAt))
	_ = tw.Flush()
}

func printRepoStatus(out io.Writer, status MirrorStatusResponse) {
	fmt.Fprintf(out, "%s\n\n", status.FullName)

	tw := newTabWriter(out)
	fmt.Fprintf(tw, "Repository present:\t%t\n", status.RepositoryPresent)
	fmt.Fprintf(tw, "Tracked repo present:\t%t\n", status.TrackedRepositoryPresent)
	fmt.Fprintf(tw, "Enabled:\t%t\n", status.Enabled)
	fmt.Fprintf(tw, "Sync mode:\t%s\n", status.SyncMode)
	fmt.Fprintf(tw, "Webhook projection:\t%t\n", status.WebhookProjectionEnabled)
	fmt.Fprintf(tw, "Allow manual backfill:\t%t\n", status.AllowManualBackfill)
	fmt.Fprintf(tw, "Issues completeness:\t%s\n", status.IssuesCompleteness)
	fmt.Fprintf(tw, "Pulls completeness:\t%s\n", status.PullsCompleteness)
	fmt.Fprintf(tw, "Comments completeness:\t%s\n", status.CommentsCompleteness)
	fmt.Fprintf(tw, "Reviews completeness:\t%s\n", status.ReviewsCompleteness)
	fmt.Fprintf(tw, "Last bootstrap:\t%s\n", humanTimePtr(status.LastBootstrapAt))
	fmt.Fprintf(tw, "Last crawl:\t%s\n", humanTimePtr(status.LastCrawlAt))
	fmt.Fprintf(tw, "Last webhook:\t%s\n", humanTimePtr(status.LastWebhookAt))
	fmt.Fprintf(tw, "Issue count:\t%d\n", status.Counts.Issues)
	fmt.Fprintf(tw, "Pull count:\t%d\n", status.Counts.Pulls)
	fmt.Fprintf(tw, "Issue comments:\t%d\n", status.Counts.IssueComments)
	fmt.Fprintf(tw, "PR reviews:\t%d\n", status.Counts.PullRequestReviews)
	fmt.Fprintf(tw, "PR review comments:\t%d\n", status.Counts.PullRequestReviewComments)
	_ = tw.Flush()
}

func printMirrorRepositoryList(out io.Writer, repos []MirrorRepositoryResponse) {
	if len(repos) == 0 {
		fmt.Fprintln(out, "no mirrored repositories found")
		return
	}

	tw := newTabWriter(out)
	fmt.Fprintln(tw, "FULL NAME\tENABLED\tSYNC MODE\tLAST WEBHOOK")
	for _, repo := range repos {
		fmt.Fprintf(tw, "%s\t%t\t%s\t%s\n",
			repo.FullName,
			repo.Enabled,
			repo.SyncMode,
			humanTimePtr(repo.Timestamps.LastWebhookAt),
		)
	}
	_ = tw.Flush()
}

func printMirrorRepository(out io.Writer, repo MirrorRepositoryResponse) {
	fmt.Fprintf(out, "%s\n\n", repo.FullName)

	tw := newTabWriter(out)
	if repo.GitHubID != nil {
		fmt.Fprintf(tw, "GitHub ID:\t%d\n", *repo.GitHubID)
	}
	if strings.TrimSpace(repo.NodeID) != "" {
		fmt.Fprintf(tw, "Node ID:\t%s\n", repo.NodeID)
	}
	if repo.Fork != nil {
		fmt.Fprintf(tw, "Fork:\t%t\n", *repo.Fork)
	}
	fmt.Fprintf(tw, "Enabled:\t%t\n", repo.Enabled)
	fmt.Fprintf(tw, "Sync mode:\t%s\n", repo.SyncMode)
	fmt.Fprintf(tw, "Issues completeness:\t%s\n", repo.Completeness.Issues)
	fmt.Fprintf(tw, "Pulls completeness:\t%s\n", repo.Completeness.Pulls)
	fmt.Fprintf(tw, "Comments completeness:\t%s\n", repo.Completeness.Comments)
	fmt.Fprintf(tw, "Reviews completeness:\t%s\n", repo.Completeness.Reviews)
	fmt.Fprintf(tw, "Last bootstrap:\t%s\n", humanTimePtr(repo.Timestamps.LastBootstrapAt))
	fmt.Fprintf(tw, "Last crawl:\t%s\n", humanTimePtr(repo.Timestamps.LastCrawlAt))
	fmt.Fprintf(tw, "Last webhook:\t%s\n", humanTimePtr(repo.Timestamps.LastWebhookAt))
	_ = tw.Flush()
}

func printMirrorRepositoryStatus(out io.Writer, status MirrorRepositoryStatusResponse) {
	fmt.Fprintf(out, "%s mirror status\n\n", status.Repository.FullName)

	tw := newTabWriter(out)
	fmt.Fprintf(tw, "State:\t%s\n", status.Sync.State)
	fmt.Fprintf(tw, "Last error:\t%s\n", coalesce(status.Sync.LastError, "-"))
	fmt.Fprintf(tw, "Open PR total:\t%d\n", status.PullRequestChanges.Total)
	fmt.Fprintf(tw, "Open PR current:\t%d\n", status.PullRequestChanges.Current)
	fmt.Fprintf(tw, "Open PR stale:\t%d\n", status.PullRequestChanges.Stale)
	fmt.Fprintf(tw, "Open PR missing:\t%d\n", status.PullRequestChanges.Missing)
	fmt.Fprintf(tw, "Inventory scan running:\t%t\n", status.Activity.InventoryScanRunning)
	fmt.Fprintf(tw, "Backfill running:\t%t\n", status.Activity.BackfillRunning)
	fmt.Fprintf(tw, "Targeted refresh pending:\t%t\n", status.Activity.TargetedRefreshPending)
	fmt.Fprintf(tw, "Targeted refresh running:\t%t\n", status.Activity.TargetedRefreshRunning)
	fmt.Fprintf(tw, "Inventory refresh requested:\t%t\n", status.Activity.InventoryRefreshRequested)
	fmt.Fprintf(tw, "Last inventory scan started:\t%s\n", humanTimePtr(status.Timestamps.LastInventoryScanStartedAt))
	fmt.Fprintf(tw, "Last inventory scan finished:\t%s\n", humanTimePtr(status.Timestamps.LastInventoryScanFinishedAt))
	fmt.Fprintf(tw, "Last backfill started:\t%s\n", humanTimePtr(status.Timestamps.LastBackfillStartedAt))
	fmt.Fprintf(tw, "Last backfill finished:\t%s\n", humanTimePtr(status.Timestamps.LastBackfillFinishedAt))
	_ = tw.Flush()
}

func printIssueList(out io.Writer, issues []gh.IssueResponse) {
	if len(issues) == 0 {
		fmt.Fprintln(out, "no issues found")
		return
	}

	tw := newTabWriter(out)
	fmt.Fprintln(tw, "NUMBER\tTITLE\tSTATE\tUPDATED")
	for _, issue := range issues {
		fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\n",
			issue.Number,
			truncate(issue.Title, 72),
			issue.State,
			humanTime(issue.UpdatedAt),
		)
	}
	_ = tw.Flush()
}

func printIssueView(out io.Writer, repo string, issue gh.IssueResponse) {
	fmt.Fprintf(out, "%s\n", issue.Title)
	fmt.Fprintf(out, "%s#%d · %s · updated %s\n\n", repo, issue.Number, issue.State, humanTime(issue.UpdatedAt))
	if strings.TrimSpace(issue.Body) != "" {
		fmt.Fprintln(out, issue.Body)
		fmt.Fprintln(out)
	}
	tw := newTabWriter(out)
	if issue.User != nil {
		fmt.Fprintf(tw, "Author:\t%s\n", issue.User.Login)
	}
	fmt.Fprintf(tw, "Comments:\t%d\n", issue.Comments)
	fmt.Fprintf(tw, "URL:\t%s\n", issue.HTMLURL)
	_ = tw.Flush()
}

func printIssueComments(out io.Writer, comments []gh.IssueCommentResponse) {
	if len(comments) == 0 {
		fmt.Fprintln(out, "no issue comments found")
		return
	}
	for i, comment := range comments {
		if i > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "---")
			fmt.Fprintln(out)
		}
		author := ""
		if comment.User != nil {
			author = comment.User.Login
		}
		fmt.Fprintf(out, "%s commented %s\n\n", author, humanTime(comment.CreatedAt))
		fmt.Fprintln(out, strings.TrimSpace(comment.Body))
	}
}

func printIssueCommentsSection(out io.Writer, comments []gh.IssueCommentResponse) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Comments")
	fmt.Fprintln(out)
	if len(comments) == 0 {
		fmt.Fprintln(out, "no issue comments found")
		return
	}
	printIssueComments(out, comments)
}

func printPullList(out io.Writer, pulls []gh.PullRequestResponse) {
	if len(pulls) == 0 {
		fmt.Fprintln(out, "no pull requests found")
		return
	}

	tw := newTabWriter(out)
	fmt.Fprintln(tw, "NUMBER\tTITLE\tSTATE\tBRANCH\tUPDATED")
	for _, pull := range pulls {
		state := pull.State
		if pull.Draft {
			state = "draft"
		}
		fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\n",
			pull.Number,
			truncate(pull.Title, 72),
			state,
			pull.Head.Ref,
			humanTime(pull.UpdatedAt),
		)
	}
	_ = tw.Flush()
}

func printPullView(out io.Writer, repo string, pr gh.PullRequestResponse) {
	fmt.Fprintf(out, "%s\n", pr.Title)
	fmt.Fprintf(out, "%s#%d · %s · %s → %s · updated %s\n\n",
		repo, pr.Number, pullState(pr), pr.Head.Ref, pr.Base.Ref, humanTime(pr.UpdatedAt))
	if strings.TrimSpace(pr.Body) != "" {
		fmt.Fprintln(out, pr.Body)
		fmt.Fprintln(out)
	}
	tw := newTabWriter(out)
	if pr.User != nil {
		fmt.Fprintf(tw, "Author:\t%s\n", pr.User.Login)
	}
	fmt.Fprintf(tw, "URL:\t%s\n", pr.HTMLURL)
	fmt.Fprintf(tw, "Commits:\t%d\n", pr.Commits)
	fmt.Fprintf(tw, "Changed files:\t%d\n", pr.ChangedFiles)
	fmt.Fprintf(tw, "Additions:\t%d\n", pr.Additions)
	fmt.Fprintf(tw, "Deletions:\t%d\n", pr.Deletions)
	if pr.Mergeable != nil {
		fmt.Fprintf(tw, "Mergeable:\t%t\n", *pr.Mergeable)
	}
	if strings.TrimSpace(pr.MergeableState) != "" {
		fmt.Fprintf(tw, "Merge state:\t%s\n", pr.MergeableState)
	}
	_ = tw.Flush()
}

func printReviews(out io.Writer, reviews []gh.PullRequestReviewResponse) {
	if len(reviews) == 0 {
		fmt.Fprintln(out, "no reviews found")
		return
	}
	for i, review := range reviews {
		if i > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "---")
			fmt.Fprintln(out)
		}
		author := ""
		if review.User != nil {
			author = review.User.Login
		}
		when := review.CreatedAt
		if review.SubmittedAt != nil {
			when = *review.SubmittedAt
		}
		fmt.Fprintf(out, "%s reviewed %s [%s]\n\n", author, humanTime(when), strings.ToLower(review.State))
		if strings.TrimSpace(review.Body) != "" {
			fmt.Fprintln(out, strings.TrimSpace(review.Body))
		} else {
			fmt.Fprintln(out, "(no review body)")
		}
	}
}

func printReviewComments(out io.Writer, comments []gh.PullRequestReviewCommentResponse) {
	if len(comments) == 0 {
		fmt.Fprintln(out, "no review comments found")
		return
	}
	for i, comment := range comments {
		if i > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "---")
			fmt.Fprintln(out)
		}
		author := ""
		if comment.User != nil {
			author = comment.User.Login
		}
		location := comment.Path
		if comment.Line != nil {
			location = fmt.Sprintf("%s:%d", comment.Path, *comment.Line)
		}
		fmt.Fprintf(out, "%s commented on %s %s\n\n", author, location, humanTime(comment.CreatedAt))
		fmt.Fprintln(out, strings.TrimSpace(comment.Body))
	}
}

func newTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
}

func humanTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func humanTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return humanTime(*t)
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boolVisibility(private bool) string {
	if private {
		return "private"
	}
	return "public"
}

func pullState(pr gh.PullRequestResponse) string {
	if pr.Merged {
		return "merged"
	}
	if pr.Draft {
		return "draft"
	}
	return pr.State
}
