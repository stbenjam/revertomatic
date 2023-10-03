package cmd

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	v1 "github.com/openshift-eng/revertomatic/pkg/api/v1"
	"github.com/openshift-eng/revertomatic/pkg/github"
)

var opts struct {
	prURI          string
	override       bool
	jira           string
	context        string
	verify         string
	localRepo      string
	forkRemote     string
	upstreamRemote string
}

func NewCommand() *cobra.Command {
	cmd.Flags().StringVarP(&opts.prURI, "pr-url", "p", "", "Pull request URL")
	cmd.Flags().StringVarP(&opts.localRepo, "local-repo", "l", "", "Local copy of the repo, already cloned")
	cmd.Flags().StringVarP(&opts.forkRemote, "fork-remote", "r", "origin", "Name of the fork remote")
	cmd.Flags().StringVarP(&opts.upstreamRemote, "upstream-remote", "u", "upstream", "Name of the upstream remote")
	cmd.Flags().StringVarP(&opts.jira, "jira", "j", "", "Jira card tracking the revert")
	cmd.Flags().StringVarP(&opts.context, "context", "c", "", "Supply context explaining the revert")
	cmd.Flags().StringVarP(&opts.verify, "verify", "v", "", "Supply details about how to verify a fix (i.e. jobs to run)")
	return cmd
}

var cmd = &cobra.Command{
	Use:   "revertomatic",
	Short: "CLI tool to revert a PR",
	RunE: func(cmd *cobra.Command, args []string) error {
		if opts.prURI == "" {
			cmd.Usage() //nolint
			return fmt.Errorf("no pr url specified")
		}

		if opts.jira == "" {
			cmd.Usage() //nolint
			return fmt.Errorf("required jira field is missing")
		}
		if opts.context == "" {
			cmd.Usage() //nolint
			return fmt.Errorf("required context field is missing")
		}
		if opts.verify == "" {
			cmd.Usage() //nolint
			return fmt.Errorf("required verify field is missing")
		}

		client, err := github.New(context.Background())
		if err != nil {
			cmd.Usage() //nolint
			return err
		}

		pr, err := client.ExtractPRInfo(opts.prURI)
		if err != nil {
			return err
		}

		var repoOpts *v1.RepositoryOptions
		if opts.localRepo != "" && opts.forkRemote != "" && opts.upstreamRemote != "" {
			logrus.Infof("have local copy repo, will use that...")
			repoOpts = &v1.RepositoryOptions{
				LocalPath:      opts.localRepo,
				UpstreamRemote: opts.upstreamRemote,
				ForkRemote:     opts.forkRemote,
			}
		}

		if err := client.Revert(pr, opts.jira, opts.context, opts.verify, repoOpts); err != nil {
			return err
		}

		fmt.Println("******** After verifying the PR is correct, you can use the comment below to override CI:")
		statuses, err := client.GetOverridableStatuses(pr)
		if err != nil {
			return err
		}

		for _, status := range statuses {
			fmt.Printf("/override %s\n", status)
		}

		return nil
	},
}
