package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/revertomatic/pkg/github"
)

var opts struct {
	prURI    string
	override bool
}

func NewCommand() *cobra.Command {
	cmd.Flags().StringVarP(&opts.prURI, "pr-url", "p", "", "Pull request URL")
	cmd.Flags().BoolVarP(&opts.override, "override", "o", false, "Override all required CI (force PR merge)")
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

		client, err := github.New(context.Background())
		if err != nil {
			cmd.Usage()
			return err
		}

		pr, err := client.ExtractPRInfo(opts.prURI)
		if err != nil {
			return err
		}

		if opts.override {
			statuses, err := client.GetOverridableStatuses(pr)
			if err != nil {
				return err
			}

			for _, status := range statuses {
				fmt.Printf("/override %s\n", status)
			}
		}
		return nil
	},
}
