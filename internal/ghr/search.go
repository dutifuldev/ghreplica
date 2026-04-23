package ghr

import (
	"context"
	"errors"
	"strings"

	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"github.com/spf13/cobra"
)

func newSearchCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Query ghreplica-specific search surfaces",
	}
	cmd.AddCommand(newSearchStatusCmd(opts))
	cmd.AddCommand(newSearchRelatedPRsCmd(opts))
	cmd.AddCommand(newSearchPRsByPathsCmd(opts))
	cmd.AddCommand(newSearchPRsByRangesCmd(opts))
	cmd.AddCommand(newSearchMentionsCmd(opts))
	cmd.AddCommand(newSearchASTGrepCmd(opts))
	return cmd
}

func newSearchStatusCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show repo-level text-search indexing status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			status, err := client.GetRepoSearchStatus(context.Background(), repo)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), status, jsonFields)
			}
			return printRepoSearchStatus(cmd.OutOrStdout(), status)
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newSearchRelatedPRsCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	var mode string
	var state string
	var limit int
	cmd := &cobra.Command{
		Use:   "related-prs <number>",
		Short: "Find PRs related to a given PR by path or range overlap",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			number, err := resolveNumberArg(args[0])
			if err != nil {
				return err
			}
			client := clientFor(opts)
			matches, err := client.SearchRelatedPullRequests(context.Background(), repo, number, mode, state, limit)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), matches, jsonFields)
			}
			return printSearchMatches(cmd.OutOrStdout(), matches)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "path_overlap", "Search mode: path_overlap or range_overlap")
	cmd.Flags().StringVarP(&state, "state", "s", "open", "Filter by state: open, closed, all")
	cmd.Flags().IntVarP(&limit, "limit", "L", 20, "Maximum number of results to fetch")
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newSearchPRsByPathsCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	var paths []string
	var state string
	var limit int
	cmd := &cobra.Command{
		Use:   "prs-by-paths",
		Short: "Find PRs that touch the given file paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			paths = normalizeStringSlice(paths)
			if len(paths) == 0 {
				return errors.New("at least one --path is required")
			}
			client := clientFor(opts)
			matches, err := client.SearchPullRequestsByPaths(context.Background(), repo, paths, state, limit)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), matches, jsonFields)
			}
			return printSearchMatches(cmd.OutOrStdout(), matches)
		},
	}
	cmd.Flags().StringSliceVarP(&paths, "path", "p", nil, "File path to search for; repeat to search multiple paths")
	cmd.Flags().StringVarP(&state, "state", "s", "open", "Filter by state: open, closed, all")
	cmd.Flags().IntVarP(&limit, "limit", "L", 20, "Maximum number of results to fetch")
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newSearchPRsByRangesCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	var paths []string
	var starts []int
	var ends []int
	var state string
	var limit int
	cmd := &cobra.Command{
		Use:   "prs-by-ranges",
		Short: "Find PRs that overlap the given file ranges",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			ranges, err := buildSearchRanges(paths, starts, ends)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			matches, err := client.SearchPullRequestsByRanges(context.Background(), repo, ranges, state, limit)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), matches, jsonFields)
			}
			return printSearchMatches(cmd.OutOrStdout(), matches)
		},
	}
	cmd.Flags().StringSliceVarP(&paths, "path", "p", nil, "File path to search within; repeat with matching --start and --end values")
	cmd.Flags().IntSliceVar(&starts, "start", nil, "Start line for a range; repeat to match each --path")
	cmd.Flags().IntSliceVar(&ends, "end", nil, "End line for a range; repeat to match each --path")
	cmd.Flags().StringVarP(&state, "state", "s", "open", "Filter by state: open, closed, all")
	cmd.Flags().IntVarP(&limit, "limit", "L", 20, "Maximum number of results to fetch")
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func buildSearchRanges(paths []string, starts, ends []int) ([]SearchRange, error) {
	paths = normalizeStringSlice(paths)
	if len(paths) == 0 {
		return nil, errors.New("at least one --path is required")
	}
	if len(paths) != len(starts) || len(paths) != len(ends) {
		return nil, errors.New("--path, --start, and --end must be provided the same number of times")
	}
	ranges := make([]SearchRange, 0, len(paths))
	for i, path := range paths {
		if starts[i] <= 0 || ends[i] <= 0 || ends[i] < starts[i] {
			return nil, errors.New("each range must have positive --start and --end with end >= start")
		}
		ranges = append(ranges, SearchRange{
			Path:  path,
			Start: starts[i],
			End:   ends[i],
		})
	}
	return ranges, nil
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func newSearchMentionsCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	var query string
	var mode string
	var scopes []string
	var state string
	var author string
	var limit int
	var page int
	cmd := &cobra.Command{
		Use:   "mentions",
		Short: "Search mirrored PR, issue, and discussion text",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			request := searchindex.MentionRequest{
				Query:  strings.TrimSpace(query),
				Mode:   strings.TrimSpace(mode),
				Scopes: normalizeStringSlice(scopes),
				State:  strings.TrimSpace(state),
				Author: strings.TrimSpace(author),
				Limit:  limit,
				Page:   page,
			}
			client := clientFor(opts)
			matches, err := client.SearchMentions(context.Background(), repo, request)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), matches, jsonFields)
			}
			return printMentionMatches(cmd.OutOrStdout(), matches)
		},
	}
	cmd.Flags().StringVarP(&query, "query", "q", "", "Search expression")
	cmd.Flags().StringVar(&mode, "mode", searchindex.ModeFTS, "Search mode: fts, fuzzy, or regex")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "Search scope; repeat for multiple scopes")
	cmd.Flags().StringVarP(&state, "state", "s", "all", "Filter by state: open, closed, all")
	cmd.Flags().StringVar(&author, "author", "", "Filter by author login")
	cmd.Flags().IntVarP(&limit, "limit", "L", 20, "Maximum number of results to fetch")
	cmd.Flags().IntVar(&page, "page", 1, "Page number to fetch")
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func newSearchASTGrepCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	var commitSHA string
	var ref string
	var pullRequestNumber int
	var language string
	var pattern string
	var paths []string
	var changedFilesOnly bool
	var limit int
	cmd := &cobra.Command{
		Use:   "ast-grep",
		Short: "Run structural code search against a specific commit, ref, or PR head",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			request, err := buildASTGrepSearchRequest(commitSHA, ref, pullRequestNumber, language, pattern, paths, changedFilesOnly, limit)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			result, err := client.SearchASTGrep(context.Background(), repo, request)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), result, jsonFields)
			}
			return printStructuralSearch(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&commitSHA, "commit", "", "Search a specific commit SHA")
	cmd.Flags().StringVar(&ref, "ref", "", "Search a specific branch or ref")
	cmd.Flags().IntVar(&pullRequestNumber, "pr", 0, "Search the current head of a pull request")
	cmd.Flags().StringVar(&language, "language", "", "Rule language, for example typescript or go")
	cmd.Flags().StringVar(&pattern, "pattern", "", "AST-grep pattern to search for")
	cmd.Flags().StringSliceVarP(&paths, "path", "p", nil, "Restrict search to one or more file paths")
	cmd.Flags().BoolVar(&changedFilesOnly, "changed-files-only", false, "Restrict a pull-request search to files changed by that PR")
	cmd.Flags().IntVarP(&limit, "limit", "L", 100, "Maximum number of matches to return")
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	_ = cmd.MarkFlagRequired("language")
	_ = cmd.MarkFlagRequired("pattern")
	return cmd
}

