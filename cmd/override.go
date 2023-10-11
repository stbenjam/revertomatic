package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/revertomatic/pkg/github"
)

var overrideOpts struct {
	prURI string
}

func NewOverrideCommand() *cobra.Command {
	overrideCmd.Flags().StringVarP(&overrideOpts.prURI, "pr-url", "p", "", "Pull request URL")
	return overrideCmd
}

var overrideCmd = &cobra.Command{
	Use:   "override",
	Short: "Show overrides for PR",
	RunE: func(cmd *cobra.Command, args []string) error {
		if overrideOpts.prURI == "" {
			cmd.Usage() //nolint
			return fmt.Errorf("no pr url specified")
		}

		client, err := github.New(context.Background())
		if err != nil {
			cmd.Usage() //nolint
			return err
		}

		pr, err := client.ExtractPRInfo(overrideOpts.prURI)
		if err != nil {
			return err
		}

		fmt.Println("******** You can use the comment below to override CI:")
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
