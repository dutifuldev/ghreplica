package ghr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type RootOptions struct {
	BaseURL string
	Repo    string
}

func NewRootCmd() *cobra.Command {
	opts := &RootOptions{
		BaseURL: strings.TrimSpace(os.Getenv("GHR_BASE_URL")),
	}

	cmd := &cobra.Command{
		Use:   "ghr",
		Short: "Read mirrored GitHub data from ghreplica",
	}

	cmd.PersistentFlags().StringVar(&opts.BaseURL, "base-url", opts.BaseURL, "ghreplica base URL")
	cmd.PersistentFlags().StringVarP(&opts.Repo, "repo", "R", "", "Select another repository using the OWNER/REPO format")

	cmd.AddCommand(newRepoCmd(opts))
	cmd.AddCommand(newMirrorCmd(opts))
	cmd.AddCommand(newIssueCmd(opts))
	cmd.AddCommand(newPRCmd(opts))
	cmd.AddCommand(newChangesCmd(opts))
	cmd.AddCommand(newSearchCmd(opts))

	return cmd
}

func clientFor(opts *RootOptions) *Client {
	return NewClient(opts.BaseURL)
}

func resolveRepo(arg string, opts *RootOptions) (string, error) {
	repo := strings.TrimSpace(arg)
	if repo == "" {
		repo = strings.TrimSpace(opts.Repo)
	}
	if repo == "" {
		return "", errors.New("repository is required; pass OWNER/REPO or use --repo")
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", errors.New("repository must be in OWNER/REPO format")
	}
	return strings.TrimSpace(parts[0]) + "/" + strings.TrimSpace(parts[1]), nil
}

func resolveNumberArg(raw string) (int, error) {
	number, err := strconv.Atoi(raw)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid number: %q", raw)
	}
	return number, nil
}

func newRepoCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "View mirrored repository data",
	}
	cmd.AddCommand(newRepoViewCmd(opts))
	cmd.AddCommand(newRepoStatusCmd(opts))
	return cmd
}

func newRepoViewCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	var openInBrowser bool
	cmd := &cobra.Command{
		Use:   "view [OWNER/REPO]",
		Short: "View a repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoArg := ""
			if len(args) == 1 {
				repoArg = args[0]
			}
			repo, err := resolveRepo(repoArg, opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			resp, err := client.GetRepository(context.Background(), repo)
			if err != nil {
				return err
			}
			if openInBrowser {
				return openURL(resp.HTMLURL)
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), resp, jsonFields)
			}
			return printRepoView(cmd.OutOrStdout(), resp)
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	cmd.Flags().BoolVarP(&openInBrowser, "web", "w", false, "Open a repository in the browser")
	return cmd
}

func newRepoStatusCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "View ghreplica mirror status for a repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			status, err := client.GetMirrorRepository(context.Background(), repo)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), status, jsonFields)
			}
			return printMirrorRepository(cmd.OutOrStdout(), status)
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newMirrorCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mirror",
		Short: "View mirror metadata and sync status",
	}
	cmd.AddCommand(newMirrorListCmd(opts))
	cmd.AddCommand(newMirrorViewCmd(opts))
	cmd.AddCommand(newMirrorStatusCmd(opts))
	return cmd
}

func newMirrorListCmd(opts *RootOptions) *cobra.Command {
	var (
		page       int
		perPage    int
		jsonFields string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List mirrored repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := clientFor(opts)
			repos, err := client.ListMirrorRepositories(context.Background(), page, perPage)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), repos, jsonFields)
			}
			return printMirrorRepositoryList(cmd.OutOrStdout(), repos)
		},
	}
	cmd.Flags().IntVar(&page, "page", 1, "Page number")
	cmd.Flags().IntVarP(&perPage, "per-page", "L", 30, "Page size")
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newMirrorViewCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "view [OWNER/REPO]",
		Short: "View stable mirror metadata for a repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoArg := ""
			if len(args) == 1 {
				repoArg = args[0]
			}
			repo, err := resolveRepo(repoArg, opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			status, err := client.GetMirrorRepository(context.Background(), repo)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), status, jsonFields)
			}
			return printMirrorRepository(cmd.OutOrStdout(), status)
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newMirrorStatusCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "status [OWNER/REPO]",
		Short: "View live mirror sync status for a repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoArg := ""
			if len(args) == 1 {
				repoArg = args[0]
			}
			repo, err := resolveRepo(repoArg, opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			status, err := client.GetMirrorRepositoryStatus(context.Background(), repo)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), status, jsonFields)
			}
			return printMirrorRepositoryStatus(cmd.OutOrStdout(), status)
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newIssueCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "View mirrored issues",
	}
	cmd.AddCommand(newIssueListCmd(opts))
	cmd.AddCommand(newIssueViewCmd(opts))
	cmd.AddCommand(newIssueCommentsCmd(opts))
	return cmd
}

