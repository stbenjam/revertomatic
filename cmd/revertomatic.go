package cmd

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/github"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"k8s.io/apimachinery/pkg/util/sets"
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

var unoveridableJobs = regexp.MustCompile(`.*(unit|lint|images|verify|tide|verify-deps)$`)

var cmd = &cobra.Command{
	Use:   "revertomatic",
	Short: "CLI tool to revert a PR",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Get the GITHUB_TOKEN environment variable
		// todo: read from config file
		githubToken := os.Getenv("GITHUB_TOKEN")
		if githubToken == "" {
			cmd.Usage() //nolint
			return fmt.Errorf("github token required; please set the GITHUB_TOKEN environment variable")
		}

		if opts.prURI == "" {
			cmd.Usage() //nolint
			return fmt.Errorf("no pr url specified")
		}
		owner, repo, prNum, err := extractGitHubInfo(opts.prURI)
		if err != nil {
			return err
		}

		ctx := context.Background()

		// If using a personal access token
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: githubToken},
		)
		tc := oauth2.NewClient(ctx, ts)

		client := github.NewClient(tc)

		// Get the PR
		pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNum)
		if err != nil {
			log.Fatalf("Failed to get PR: %v", err)
		}

		if pr.Head == nil || pr.Head.SHA == nil {
			log.Fatalf("Failed to retrieve SHA of the PR")
		}

		sha := *pr.Head.SHA
		logrus.Infof("Most recent SHA of the PR: %s\n", sha)

		if opts.override {
			// Get statuses for the SHA
			statuses, _, err := client.Repositories.ListStatuses(ctx, owner, repo, sha, nil)
			if err != nil {
				log.Fatalf("Failed to get statuses for SHA %s: %v", sha, err)
			}

			uniqueContexts := sets.New[string]()
			for _, status := range statuses {
				if status != nil && status.Context != nil && !unoveridableJobs.MatchString(*status.Context) {
					uniqueContexts.Insert(*status.Context)
				}
			}

			forceMergeComment := ""
			for _, c := range uniqueContexts.UnsortedList() {
				forceMergeComment += fmt.Sprintf("/override %s\n", c)
			}

			// todo automatically open revert PR and comment overrides
			logrus.Infof("comment this to override ci contexts and force your PR")
			fmt.Println(forceMergeComment)
		}

		return nil
	},
}

func extractGitHubInfo(githubURL string) (owner, repo string, prNum int, err error) {
	u, err := url.Parse(githubURL)
	if err != nil {
		return "", "", 0, err
	}

	// Assuming the URL is correct, we split the path into segments
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < 4 || segments[2] != "pull" {
		return "", "", 0, fmt.Errorf("invalid GitHub PR URL format")
	}

	fmtNum, err := strconv.Atoi(segments[3])
	if err != nil {
		return "", "", 0, err
	}

	logrus.WithFields(map[string]interface{}{
		"owner":     segments[0],
		"repo":      segments[1],
		"pr_number": fmtNum,
	}).Infof("found info for pr %s", githubURL)
	return segments[0], segments[1], fmtNum, nil
}
