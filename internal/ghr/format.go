package ghr

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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

func printRepoView(out io.Writer, repo gh.RepositoryResponse) error {
	r := newRenderer()
	r.Printf("%s\n", repo.FullName)
	if strings.TrimSpace(repo.Description) != "" {
		r.Printf("%s\n\n", repo.Description)
	} else {
		r.Println()
	}

	tw := r.Tab()
	tw.Printf("URL:\t%s\n", repo.HTMLURL)
	tw.Printf("Visibility:\t%s\n", coalesce(repo.Visibility, boolVisibility(repo.Private)))
	tw.Printf("Default branch:\t%s\n", repo.DefaultBranch)
	tw.Printf("Archived:\t%t\n", repo.Archived)
	tw.Printf("Updated:\t%s\n", humanTime(repo.UpdatedAt))
	tw.Flush()
	return r.FlushTo(out)
}

func printMirrorRepositoryList(out io.Writer, repos []MirrorRepositoryResponse) error {
	r := newRenderer()
	if len(repos) == 0 {
		r.Println("no mirrored repositories found")
		return r.FlushTo(out)
	}

	tw := r.Tab()
	tw.Println("FULL NAME\tENABLED\tSYNC MODE\tLAST WEBHOOK")
	for _, repo := range repos {
		tw.Printf("%s\t%t\t%s\t%s\n",
			repo.FullName,
			repo.Enabled,
			repo.SyncMode,
			humanTimePtr(repo.Timestamps.LastWebhookAt),
		)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func printMirrorRepository(out io.Writer, repo MirrorRepositoryResponse) error {
	r := newRenderer()
	r.Printf("%s\n\n", repo.FullName)

	tw := r.Tab()
	if repo.GitHubID != nil {
		tw.Printf("GitHub ID:\t%d\n", *repo.GitHubID)
	}
	if strings.TrimSpace(repo.NodeID) != "" {
		tw.Printf("Node ID:\t%s\n", repo.NodeID)
	}
	if repo.Fork != nil {
		tw.Printf("Fork:\t%t\n", *repo.Fork)
	}
	tw.Printf("Enabled:\t%t\n", repo.Enabled)
	tw.Printf("Sync mode:\t%s\n", repo.SyncMode)
	tw.Printf("Issues completeness:\t%s\n", repo.Completeness.Issues)
	tw.Printf("Pulls completeness:\t%s\n", repo.Completeness.Pulls)
	tw.Printf("Comments completeness:\t%s\n", repo.Completeness.Comments)
	tw.Printf("Reviews completeness:\t%s\n", repo.Completeness.Reviews)
	tw.Printf("Last bootstrap:\t%s\n", humanTimePtr(repo.Timestamps.LastBootstrapAt))
	tw.Printf("Last crawl:\t%s\n", humanTimePtr(repo.Timestamps.LastCrawlAt))
	tw.Printf("Last webhook:\t%s\n", humanTimePtr(repo.Timestamps.LastWebhookAt))
	tw.Flush()
	return r.FlushTo(out)
}

func printMirrorRepositoryStatus(out io.Writer, status MirrorRepositoryStatusResponse) error {
	r := newRenderer()
	r.Printf("%s mirror status\n\n", status.Repository.FullName)

	tw := r.Tab()
	tw.Printf("State:\t%s\n", status.Sync.State)
	tw.Printf("Last error:\t%s\n", coalesce(status.Sync.LastError, "-"))
	tw.Printf("Open PR total:\t%d\n", status.PullRequestChanges.Total)
	tw.Printf("Open PR current:\t%d\n", status.PullRequestChanges.Current)
	tw.Printf("Open PR stale:\t%d\n", status.PullRequestChanges.Stale)
	tw.Printf("Open PR missing:\t%s\n", missingCountString(status.PullRequestChanges.Missing, status.PullRequestChanges.MissingStale))
	tw.Printf("Inventory scan running:\t%t\n", status.Activity.InventoryScanRunning)
	tw.Printf("Backfill running:\t%t\n", status.Activity.BackfillRunning)
	tw.Printf("Targeted refresh pending:\t%t\n", status.Activity.TargetedRefreshPending)
	tw.Printf("Targeted refresh running:\t%t\n", status.Activity.TargetedRefreshRunning)
	tw.Printf("Recent PR repair pending:\t%t\n", status.Activity.RecentPRRepairPending)
	tw.Printf("Recent PR repair running:\t%t\n", status.Activity.RecentPRRepairRunning)
	tw.Printf("Full history repair running:\t%t\n", status.Activity.FullHistoryRepairRunning)
	tw.Printf("Inventory refresh requested:\t%t\n", status.Activity.InventoryRefreshRequested)
	tw.Printf("Last inventory scan started:\t%s\n", humanTimePtr(status.Timestamps.LastInventoryScanStartedAt))
	tw.Printf("Last inventory scan finished:\t%s\n", humanTimePtr(status.Timestamps.LastInventoryScanFinishedAt))
	tw.Printf("Last backfill started:\t%s\n", humanTimePtr(status.Timestamps.LastBackfillStartedAt))
	tw.Printf("Last backfill finished:\t%s\n", humanTimePtr(status.Timestamps.LastBackfillFinishedAt))
	tw.Printf("Last recent PR repair requested:\t%s\n", humanTimePtr(status.Timestamps.LastRecentPRRepairRequestedAt))
	tw.Printf("Last recent PR repair started:\t%s\n", humanTimePtr(status.Timestamps.LastRecentPRRepairStartedAt))
	tw.Printf("Last recent PR repair finished:\t%s\n", humanTimePtr(status.Timestamps.LastRecentPRRepairFinishedAt))
	tw.Printf("Last full history repair started:\t%s\n", humanTimePtr(status.Timestamps.LastFullHistoryRepairStartedAt))
	tw.Printf("Last full history repair finished:\t%s\n", humanTimePtr(status.Timestamps.LastFullHistoryRepairFinishedAt))
	tw.Flush()
	return r.FlushTo(out)
}

func printIssueList(out io.Writer, issues []gh.IssueResponse) error {
	r := newRenderer()
	if len(issues) == 0 {
		r.Println("no issues found")
		return r.FlushTo(out)
	}

	tw := r.Tab()
	tw.Println("NUMBER\tTITLE\tSTATE\tUPDATED")
	for _, issue := range issues {
		tw.Printf("#%d\t%s\t%s\t%s\n",
			issue.Number,
			truncate(issue.Title, 72),
			issue.State,
			humanTime(issue.UpdatedAt),
		)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func printIssueView(out io.Writer, repo string, issue gh.IssueResponse) error {
	r := newRenderer()
	r.Printf("%s\n", issue.Title)
	r.Printf("%s#%d · %s · updated %s\n\n", repo, issue.Number, issue.State, humanTime(issue.UpdatedAt))
	if strings.TrimSpace(issue.Body) != "" {
		r.Println(issue.Body)
		r.Println()
	}
	tw := r.Tab()
	if issue.User != nil {
		tw.Printf("Author:\t%s\n", issue.User.Login)
	}
	tw.Printf("Comments:\t%d\n", issue.Comments)
	tw.Printf("URL:\t%s\n", issue.HTMLURL)
	tw.Flush()
	return r.FlushTo(out)
}

func printIssueComments(out io.Writer, comments []gh.IssueCommentResponse) error {
	r := newRenderer()
	if len(comments) == 0 {
		r.Println("no issue comments found")
		return r.FlushTo(out)
	}
	for i, comment := range comments {
		if i > 0 {
			r.Println()
			r.Println("---")
			r.Println()
		}
		author := ""
		if comment.User != nil {
			author = comment.User.Login
		}
		r.Printf("%s commented %s\n\n", author, humanTime(comment.CreatedAt))
		r.Println(strings.TrimSpace(comment.Body))
	}
	return r.FlushTo(out)
}

func printIssueCommentsSection(out io.Writer, comments []gh.IssueCommentResponse) error {
	r := newRenderer()
	r.Println()
	r.Println("Comments")
	r.Println()
	if err := r.FlushTo(out); err != nil {
		return err
	}
	return printIssueComments(out, comments)
}

func printPullList(out io.Writer, pulls []gh.PullRequestResponse) error {
	r := newRenderer()
	if len(pulls) == 0 {
		r.Println("no pull requests found")
		return r.FlushTo(out)
	}

	tw := r.Tab()
	tw.Println("NUMBER\tTITLE\tSTATE\tBRANCH\tUPDATED")
	for _, pull := range pulls {
		state := pull.State
		if pull.Draft {
			state = "draft"
		}
		tw.Printf("#%d\t%s\t%s\t%s\t%s\n",
			pull.Number,
			truncate(pull.Title, 72),
			state,
			pull.Head.Ref,
			humanTime(pull.UpdatedAt),
		)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func printPullView(out io.Writer, repo string, pr gh.PullRequestResponse) error {
	r := newRenderer()
	r.Printf("%s\n", pr.Title)
	r.Printf("%s#%d · %s · %s → %s · updated %s\n\n",
		repo, pr.Number, pullState(pr), pr.Head.Ref, pr.Base.Ref, humanTime(pr.UpdatedAt))
	if strings.TrimSpace(pr.Body) != "" {
		r.Println(pr.Body)
		r.Println()
	}
	tw := r.Tab()
	if pr.User != nil {
		tw.Printf("Author:\t%s\n", pr.User.Login)
	}
	tw.Printf("URL:\t%s\n", pr.HTMLURL)
	tw.Printf("Commits:\t%d\n", pr.Commits)
	tw.Printf("Changed files:\t%d\n", pr.ChangedFiles)
	tw.Printf("Additions:\t%d\n", pr.Additions)
	tw.Printf("Deletions:\t%d\n", pr.Deletions)
	if pr.Mergeable != nil {
		tw.Printf("Mergeable:\t%t\n", *pr.Mergeable)
	}
	if strings.TrimSpace(pr.MergeableState) != "" {
		tw.Printf("Merge state:\t%s\n", pr.MergeableState)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func printReviews(out io.Writer, reviews []gh.PullRequestReviewResponse) error {
	r := newRenderer()
	if len(reviews) == 0 {
		r.Println("no reviews found")
		return r.FlushTo(out)
	}
	for i, review := range reviews {
		if i > 0 {
			r.Println()
			r.Println("---")
			r.Println()
		}
		author := ""
		if review.User != nil {
			author = review.User.Login
		}
		when := review.CreatedAt
		if review.SubmittedAt != nil {
			when = *review.SubmittedAt
		}
		r.Printf("%s reviewed %s [%s]\n\n", author, humanTime(when), strings.ToLower(review.State))
		if strings.TrimSpace(review.Body) != "" {
			r.Println(strings.TrimSpace(review.Body))
		} else {
			r.Println("(no review body)")
		}
	}
	return r.FlushTo(out)
}

func printReviewComments(out io.Writer, comments []gh.PullRequestReviewCommentResponse) error {
	r := newRenderer()
	if len(comments) == 0 {
		r.Println("no review comments found")
		return r.FlushTo(out)
	}
	for i, comment := range comments {
		if i > 0 {
			r.Println()
			r.Println("---")
			r.Println()
		}
		author := ""
		if comment.User != nil {
			author = comment.User.Login
		}
		location := comment.Path
		if comment.Line != nil {
			location = fmt.Sprintf("%s:%d", comment.Path, *comment.Line)
		}
		r.Printf("%s commented on %s %s\n\n", author, location, humanTime(comment.CreatedAt))
		r.Println(strings.TrimSpace(comment.Body))
	}
	return r.FlushTo(out)
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
