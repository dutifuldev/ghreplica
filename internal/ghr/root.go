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
	cmd.AddCommand(newIssueCmd(opts))
	cmd.AddCommand(newPRCmd(opts))

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
			printRepoView(cmd.OutOrStdout(), resp)
			return nil
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
			status, err := client.GetMirrorStatus(context.Background(), repo)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), status, jsonFields)
			}
			printRepoStatus(cmd.OutOrStdout(), status)
			return nil
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
			printIssueList(cmd.OutOrStdout(), issues)
			return nil
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
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			number, err := resolveNumberArg(args[0])
			if err != nil {
				return err
			}
			client := clientFor(opts)
			issue, err := client.GetIssue(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if openInBrowser {
				return openURL(issue.HTMLURL)
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), issue, jsonFields)
			}
			printIssueView(cmd.OutOrStdout(), repo, issue)
			if showComments {
				comments, err := client.ListIssueComments(context.Background(), repo, number)
				if err != nil {
					return err
				}
				printIssueCommentsSection(cmd.OutOrStdout(), comments)
			}
			return nil
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
			printIssueComments(cmd.OutOrStdout(), comments)
			return nil
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
			printPullList(cmd.OutOrStdout(), pulls)
			return nil
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
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			number, err := resolveNumberArg(args[0])
			if err != nil {
				return err
			}
			client := clientFor(opts)
			pr, err := client.GetPullRequest(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if openInBrowser {
				return openURL(pr.HTMLURL)
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), pr, jsonFields)
			}
			printPullView(cmd.OutOrStdout(), repo, pr)
			if showComments {
				comments, err := client.ListIssueComments(context.Background(), repo, number)
				if err != nil {
					return err
				}
				printIssueCommentsSection(cmd.OutOrStdout(), comments)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	cmd.Flags().BoolVarP(&showComments, "comments", "c", false, "View pull request comments")
	cmd.Flags().BoolVarP(&openInBrowser, "web", "w", false, "Open a pull request in the browser")
	return cmd
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
			printReviews(cmd.OutOrStdout(), reviews)
			return nil
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
			printReviewComments(cmd.OutOrStdout(), comments)
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}