func newIssueListCmd(opts *RootOptions) *cobra.Command {
	var state string
	var limit int
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues in a repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			issues, err := client.ListIssues(context.Background(), repo, state, limit)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), issues, jsonFields)
			}
			return printIssueList(cmd.OutOrStdout(), issues)
		},
	}
	cmd.Flags().StringVarP(&state, "state", "s", "open", "Filter by state: open, closed, all")
	cmd.Flags().IntVarP(&limit, "limit", "L", 30, "Maximum number of issues to fetch")
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newIssueViewCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	var showComments bool
	var openInBrowser bool
	cmd := &cobra.Command{
		Use:   "view <number>",
		Short: "View an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIssueView(cmd, opts, args[0], issueViewOptions{
				jsonFields:    jsonFields,
				showComments:  showComments,
				openInBrowser: openInBrowser,
			})
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	cmd.Flags().BoolVarP(&showComments, "comments", "c", false, "View issue comments")
	cmd.Flags().BoolVarP(&openInBrowser, "web", "w", false, "Open an issue in the browser")
	return cmd
}

func newIssueCommentsCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "comments <number>",
		Short: "View issue comments",
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
			comments, err := client.ListIssueComments(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), comments, jsonFields)
			}
			return printIssueComments(cmd.OutOrStdout(), comments)
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newPRCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "View mirrored pull requests",
	}
	cmd.AddCommand(newPRListCmd(opts))
	cmd.AddCommand(newPRViewCmd(opts))
	cmd.AddCommand(newPRReviewsCmd(opts))
	cmd.AddCommand(newPRCommentsCmd(opts))
	return cmd
}

func newPRListCmd(opts *RootOptions) *cobra.Command {
	var state string
	var limit int
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List pull requests in a repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			pulls, err := client.ListPullRequests(context.Background(), repo, state, limit)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), pulls, jsonFields)
			}
			return printPullList(cmd.OutOrStdout(), pulls)
		},
	}
	cmd.Flags().StringVarP(&state, "state", "s", "open", "Filter by state: open, closed, all")
	cmd.Flags().IntVarP(&limit, "limit", "L", 30, "Maximum number of pull requests to fetch")
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newPRViewCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	var showComments bool
	var openInBrowser bool
	cmd := &cobra.Command{
		Use:   "view <number>",
		Short: "View a pull request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPullRequestView(cmd, opts, args[0], issueViewOptions{
				jsonFields:    jsonFields,
				showComments:  showComments,
				openInBrowser: openInBrowser,
			})
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	cmd.Flags().BoolVarP(&showComments, "comments", "c", false, "View pull request comments")
	cmd.Flags().BoolVarP(&openInBrowser, "web", "w", false, "Open a pull request in the browser")
	return cmd
}

type issueViewOptions struct {
	jsonFields    string
	showComments  bool
	openInBrowser bool
}

func runIssueView(cmd *cobra.Command, opts *RootOptions, arg string, options issueViewOptions) error {
	repo, number, client, err := resolveViewRequest(arg, opts)
	if err != nil {
		return err
	}
	issue, err := client.GetIssue(context.Background(), repo, number)
	if err != nil {
		return err
	}
	if options.openInBrowser {
		return openURL(issue.HTMLURL)
	}
	if strings.TrimSpace(options.jsonFields) != "" {
		return writeJSON(cmd.OutOrStdout(), issue, options.jsonFields)
	}
	if err := printIssueView(cmd.OutOrStdout(), repo, issue); err != nil {
		return err
	}
	return printOptionalIssueComments(cmd, client, repo, number, options.showComments)
}

func runPullRequestView(cmd *cobra.Command, opts *RootOptions, arg string, options issueViewOptions) error {
	repo, number, client, err := resolveViewRequest(arg, opts)
	if err != nil {
		return err
	}
	pr, err := client.GetPullRequest(context.Background(), repo, number)
	if err != nil {
		return err
	}
	if options.openInBrowser {
		return openURL(pr.HTMLURL)
	}
	if strings.TrimSpace(options.jsonFields) != "" {
		return writeJSON(cmd.OutOrStdout(), pr, options.jsonFields)
	}
	if err := printPullView(cmd.OutOrStdout(), repo, pr); err != nil {
		return err
	}
	return printOptionalIssueComments(cmd, client, repo, number, options.showComments)
}

func resolveViewRequest(arg string, opts *RootOptions) (string, int, *Client, error) {
	repo, err := resolveRepo("", opts)
	if err != nil {
		return "", 0, nil, err
	}
	number, err := resolveNumberArg(arg)
	if err != nil {
		return "", 0, nil, err
	}
	return repo, number, clientFor(opts), nil
}

func printOptionalIssueComments(cmd *cobra.Command, client *Client, repo string, number int, showComments bool) error {
	if !showComments {
		return nil
	}
	comments, err := client.ListIssueComments(context.Background(), repo, number)
	if err != nil {
		return err
	}
	return printIssueCommentsSection(cmd.OutOrStdout(), comments)
}

func newPRReviewsCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "reviews <number>",
		Short: "View pull request reviews",
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
			reviews, err := client.ListPullRequestReviews(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), reviews, jsonFields)
			}
			return printReviews(cmd.OutOrStdout(), reviews)
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newPRCommentsCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "comments <number>",
		Short: "View pull request review comments",
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
			comments, err := client.ListPullRequestComments(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), comments, jsonFields)
			}
			return printReviewComments(cmd.OutOrStdout(), comments)
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}
