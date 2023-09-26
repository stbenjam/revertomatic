package github

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
	"golang.org/x/oauth2"
	"k8s.io/apimachinery/pkg/util/sets"

	v1 "github.com/openshift-eng/revertomatic/pkg/api/v1"
)

var unoveridableJobs = regexp.MustCompile(`.*(unit|lint|images|verify|tide|verify-deps)$`)

type Client struct {
	ctx    context.Context
	client *github.Client
}

func New(ctx context.Context) (*Client, error) {
	// Get the GITHUB_TOKEN environment variable
	// todo: read from config file
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		return nil, fmt.Errorf("github token required; please set the GITHUB_TOKEN environment variable")
	}

	// If using a personal access token
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)

	return &Client{
		ctx:    ctx,
		client: github.NewClient(tc),
	}, nil
}

func (c *Client) ExtractPRInfo(githubURL string) (pr *v1.PullRequest, err error) {
	u, err := url.Parse(githubURL)
	if err != nil {
		return nil, err
	}

	// Assuming the URL is correct, we split the path into segments
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < 4 || segments[2] != "pull" {
		return nil, fmt.Errorf("invalid GitHub PR URL format")
	}

	fmtNum, err := strconv.Atoi(segments[3])
	if err != nil {
		return nil, err
	}

	logrus.WithFields(map[string]interface{}{
		"owner":     segments[0],
		"repo":      segments[1],
		"pr_number": fmtNum,
	}).Infof("found info for pr %s", githubURL)
	return &v1.PullRequest{
		Owner:      segments[0],
		Repository: segments[1],
		Number:     fmtNum,
	}, nil
}

func (c *Client) GetOverridableStatuses(prInfo *v1.PullRequest) ([]string, error) {
	// Get the PR
	pr, _, err := c.client.PullRequests.Get(c.ctx, prInfo.Owner, prInfo.Repository, prInfo.Number)
	if err != nil {
		logrus.WithError(err).WithFields(map[string]interface{}{
			"owner": prInfo.Owner,
			"repo":  prInfo.Repository,
			"prNum": prInfo.Number,
		}).Warn("failed to get PR")
		return nil, err
	}

	if pr.Head == nil || pr.Head.SHA == nil {
		log.Fatalf("Failed to retrieve SHA of the PR")
	}

	sha := *pr.Head.SHA
	logrus.Infof("Most recent SHA of the PR: %s\n", sha)

	// Get statuses for the SHA
	statuses, _, err := c.client.Repositories.ListStatuses(c.ctx, prInfo.Owner, prInfo.Repository, sha, nil)
	if err != nil {
		logrus.WithError(err).WithFields(map[string]interface{}{
			"owner": prInfo.Owner,
			"repo":  prInfo.Repository,
			"prNum": prInfo.Number,
			"sha":   sha,
		}).Warn("failed to get statuses")
		return nil, err
	}

	uniqueStatuses := sets.New[string]()
	for _, status := range statuses {
		if status != nil && status.Context != nil && !unoveridableJobs.MatchString(*status.Context) {
			uniqueStatuses.Insert(*status.Context)
		}
	}

	return uniqueStatuses.UnsortedList(), nil
}
