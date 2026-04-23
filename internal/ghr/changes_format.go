package ghr

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
)

func printRepoChangeStatus(out io.Writer, status RepoChangeStatusResponse) error {
	r := newRenderer()
	r.Printf("%s change status\n", status.FullName)
	r.Println()
	tw := r.Tab()
	tw.Printf("Backfill mode:\t%s\n", status.BackfillMode)
	tw.Printf("Priority:\t%d\n", status.BackfillPriority)
	tw.Printf("Targeted refresh pending:\t%t\n", status.TargetedRefreshPending)
	tw.Printf("Targeted refresh running:\t%t\n", status.TargetedRefreshRunning)
	tw.Printf("Inventory current gen:\t%d\n", status.InventoryGenerationCurrent)
	tw.Printf("Inventory building gen:\t%s\n", intPtrString(status.InventoryGenerationBuilding))
	tw.Printf("Inventory needs refresh:\t%t\n", status.InventoryNeedsRefresh)
	tw.Printf("Inventory scan running:\t%t\n", status.InventoryScanRunning)
	tw.Printf("Backfill running:\t%t\n", status.BackfillRunning)
	tw.Printf("Backfill generation:\t%d\n", status.BackfillGeneration)
	tw.Printf("Open PRs total:\t%d\n", status.OpenPRTotal)
	tw.Printf("Open PRs current:\t%d\n", status.OpenPRCurrent)
	tw.Printf("Open PRs stale:\t%d\n", status.OpenPRStale)
	tw.Printf("Open PRs missing:\t%s\n", missingCountString(status.OpenPRMissing, status.OpenPRMissingStale))
	tw.Printf("Backfill cursor:\t%s\n", intPtrString(status.BackfillCursor))
	tw.Printf("Backfill cursor updated:\t%s\n", humanTimePtr(status.BackfillCursorUpdatedAt))
	tw.Printf("Last webhook:\t%s\n", humanTimePtr(status.LastWebhookAt))
	tw.Printf("Last inventory scan started:\t%s\n", humanTimePtr(status.LastInventoryScanStartedAt))
	tw.Printf("Last inventory scan finished:\t%s\n", humanTimePtr(status.LastInventoryScanFinishedAt))
	tw.Printf("Last inventory scan succeeded:\t%s\n", humanTimePtr(status.LastInventoryScanSucceededAt))
	tw.Printf("Inventory last committed:\t%s\n", humanTimePtr(status.InventoryLastCommittedAt))
	tw.Printf("Last backfill started:\t%s\n", humanTimePtr(status.LastBackfillStartedAt))
	tw.Printf("Last backfill finished:\t%s\n", humanTimePtr(status.LastBackfillFinishedAt))
	tw.Printf("Last error:\t%s\n", coalesce(status.LastError, "-"))
	tw.Flush()
	return r.FlushTo(out)
}

func missingCountString(value *int, stale bool) string {
	if stale {
		return "unknown (inventory stale)"
	}
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *value)
}

func printPullRequestChangeStatus(out io.Writer, repo string, status PullRequestChangeStatusResponse) error {
	r := newRenderer()
	r.Printf("%s change status\n", formatPullRef(repo, status.PullRequestNumber))
	r.Println()
	tw := r.Tab()
	tw.Printf("Indexed:\t%t\n", status.Indexed)
	tw.Printf("State:\t%s\n", coalesce(status.State, "-"))
	tw.Printf("Draft:\t%t\n", status.Draft)
	tw.Printf("Head SHA:\t%s\n", coalesce(status.HeadSHA, "-"))
	tw.Printf("Base SHA:\t%s\n", coalesce(status.BaseSHA, "-"))
	tw.Printf("Merge base:\t%s\n", coalesce(status.MergeBaseSHA, "-"))
	tw.Printf("Base ref:\t%s\n", coalesce(status.BaseRef, "-"))
	tw.Printf("Indexed as:\t%s\n", coalesce(status.IndexedAs, "-"))
	tw.Printf("Freshness:\t%s\n", coalesce(status.IndexFreshness, "-"))
	tw.Printf("Changed files:\t%d\n", status.ChangedFiles)
	tw.Printf("Indexed files:\t%d\n", status.IndexedFileCount)
	tw.Printf("Path-only files:\t%d\n", status.PathOnlyFileCount)
	tw.Printf("Skipped files:\t%d\n", status.SkippedFileCount)
	tw.Printf("Hunks:\t%d\n", status.HunkCount)
	tw.Printf("Additions:\t%d\n", status.Additions)
	tw.Printf("Deletions:\t%d\n", status.Deletions)
	tw.Printf("Patch bytes:\t%d\n", status.PatchBytes)
	tw.Printf("Backfill in progress:\t%t\n", status.BackfillInProgress)
	tw.Printf("Inventory needs refresh:\t%t\n", status.InventoryNeedsRefresh)
	tw.Printf("Last indexed:\t%s\n", humanTimePtr(status.LastIndexedAt))
	tw.Printf("Last error:\t%s\n", coalesce(status.LastError, "-"))
	tw.Flush()
	return r.FlushTo(out)
}

