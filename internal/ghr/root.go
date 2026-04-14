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

func resolveRepoAndNumber(args []string, opts *RootOptions) (string, int, error) {
	switch len(args) {
	case 1:
		repo, err := resolveRepo("", opts)
		if err != nil {
			return "", 0, err
		}
		number, err := strconv.Atoi(args[0])
		if err != nil || number <= 0 {
			return "", 0, fmt.Errorf("invalid number: %q", args[0])
		}
		return repo, number, nil
	case 2:
		repo, err := resolveRepo(args[0], opts)
		if err != nil {
			return "", 0, err
		}
		number, err := strconv.Atoi(args[1])
		if err != nil || number <= 0 {
			return "", 0, fmt.Errorf("invalid number: %q", args[1])
		}
		return repo, number, nil
	default:
		return "", 0, errors.New("expected [OWNER/REPO] NUMBER")
	}
}

func newRepoCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "View mirrored repository data",
	}
	cmd.AddCommand(newRepoViewCmd(opts))
	return cmd
}

func newRepoViewCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
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
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), resp, jsonFields)
			}
			printRepoView(cmd.OutOrStdout(), resp)
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
		Use:   "list [OWNER/REPO]",
		Short: "List issues in a repository",
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
	cmd := &cobra.Command{
		Use:   "view [OWNER/REPO] NUMBER",
		Short: "View an issue",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, number, err := resolveRepoAndNumber(args, opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			issue, err := client.GetIssue(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), issue, jsonFields)
			}
			printIssueView(cmd.OutOrStdout(), repo, issue)
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newIssueCommentsCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "comments [OWNER/REPO] NUMBER",
		Short: "View issue comments",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, number, err := resolveRepoAndNumber(args, opts)
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
		Use:   "list [OWNER/REPO]",
		Short: "List pull requests in a repository",
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
	cmd := &cobra.Command{
		Use:   "view [OWNER/REPO] NUMBER",
		Short: "View a pull request",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, number, err := resolveRepoAndNumber(args, opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			pr, err := client.GetPullRequest(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), pr, jsonFields)
			}
			printPullView(cmd.OutOrStdout(), repo, pr)
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newPRReviewsCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "reviews [OWNER/REPO] NUMBER",
		Short: "View pull request reviews",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, number, err := resolveRepoAndNumber(args, opts)
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
		Use:   "comments [OWNER/REPO] NUMBER",
		Short: "View pull request review comments",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, number, err := resolveRepoAndNumber(args, opts)
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
