package ghr

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dutifuldev/ghreplica/internal/gitindex"
)

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
