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
			printRepoSearchStatus(cmd.OutOrStdout(), status)
			return nil
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
			printSearchMatches(cmd.OutOrStdout(), matches)
			return nil
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
			printSearchMatches(cmd.OutOrStdout(), matches)
			return nil
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
			printSearchMatches(cmd.OutOrStdout(), matches)
			return nil
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
			printMentionMatches(cmd.OutOrStdout(), matches)
			return nil
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