func printPullRequestChangeSnapshot(out io.Writer, repo string, snapshot PullRequestChangeSnapshotResponse) error {
	r := newRenderer()
	r.Printf("%s change snapshot\n", formatPullRef(repo, snapshot.PullRequestNumber))
	r.Println()

	tw := r.Tab()
	tw.Printf("Head SHA:\t%s\n", snapshot.HeadSHA)
	tw.Printf("Base SHA:\t%s\n", snapshot.BaseSHA)
	tw.Printf("Merge base:\t%s\n", snapshot.MergeBaseSHA)
	tw.Printf("Base ref:\t%s\n", snapshot.BaseRef)
	tw.Printf("State:\t%s\n", snapshot.State)
	tw.Printf("Draft:\t%t\n", snapshot.Draft)
	tw.Printf("Indexed as:\t%s\n", snapshot.IndexedAs)
	tw.Printf("Freshness:\t%s\n", snapshot.IndexFreshness)
	tw.Printf("Path count:\t%d\n", snapshot.PathCount)
	tw.Printf("Indexed files:\t%d\n", snapshot.IndexedFileCount)
	tw.Printf("Hunks:\t%d\n", snapshot.HunkCount)
	tw.Printf("Additions:\t%d\n", snapshot.Additions)
	tw.Printf("Deletions:\t%d\n", snapshot.Deletions)
	tw.Printf("Patch bytes:\t%d\n", snapshot.PatchBytes)
	tw.Printf("Last indexed:\t%s\n", humanTimePtr(snapshot.LastIndexedAt))
	tw.Flush()
	return r.FlushTo(out)
}