func buildASTGrepSearchRequest(commitSHA, ref string, pullRequestNumber int, language, pattern string, paths []string, changedFilesOnly bool, limit int) (StructuralSearchRequest, error) {
	targets := 0
	if strings.TrimSpace(commitSHA) != "" {
		targets++
	}
	if strings.TrimSpace(ref) != "" {
		targets++
	}
	if pullRequestNumber > 0 {
		targets++
	}
	if targets != 1 {
		return StructuralSearchRequest{}, errors.New("exactly one of --commit, --ref, or --pr is required")
	}
	if changedFilesOnly && pullRequestNumber <= 0 {
		return StructuralSearchRequest{}, errors.New("--changed-files-only requires --pr")
	}
	if strings.TrimSpace(language) == "" {
		return StructuralSearchRequest{}, errors.New("--language is required")
	}
	if strings.TrimSpace(pattern) == "" {
		return StructuralSearchRequest{}, errors.New("--pattern is required")
	}
	return StructuralSearchRequest{
		CommitSHA:         strings.TrimSpace(commitSHA),
		Ref:               strings.TrimSpace(ref),
		PullRequestNumber: pullRequestNumber,
		Language:          strings.TrimSpace(language),
		Rule:              map[string]any{"pattern": strings.TrimSpace(pattern)},
		Paths:             normalizeStringSlice(paths),
		ChangedFilesOnly:  changedFilesOnly,
		Limit:             limit,
	}, nil
}
