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
	"k8s.io/apimachinery/pkg/util/wait"

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

<div align="right">
PR created by <a href="https://github.com/stbenjam/revertomatic">Revertomatic<sup>:tm:</sup></a>
</div>
`

// unoveridableJobs are the jobs we typically don't want to override: typically fast running and the bare minimum to
// make sure things build.
var unoveridableJobs = regexp.MustCompile(`.*(unit|lint|images|verify|tide|verify-deps|fmt|vendor|vet)$`)

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

func (c *Client) Revert(prInfo *v1.PullRequest, jira, contextMsg, jobs string, repoOpts *v1.RepositoryOptions) (*v1.PullRequest, error) {
	// Fetch user details
	user, _, err := c.client.Users.Get(c.ctx, "")
	if err != nil {
		return nil, err
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
				// Sometimes the fork is queued and not ready immediately, so wait for it to be ready
				backoff := wait.Backoff{
					Duration: 1 * time.Second,
					Factor:   1.5,
					Jitter:   0.2,
					Steps:    10,
				}
				operation := func(ctx context.Context) (bool, error) {
					logrus.WithError(err).Infof("Fork not ready, waiting to see it becomes available...")
					repo, _, err = c.client.Repositories.Get(c.ctx, *user.Login, prInfo.Repository)
					if err != nil {
						return false, nil
					}

					return true, nil
				}

				if err := wait.ExponentialBackoffWithContext(c.ctx, backoff, operation); err != nil {
					logrus.WithError(err).Warningf("fork failed to become available")
					return nil, err
				}
			}

		} else {
			logrus.Infof("fork of %q already exists for user %q", prInfo.Repository, *user.Login)
		}

		// Clone repository
		forkURL := fmt.Sprintf("git@github.com:%s/%s.git", *user.Login, *repo.Name)
		tempDir, err := c.cloneRepository(prInfo, forkURL)
		if err != nil {
			return nil, err
		}
		defer func() {
			_ = os.RemoveAll(tempDir) // clean up after using
		}()

		repoOpts = &v1.RepositoryOptions{
			LocalPath:      tempDir,
			UpstreamRemote: "origin",
			ForkRemote:     "fork",
		}
	}

	if err := os.Chdir(repoOpts.LocalPath); err != nil {
		return nil, err
	}

	// Branch
	err = execWithOutput("git", "fetch", repoOpts.UpstreamRemote)
	if err != nil {
		return nil, err
	}
	revertBranch := fmt.Sprintf("revert-%d-%d", prInfo.Number, time.Now().UnixMilli())
	logrus.Infof("creating revert branch %s", revertBranch)
	err = execWithOutput("git", "checkout", "-b", revertBranch, fmt.Sprintf("%s/%s", repoOpts.UpstreamRemote, prInfo.BaseBranch))
	if err != nil {
		return nil, err
	}
	err = execWithOutput("git", "revert", "-m1", "--no-edit", prInfo.MergedSHA)
	if err != nil {
		return nil, err
	}
	err = execWithOutput("git", "push", repoOpts.ForkRemote, fmt.Sprintf("%s:%s", revertBranch, revertBranch))
	if err != nil {
		return nil, err
	}

	// Revert template
	tmpl, err := template.New("revertTemplate").Parse(revertTemplate)
	if err != nil {
		return nil, err
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
		Context:        contextMsg,
		Jobs:           jobs,
		OriginalPR:     prInfo.Number,
	}

	var renderedMsg bytes.Buffer
	if err := tmpl.Execute(&renderedMsg, data); err != nil {
		return nil, err
	}

	// Pull request details
	newPR := &github.NewPullRequest{
		Title:               github.String(fmt.Sprintf("%s: Revert #%d %q", jira, prInfo.Number, prInfo.Title)),
		Head:                github.String(fmt.Sprintf("%s:%s", *user.Login, revertBranch)),
		Base:                github.String(prInfo.BaseBranch), // branch you want to merge into
		Body:                github.String(renderedMsg.String()),
		MaintainerCanModify: github.Bool(true),
	}

	pr, _, err := c.client.PullRequests.Create(c.ctx, prInfo.Owner, prInfo.Repository, newPR)
	if err != nil {
		logrus.WithError(err).Warn("PullRequests.Create returned error")
		return nil, err
	}

	logrus.Infof("pr created %s", pr.GetHTMLURL())

	return c.ExtractPRInfo(pr.GetHTMLURL())
}

func (c *Client) cloneRepository(prInfo *v1.PullRequest, forkURL string) (string, error) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "revertomatic_")
	if err != nil {
		return "", err
	}

	// Clone the upstream repository
	logrus.Infof("cloning upstream repository...")
	upstreamURL := fmt.Sprintf("https://github.com/%s/%s.git", prInfo.Owner, prInfo.Repository)
	// shallow clone for slightly faster reverts
	err = execWithOutput("git", "clone", "-b", prInfo.BaseBranch, upstreamURL, tempDir)
	if err != nil {
		return "", err
	}

	// Navigate to the cloned repository directory
	if err := os.Chdir(tempDir); err != nil {
		panic(err) // this really shouldn't happen
	}

	// Add a remote for the fork
	logrus.Infof("adding personal fork remote")
	err = execWithOutput("git", "remote", "add", "fork", forkURL)
	if err != nil {
		return "", err
	}

	return tempDir, nil
}

func execWithOutput(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
