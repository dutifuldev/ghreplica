package ghr

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
)

func printRepoChangeStatus(out io.Writer, status RepoChangeStatusResponse) {
	fmt.Fprintf(out, "%s change status\n", status.FullName)
	fmt.Fprintln(out)
	tw := newTabWriter(out)
	fmt.Fprintf(tw, "Backfill mode:\t%s\n", status.BackfillMode)
	fmt.Fprintf(tw, "Priority:\t%d\n", status.BackfillPriority)
	fmt.Fprintf(tw, "Targeted refresh pending:\t%t\n", status.TargetedRefreshPending)
	fmt.Fprintf(tw, "Targeted refresh running:\t%t\n", status.TargetedRefreshRunning)
	fmt.Fprintf(tw, "Inventory current gen:\t%d\n", status.InventoryGenerationCurrent)
	fmt.Fprintf(tw, "Inventory building gen:\t%s\n", intPtrString(status.InventoryGenerationBuilding))
	fmt.Fprintf(tw, "Inventory needs refresh:\t%t\n", status.InventoryNeedsRefresh)
	fmt.Fprintf(tw, "Inventory scan running:\t%t\n", status.InventoryScanRunning)
	fmt.Fprintf(tw, "Backfill running:\t%t\n", status.BackfillRunning)
	fmt.Fprintf(tw, "Backfill generation:\t%d\n", status.BackfillGeneration)
	fmt.Fprintf(tw, "Open PRs total:\t%d\n", status.OpenPRTotal)
	fmt.Fprintf(tw, "Open PRs current:\t%d\n", status.OpenPRCurrent)
	fmt.Fprintf(tw, "Open PRs stale:\t%d\n", status.OpenPRStale)
	fmt.Fprintf(tw, "Open PRs missing:\t%s\n", missingCountString(status.OpenPRMissing, status.OpenPRMissingStale))
	fmt.Fprintf(tw, "Backfill cursor:\t%s\n", intPtrString(status.BackfillCursor))
	fmt.Fprintf(tw, "Backfill cursor updated:\t%s\n", humanTimePtr(status.BackfillCursorUpdatedAt))
	fmt.Fprintf(tw, "Last webhook:\t%s\n", humanTimePtr(status.LastWebhookAt))
	fmt.Fprintf(tw, "Last inventory scan started:\t%s\n", humanTimePtr(status.LastInventoryScanStartedAt))
	fmt.Fprintf(tw, "Last inventory scan finished:\t%s\n", humanTimePtr(status.LastInventoryScanFinishedAt))
	fmt.Fprintf(tw, "Last inventory scan succeeded:\t%s\n", humanTimePtr(status.LastInventoryScanSucceededAt))
	fmt.Fprintf(tw, "Inventory last committed:\t%s\n", humanTimePtr(status.InventoryLastCommittedAt))
	fmt.Fprintf(tw, "Last backfill started:\t%s\n", humanTimePtr(status.LastBackfillStartedAt))
	fmt.Fprintf(tw, "Last backfill finished:\t%s\n", humanTimePtr(status.LastBackfillFinishedAt))
	fmt.Fprintf(tw, "Last error:\t%s\n", coalesce(status.LastError, "-"))
	_ = tw.Flush()
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

func printPullRequestChangeStatus(out io.Writer, repo string, status PullRequestChangeStatusResponse) {
	fmt.Fprintf(out, "%s change status\n", formatPullRef(repo, status.PullRequestNumber))
	fmt.Fprintln(out)
	tw := newTabWriter(out)
	fmt.Fprintf(tw, "Indexed:\t%t\n", status.Indexed)
	fmt.Fprintf(tw, "State:\t%s\n", coalesce(status.State, "-"))
	fmt.Fprintf(tw, "Draft:\t%t\n", status.Draft)
	fmt.Fprintf(tw, "Head SHA:\t%s\n", coalesce(status.HeadSHA, "-"))
	fmt.Fprintf(tw, "Base SHA:\t%s\n", coalesce(status.BaseSHA, "-"))
	fmt.Fprintf(tw, "Merge base:\t%s\n", coalesce(status.MergeBaseSHA, "-"))
	fmt.Fprintf(tw, "Base ref:\t%s\n", coalesce(status.BaseRef, "-"))
	fmt.Fprintf(tw, "Indexed as:\t%s\n", coalesce(status.IndexedAs, "-"))
	fmt.Fprintf(tw, "Freshness:\t%s\n", coalesce(status.IndexFreshness, "-"))
	fmt.Fprintf(tw, "Changed files:\t%d\n", status.ChangedFiles)
	fmt.Fprintf(tw, "Indexed files:\t%d\n", status.IndexedFileCount)
	fmt.Fprintf(tw, "Path-only files:\t%d\n", status.PathOnlyFileCount)
	fmt.Fprintf(tw, "Skipped files:\t%d\n", status.SkippedFileCount)
	fmt.Fprintf(tw, "Hunks:\t%d\n", status.HunkCount)
	fmt.Fprintf(tw, "Additions:\t%d\n", status.Additions)
	fmt.Fprintf(tw, "Deletions:\t%d\n", status.Deletions)
	fmt.Fprintf(tw, "Patch bytes:\t%d\n", status.PatchBytes)
	fmt.Fprintf(tw, "Backfill in progress:\t%t\n", status.BackfillInProgress)
	fmt.Fprintf(tw, "Inventory needs refresh:\t%t\n", status.InventoryNeedsRefresh)
	fmt.Fprintf(tw, "Last indexed:\t%s\n", humanTimePtr(status.LastIndexedAt))
	fmt.Fprintf(tw, "Last error:\t%s\n", coalesce(status.LastError, "-"))
	_ = tw.Flush()
}

func printPullRequestChangeSnapshot(out io.Writer, repo string, snapshot PullRequestChangeSnapshotResponse) {
	fmt.Fprintf(out, "%s change snapshot\n", formatPullRef(repo, snapshot.PullRequestNumber))
	fmt.Fprintln(out)

	tw := newTabWriter(out)
	fmt.Fprintf(tw, "Head SHA:\t%s\n", snapshot.HeadSHA)
	fmt.Fprintf(tw, "Base SHA:\t%s\n", snapshot.BaseSHA)
	fmt.Fprintf(tw, "Merge base:\t%s\n", snapshot.MergeBaseSHA)
	fmt.Fprintf(tw, "Base ref:\t%s\n", snapshot.BaseRef)
	fmt.Fprintf(tw, "State:\t%s\n", snapshot.State)
	fmt.Fprintf(tw, "Draft:\t%t\n", snapshot.Draft)
	fmt.Fprintf(tw, "Indexed as:\t%s\n", snapshot.IndexedAs)
	fmt.Fprintf(tw, "Freshness:\t%s\n", snapshot.IndexFreshness)
	fmt.Fprintf(tw, "Path count:\t%d\n", snapshot.PathCount)
	fmt.Fprintf(tw, "Indexed files:\t%d\n", snapshot.IndexedFileCount)
	fmt.Fprintf(tw, "Hunks:\t%d\n", snapshot.HunkCount)
	fmt.Fprintf(tw, "Additions:\t%d\n", snapshot.Additions)
	fmt.Fprintf(tw, "Deletions:\t%d\n", snapshot.Deletions)
	fmt.Fprintf(tw, "Patch bytes:\t%d\n", snapshot.PatchBytes)
	fmt.Fprintf(tw, "Last indexed:\t%s\n", humanTimePtr(snapshot.LastIndexedAt))
	_ = tw.Flush()
}

func printFileChanges(out io.Writer, files []gitindex.FileChange) {
	if len(files) == 0 {
		fmt.Fprintln(out, "no indexed files found")
		return
	}

	tw := newTabWriter(out)
	fmt.Fprintln(tw, "PATH\tSTATUS\tKIND\tINDEXED\t+/-")
	for _, file := range files {
		delta := fmt.Sprintf("+%d/-%d", file.Additions, file.Deletions)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", file.Path, file.Status, file.FileKind, file.IndexedAs, delta)
	}
	_ = tw.Flush()
}

func printCommitView(out io.Writer, repo string, commit CommitResponse) {
	fmt.Fprintf(out, "%s commit %s\n", repo, shortSHA(commit.SHA))
	fmt.Fprintln(out)
	if strings.TrimSpace(commit.Message) != "" {
		fmt.Fprintln(out, strings.TrimSpace(commit.Message))
		fmt.Fprintln(out)
	}
	tw := newTabWriter(out)
	fmt.Fprintf(tw, "SHA:\t%s\n", commit.SHA)
	fmt.Fprintf(tw, "Tree:\t%s\n", commit.TreeSHA)
	fmt.Fprintf(tw, "Author:\t%s <%s>\n", commit.AuthorName, commit.AuthorEmail)
	fmt.Fprintf(tw, "Authored:\t%s\n", humanTime(commit.AuthoredAt))
	fmt.Fprintf(tw, "Committer:\t%s <%s>\n", commit.CommitterName, commit.CommitterEmail)
	fmt.Fprintf(tw, "Committed:\t%s\n", humanTime(commit.CommittedAt))
	fmt.Fprintf(tw, "Encoding:\t%s\n", coalesce(commit.MessageEncoding, "UTF-8"))
	fmt.Fprintf(tw, "Parents:\t%s\n", strings.Join(commit.Parents, ", "))
	_ = tw.Flush()
	if len(commit.ParentDetails) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Parent detail")
		tw = newTabWriter(out)
		fmt.Fprintln(tw, "PARENT\tINDEXED\tREASON\tPATHS\tHUNKS\t+/-")
		for _, parent := range commit.ParentDetails {
			fmt.Fprintf(tw, "%d:%s\t%s\t%s\t%d\t%d\t+%d/-%d\n",
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
		_ = tw.Flush()
	}
}

func printCommitFiles(out io.Writer, files []map[string]any) {
	if len(files) == 0 {
		fmt.Fprintln(out, "no indexed commit files found")
		return
	}

	tw := newTabWriter(out)
	fmt.Fprintln(tw, "PARENT\tPATH\tSTATUS\tKIND\tINDEXED\t+/-")
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
		fmt.Fprintf(tw, "%d:%s\t%s\t%s\t%s\t%s\t+%d/-%d\n",
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
	_ = tw.Flush()
}

func printCompare(out io.Writer, repo string, compare CompareResponse) {
	fmt.Fprintf(out, "%s compare %s...%s\n", repo, compare.Base, compare.Head)
	fmt.Fprintln(out)
	tw := newTabWriter(out)
	fmt.Fprintf(tw, "Resolved base:\t%s\n", compare.Resolved.Base)
	fmt.Fprintf(tw, "Resolved head:\t%s\n", compare.Resolved.Head)
	fmt.Fprintf(tw, "Snapshot PR:\t#%d\n", compare.Snapshot.PullRequestNumber)
	fmt.Fprintf(tw, "Indexed as:\t%s\n", compare.Snapshot.IndexedAs)
	fmt.Fprintf(tw, "Freshness:\t%s\n", compare.Snapshot.IndexFreshness)
	fmt.Fprintf(tw, "Paths:\t%d\n", len(compare.Files))
	_ = tw.Flush()
	fmt.Fprintln(out)
	printFileChanges(out, compare.Files)
}

func printSearchMatches(out io.Writer, matches []gitindex.SearchMatch) {
	if len(matches) == 0 {
		fmt.Fprintln(out, "no matching pull requests found")
		return
	}

	tw := newTabWriter(out)
	fmt.Fprintln(tw, "PR\tSTATE\tSCORE\tINDEXED\tFRESHNESS\tWHY")
	for _, match := range matches {
		state := match.State
		if match.Draft {
			state = "draft"
		}
		fmt.Fprintf(tw, "#%d\t%s\t%.0f\t%s\t%s\t%s\n",
			match.PullRequestNumber,
			state,
			match.Score,
			match.IndexedAs,
			match.IndexFreshness,
			formatSearchReasons(match),
		)
	}
	_ = tw.Flush()
}

func printMentionMatches(out io.Writer, matches []searchindex.MentionMatch) {
	if len(matches) == 0 {
		fmt.Fprintln(out, "no matching mentions found")
		return
	}

	tw := newTabWriter(out)
	fmt.Fprintln(tw, "TYPE\tRESOURCE\tFIELD\tSCORE\tEXCERPT")
	for _, match := range matches {
		fmt.Fprintf(tw, "%s\t#%d\t%s\t%.2f\t%s\n",
			match.Resource.Type,
			match.Resource.Number,
			match.MatchedField,
			match.Score,
			truncate(match.Excerpt, 120),
		)
	}
	_ = tw.Flush()
}

func printRepoSearchStatus(out io.Writer, status RepoSearchStatusResponse) {
	fmt.Fprintf(out, "%s text search status\n", status.Repository.FullName)
	fmt.Fprintln(out)
	tw := newTabWriter(out)
	fmt.Fprintf(tw, "Text index status:\t%s\n", coalesce(status.TextIndexStatus, "-"))
	fmt.Fprintf(tw, "Document count:\t%d\n", status.DocumentCount)
	fmt.Fprintf(tw, "Freshness:\t%s\n", coalesce(status.Freshness, "-"))
	fmt.Fprintf(tw, "Coverage:\t%s\n", coalesce(status.Coverage, "-"))
	fmt.Fprintf(tw, "Last indexed:\t%s\n", humanTimePtr(status.LastIndexedAt))
	fmt.Fprintf(tw, "Last source update:\t%s\n", humanTimePtr(status.LastSourceUpdateAt))
	fmt.Fprintf(tw, "Last error:\t%s\n", coalesce(status.LastError, "-"))
	_ = tw.Flush()
}

func printStructuralSearch(out io.Writer, result StructuralSearchResponse) {
	fmt.Fprintf(out, "%s ast-grep search\n", result.Repository.FullName)
	fmt.Fprintln(out)
	tw := newTabWriter(out)
	fmt.Fprintf(tw, "Resolved commit:\t%s\n", result.ResolvedCommitSHA)
	fmt.Fprintf(tw, "Resolved ref:\t%s\n", coalesce(result.ResolvedRef, "-"))
	fmt.Fprintf(tw, "Matches:\t%d\n", len(result.Matches))
	fmt.Fprintf(tw, "Truncated:\t%t\n", result.Truncated)
	_ = tw.Flush()
	if len(result.Matches) == 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "no structural matches found")
		return
	}
	fmt.Fprintln(out)
	tw = newTabWriter(out)
	fmt.Fprintln(tw, "PATH\tLOCATION\tCAPTURES\tTEXT")
	for _, match := range result.Matches {
		fmt.Fprintf(tw, "%s\t%d:%d-%d:%d\t%s\t%s\n",
			match.Path,
			match.StartLine,
			match.StartColumn,
			match.EndLine,
			match.EndColumn,
			formatStructuralCaptures(match.MetaVariables),
			truncate(strings.TrimSpace(match.Text), 120),
		)
	}
	_ = tw.Flush()
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
	if len(meta.Single) > 0 {
		keys := make([]string, 0, len(meta.Single))
		for key := range meta.Single {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key+"="+meta.Single[key])
		}
	}
	if len(meta.Multi) > 0 {
		keys := make([]string, 0, len(meta.Multi))
		for key := range meta.Multi {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key+"=["+strings.Join(meta.Multi[key], ",")+"]")
		}
	}
	if len(meta.Transformed) > 0 {
		keys := make([]string, 0, len(meta.Transformed))
		for key := range meta.Transformed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key+"="+meta.Transformed[key])
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
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