func printFileChanges(out io.Writer, files []gitindex.FileChange) error {
	r := newRenderer()
	if len(files) == 0 {
		r.Println("no indexed files found")
		return r.FlushTo(out)
	}

	tw := r.Tab()
	tw.Println("PATH\tSTATUS\tKIND\tINDEXED\t+/-")
	for _, file := range files {
		delta := fmt.Sprintf("+%d/-%d", file.Additions, file.Deletions)
		tw.Printf("%s\t%s\t%s\t%s\t%s\n", file.Path, file.Status, file.FileKind, file.IndexedAs, delta)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func printCommitView(out io.Writer, repo string, commit CommitResponse) error {
	r := newRenderer()
	r.Printf("%s commit %s\n", repo, shortSHA(commit.SHA))
	r.Println()
	if strings.TrimSpace(commit.Message) != "" {
		r.Println(strings.TrimSpace(commit.Message))
		r.Println()
	}
	tw := r.Tab()
	tw.Printf("SHA:\t%s\n", commit.SHA)
	tw.Printf("Tree:\t%s\n", commit.TreeSHA)
	tw.Printf("Author:\t%s <%s>\n", commit.AuthorName, commit.AuthorEmail)
	tw.Printf("Authored:\t%s\n", humanTime(commit.AuthoredAt))
	tw.Printf("Committer:\t%s <%s>\n", commit.CommitterName, commit.CommitterEmail)
	tw.Printf("Committed:\t%s\n", humanTime(commit.CommittedAt))
	tw.Printf("Encoding:\t%s\n", coalesce(commit.MessageEncoding, "UTF-8"))
	tw.Printf("Parents:\t%s\n", strings.Join(commit.Parents, ", "))
	tw.Flush()
	if len(commit.ParentDetails) > 0 {
		r.Println()
		r.Println("Parent detail")
		tw = r.Tab()
		tw.Println("PARENT\tINDEXED\tREASON\tPATHS\tHUNKS\t+/-")
		for _, parent := range commit.ParentDetails {
			tw.Printf("%d:%s\t%s\t%s\t%d\t%d\t+%d/-%d\n",
				parent.ParentIndex,
				shortSHA(parent.ParentSHA),
				coalesce(parent.IndexedAs, "full"),
				coalesce(parent.IndexReason, "within_budget"),
				parent.PathCount,
				parent.HunkCount,
				parent.Additions,
				parent.Deletions,
			)
		}
		tw.Flush()
	}
	return r.FlushTo(out)
}

func printCommitFiles(out io.Writer, files []map[string]any) error {
	r := newRenderer()
	if len(files) == 0 {
		r.Println("no indexed commit files found")
		return r.FlushTo(out)
	}

	tw := r.Tab()
	tw.Println("PARENT\tPATH\tSTATUS\tKIND\tINDEXED\t+/-")
	for _, file := range files {
		parentIndex := intFromMap(file, "parent_index")
		parentSHA, _ := file["parent_sha"].(string)
		change, _ := file["file"].(map[string]any)
		path, _ := change["path"].(string)
		status, _ := change["status"].(string)
		fileKind, _ := change["file_kind"].(string)
		indexedAs, _ := change["indexed_as"].(string)
		additions := intFromMap(change, "additions")
		deletions := intFromMap(change, "deletions")
		tw.Printf("%d:%s\t%s\t%s\t%s\t%s\t+%d/-%d\n",
			parentIndex,
			shortSHA(parentSHA),
			path,
			status,
			fileKind,
			indexedAs,
			additions,
			deletions,
		)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func printCompare(out io.Writer, repo string, compare CompareResponse) error {
	r := newRenderer()
	r.Printf("%s compare %s...%s\n", repo, compare.Base, compare.Head)
	r.Println()
	tw := r.Tab()
	tw.Printf("Resolved base:\t%s\n", compare.Resolved.Base)
	tw.Printf("Resolved head:\t%s\n", compare.Resolved.Head)
	tw.Printf("Snapshot PR:\t#%d\n", compare.Snapshot.PullRequestNumber)
	tw.Printf("Indexed as:\t%s\n", compare.Snapshot.IndexedAs)
	tw.Printf("Freshness:\t%s\n", compare.Snapshot.IndexFreshness)
	tw.Printf("Paths:\t%d\n", len(compare.Files))
	tw.Flush()
	r.Println()
	if err := r.FlushTo(out); err != nil {
		return err
	}
	return printFileChanges(out, compare.Files)
}

func printSearchMatches(out io.Writer, matches []gitindex.SearchMatch) error {
	r := newRenderer()
	if len(matches) == 0 {
		r.Println("no matching pull requests found")
		return r.FlushTo(out)
	}

	tw := r.Tab()
	tw.Println("PR\tSTATE\tSCORE\tINDEXED\tFRESHNESS\tWHY")
	for _, match := range matches {
		state := match.State
		if match.Draft {
			state = "draft"
		}
		tw.Printf("#%d\t%s\t%.0f\t%s\t%s\t%s\n",
			match.PullRequestNumber,
			state,
			match.Score,
			match.IndexedAs,
			match.IndexFreshness,
			formatSearchReasons(match),
		)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func printMentionMatches(out io.Writer, matches []searchindex.MentionMatch) error {
	r := newRenderer()
	if len(matches) == 0 {
		r.Println("no matching mentions found")
		return r.FlushTo(out)
	}

	tw := r.Tab()
	tw.Println("TYPE\tRESOURCE\tFIELD\tSCORE\tEXCERPT")
	for _, match := range matches {
		tw.Printf("%s\t#%d\t%s\t%.2f\t%s\n",
			match.Resource.Type,
			match.Resource.Number,
			match.MatchedField,
			match.Score,
			truncate(match.Excerpt, 120),
		)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func printRepoSearchStatus(out io.Writer, status RepoSearchStatusResponse) error {
	r := newRenderer()
	r.Printf("%s text search status\n", status.Repository.FullName)
	r.Println()
	tw := r.Tab()
	tw.Printf("Text index status:\t%s\n", coalesce(status.TextIndexStatus, "-"))
	tw.Printf("Document count:\t%d\n", status.DocumentCount)
	tw.Printf("Freshness:\t%s\n", coalesce(status.Freshness, "-"))
	tw.Printf("Coverage:\t%s\n", coalesce(status.Coverage, "-"))
	tw.Printf("Last indexed:\t%s\n", humanTimePtr(status.LastIndexedAt))
	tw.Printf("Last source update:\t%s\n", humanTimePtr(status.LastSourceUpdateAt))
	tw.Printf("Last error:\t%s\n", coalesce(status.LastError, "-"))
	tw.Flush()
	return r.FlushTo(out)
}

func printStructuralSearch(out io.Writer, result StructuralSearchResponse) error {
	r := newRenderer()
	r.Printf("%s ast-grep search\n", result.Repository.FullName)
	r.Println()
	tw := r.Tab()
	tw.Printf("Resolved commit:\t%s\n", result.ResolvedCommitSHA)
	tw.Printf("Resolved ref:\t%s\n", coalesce(result.ResolvedRef, "-"))
	tw.Printf("Matches:\t%d\n", len(result.Matches))
	tw.Printf("Truncated:\t%t\n", result.Truncated)
	tw.Flush()
	if len(result.Matches) == 0 {
		r.Println()
		r.Println("no structural matches found")
		return r.FlushTo(out)
	}
	r.Println()
	tw = r.Tab()
	tw.Println("PATH\tLOCATION\tCAPTURES\tTEXT")
	for _, match := range result.Matches {
		tw.Printf("%s\t%d:%d-%d:%d\t%s\t%s\n",
			match.Path,
			match.StartLine,
			match.StartColumn,
			match.EndLine,
			match.EndColumn,
			formatStructuralCaptures(match.MetaVariables),
			truncate(strings.TrimSpace(match.Text), 120),
		)
	}
	tw.Flush()
	return r.FlushTo(out)
}

func formatSearchReasons(match gitindex.SearchMatch) string {
	parts := make([]string, 0, 4)
	if len(match.SharedPaths) > 0 {
		paths := append([]string(nil), match.SharedPaths...)
		sort.Strings(paths)
		parts = append(parts, "paths="+strings.Join(paths, ","))
	}
	if match.OverlappingHunks > 0 {
		parts = append(parts, fmt.Sprintf("overlapping_hunks=%d", match.OverlappingHunks))
	}
	if len(match.MatchedRanges) > 0 {
		paths := make([]string, 0, len(match.MatchedRanges))
		seen := map[string]struct{}{}
		for _, mr := range match.MatchedRanges {
			if _, ok := seen[mr.Path]; ok {
				continue
			}
			seen[mr.Path] = struct{}{}
			paths = append(paths, mr.Path)
		}
		sort.Strings(paths)
		parts = append(parts, "ranges="+strings.Join(paths, ","))
	}
	if len(match.Reasons) > 0 {
		parts = append(parts, "reasons="+strings.Join(match.Reasons, ","))
	}
	return strings.Join(parts, " | ")
}

func formatPullRef(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

func formatStructuralCaptures(meta gitindex.StructuralMetaVariable) string {
	parts := make([]string, 0, len(meta.Single)+len(meta.Transformed))
	parts = appendSortedCaptureValues(parts, meta.Single)
	parts = appendSortedMultiCaptureValues(parts, meta.Multi)
	parts = appendSortedCaptureValues(parts, meta.Transformed)
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func appendSortedCaptureValues(parts []string, values map[string]string) []string {
	keys := sortedCaptureKeys(values)
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return parts
}

func appendSortedMultiCaptureValues(parts []string, values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"=["+strings.Join(values[key], ",")+"]")
	}
	return parts
}

func sortedCaptureKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func intFromMap(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func intPtrString(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *v)
}
