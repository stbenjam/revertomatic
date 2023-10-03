package github

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/google/go-github/github"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"k8s.io/apimachinery/pkg/util/sets"

	v1 "github.com/openshift-eng/revertomatic/pkg/api/v1"
)

const revertTemplate = `
Reverts #{{.OriginalPR}} ; tracked by {{.JiraIssue}}

Per [OpenShift policy](https://github.com/openshift/enhancements/blob/master/enhancements/release/improving-ci-signal.md#quick-revert), we are reverting this breaking change to get CI and/or nightly payloads flowing again.

{{.Context}}

To unrevert this, revert this PR, and layer an additional separate commit on top that addresses the problem. Before merging the unrevert, please run these jobs on the PR and check the result of these jobs to confirm the fix has corrected the problem:

` + "```" + `
{{.Jobs}}
` + "```" + `

CC: @{{.OriginalAuthor}}
`

// unoveridableJobs are the jobs we typically don't want to override: typically fast running and the bare minimum to
// make sure things build.
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

func (c *Client) ExtractPRInfo(githubURL string) (*v1.PullRequest, error) {
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

	pr := &v1.PullRequest{
		Owner:      segments[0],
		Repository: segments[1],
		Number:     fmtNum,
	}

	// Get the PR
	prGH, _, err := c.client.PullRequests.Get(c.ctx, pr.Owner, pr.Repository, pr.Number)
	if err != nil {
		logrus.WithError(err).WithFields(map[string]interface{}{
			"owner": pr.Owner,
			"repo":  pr.Repository,
			"prNum": pr.Number,
		}).Warn("failed to get PR")
		return nil, err
	}
	if prGH.MergeCommitSHA != nil {
		pr.MergedSHA = *prGH.MergeCommitSHA
	}
	if prGH.Base != nil {
		pr.BaseBranch = *prGH.Base.Ref
	}
	if prGH.Title != nil {
		pr.Title = *prGH.Title
	}
	if prGH.User != nil {
		pr.Author = *prGH.User.Login
	}

	logrus.WithFields(map[string]interface{}{
		"owner":     segments[0],
		"repo":      segments[1],
		"pr_number": fmtNum,
	}).Infof("found info for pr %s", githubURL)

	return pr, nil
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

func (c *Client) Revert(prInfo *v1.PullRequest, jira, context, jobs string, repoOpts *v1.RepositoryOptions) error {
	// Fetch user details
	user, _, err := c.client.Users.Get(c.ctx, "")
	if err != nil {
		return err
	}

	// If we don't have a local copy we'll clone it and make sure the user has a fork
	if repoOpts == nil {
		// Find a user's fork
		// Note, this won't work if the repository was renamed.  Is there a better way to find
		// the user's fork?
		repo, _, err := c.client.Repositories.Get(c.ctx, *user.Login, prInfo.Repository)
		if err != nil && repo == nil {
			logrus.Infof("fork of %q not found for user %q, creating one...", prInfo.Repository, *user.Login)
			// If not, create a fork
			repo, _, err = c.client.Repositories.CreateFork(c.ctx, prInfo.Owner, prInfo.Repository, nil)
			if err != nil {
				return err
			}
		} else {
			logrus.Infof("fork of %q already exists for user %q", prInfo.Repository, *user.Login)
		}

		// Clone repository
		tempDir, err := c.cloneRepository(prInfo, user)
		if err != nil {
			return err
		}
		defer func() {
			_ = os.RemoveAll(tempDir) // clean up after using
		}()

		repoOpts = &v1.RepositoryOptions{
			LocalPath:      tempDir,
			UpstreamRemote: "upstream",
			ForkRemote:     "fork",
		}
	}

	if err := os.Chdir(repoOpts.LocalPath); err != nil {
		return err
	}

	// Branch
	err = exec.Command("git", "fetch", repoOpts.UpstreamRemote).Run()
	if err != nil {
		return err
	}
	revertBranch := fmt.Sprintf("revert-%d-%d", prInfo.Number, time.Now().UnixMilli())
	logrus.Infof("creating revert branch %s", revertBranch)
	err = exec.Command("git", "checkout", "-b", revertBranch, fmt.Sprintf("%s/%s", repoOpts.UpstreamRemote, prInfo.BaseBranch)).Run()
	if err != nil {
		return err
	}
	err = exec.Command("git", "revert", "-m1", "--no-edit", prInfo.MergedSHA).Run()
	if err != nil {
		return err
	}
	err = exec.Command("git", "push", repoOpts.ForkRemote, revertBranch).Run()
	if err != nil {
		return err
	}

	// Revert template
	tmpl, err := template.New("revertTemplate").Parse(revertTemplate)
	if err != nil {
		return err
	}

	// Revert template
	data := struct {
		OriginalPR     int
		OriginalAuthor string
		JiraIssue      string
		Context        string
		Jobs           string
	}{
		OriginalAuthor: prInfo.Author,
		JiraIssue:      jira,
		Context:        context,
		Jobs:           jobs,
		OriginalPR:     prInfo.Number,
	}

	var renderedMsg bytes.Buffer
	if err := tmpl.Execute(&renderedMsg, data); err != nil {
		return err
	}

	// Pull request details
	newPR := &github.NewPullRequest{
		Title:               github.String(fmt.Sprintf("Revert #%d %q", prInfo.Number, prInfo.Title)),
		Head:                github.String(fmt.Sprintf("%s:%s", *user.Login, revertBranch)),
		Base:                github.String(prInfo.BaseBranch), // branch you want to merge into
		Body:                github.String(renderedMsg.String()),
		MaintainerCanModify: github.Bool(true),
	}

	pr, _, err := c.client.PullRequests.Create(c.ctx, prInfo.Owner, prInfo.Repository, newPR)
	if err != nil {
		logrus.WithError(err).Warn("PullRequests.Create returned error")
		return err
	}

	logrus.Infof("pr created %s", pr.GetHTMLURL())

	return nil
}

func (c *Client) cloneRepository(prInfo *v1.PullRequest, user *github.User) (string, error) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "revertomatic_")
	if err != nil {
		return "", err
	}

	// Clone the upstream repository
	logrus.Infof("cloning upstream repository...")
	upstreamURL := fmt.Sprintf("https://github.com/%s/%s.git", prInfo.Owner, prInfo.Repository)
	// shallow clone for slightly faster reverts
	err = exec.Command("git", "clone", "-b", prInfo.BaseBranch, upstreamURL, tempDir).Run()
	if err != nil {
		return "", err
	}

	// Navigate to the cloned repository directory
	os.Chdir(tempDir)

	// Add a remote for the fork
	logrus.Infof("adding personal fork remote")
	forkURL := fmt.Sprintf("git@github.com:%s/%s.git", *user.Login, prInfo.Repository)
	err = exec.Command("git", "remote", "add", "fork", forkURL).Run()
	if err != nil {
		return "", err
	}

	return tempDir, nil
}
