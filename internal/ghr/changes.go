package ghr

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
)

func newChangesCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changes",
		Short: "Read normalized git change data from ghreplica",
	}
	cmd.AddCommand(newChangesPRCmd(opts))
	cmd.AddCommand(newChangesCommitCmd(opts))
	cmd.AddCommand(newChangesCompareCmd(opts))
	return cmd
}

func newChangesPRCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Read indexed pull request change data",
	}
	cmd.AddCommand(newChangesPRViewCmd(opts))
	cmd.AddCommand(newChangesPRFilesCmd(opts))
	return cmd
}

func newChangesPRViewCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "view <number>",
		Short: "View indexed change metadata for a pull request",
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
			snapshot, err := client.GetPullRequestChangeSnapshot(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), snapshot, jsonFields)
			}
			printPullRequestChangeSnapshot(cmd.OutOrStdout(), repo, snapshot)
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newChangesPRFilesCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "files <number>",
		Short: "List indexed changed files for a pull request",
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
			files, err := client.ListPullRequestChangeFiles(context.Background(), repo, number)
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), files, jsonFields)
			}
			printFileChanges(cmd.OutOrStdout(), files)
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newChangesCommitCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Read indexed commit change data",
	}
	cmd.AddCommand(newChangesCommitViewCmd(opts))
	cmd.AddCommand(newChangesCommitFilesCmd(opts))
	return cmd
}

func newChangesCommitViewCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "view <sha>",
		Short: "View indexed commit metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			commit, err := client.GetCommit(context.Background(), repo, strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), commit, jsonFields)
			}
			printCommitView(cmd.OutOrStdout(), repo, commit)
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newChangesCommitFilesCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "files <sha>",
		Short: "List indexed changed files for a commit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			files, err := client.ListCommitFiles(context.Background(), repo, strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), files, jsonFields)
			}
			printCommitFiles(cmd.OutOrStdout(), files)
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}

func newChangesCompareCmd(opts *RootOptions) *cobra.Command {
	var jsonFields string
	cmd := &cobra.Command{
		Use:   "compare <base>...<head>",
		Short: "View indexed compare data for a known head/base pair",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo("", opts)
			if err != nil {
				return err
			}
			client := clientFor(opts)
			compare, err := client.CompareChanges(context.Background(), repo, strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			if strings.TrimSpace(jsonFields) != "" {
				return writeJSON(cmd.OutOrStdout(), compare, jsonFields)
			}
			printCompare(cmd.OutOrStdout(), repo, compare)
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonFields, "json", "", "Output JSON with the specified fields")
	return cmd
}
