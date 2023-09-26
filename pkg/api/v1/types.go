package v1

type PullRequest struct {
	Owner      string
	Repository string
	Number     int
	Title      string
	MergedSHA  string
	BaseBranch string
	Author     string
}
