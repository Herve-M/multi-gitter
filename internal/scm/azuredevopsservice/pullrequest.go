package azuredevopsservice

import (
	"fmt"

	"github.com/lindell/multi-gitter/internal/scm"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
)

type pullRequest struct {
	projectName    string
	repositoryName string
	id             int
	status         scm.PullRequestStatus
	isDraft        bool
	webURL         string
}

func (pr pullRequest) String() string {
	return fmt.Sprintf("%s/%s #%d", pr.projectName, pr.repositoryName, pr.id)
}

func (pr pullRequest) Status() scm.PullRequestStatus {
	return pr.status
}

func (pr pullRequest) URL() string {
	return pr.webURL
}

func (a *AzureDevOpsService) convertPullRequest(nativePullRequest *git.GitPullRequest) scm.PullRequest {
	return pullRequest{
		projectName:    *nativePullRequest.Repository.Project.Name,
		repositoryName: *nativePullRequest.Repository.Name,
		id:             *nativePullRequest.PullRequestId,
		status:         convertPullRequestStatus(nativePullRequest.Status),
		isDraft:        *nativePullRequest.IsDraft,
		webURL:         *nativePullRequest.RemoteUrl, //TODO: find how to get it
	}
}

func convertPullRequestStatus(prStatus *git.PullRequestStatus) scm.PullRequestStatus {
	switch {
	case prStatus == &git.PullRequestStatusValues.NotSet:
		return scm.PullRequestStatusUnknown
	case prStatus == &git.PullRequestStatusValues.Active:
		return scm.PullRequestStatusPending
	case prStatus == &git.PullRequestStatusValues.Abandoned:
		return scm.PullRequestStatusClosed
	case prStatus == &git.PullRequestStatusValues.Completed:
		return scm.PullRequestStatusClosed
	case prStatus == &git.PullRequestStatusValues.All:
		return scm.PullRequestStatusUnknown
	}
	return scm.PullRequestStatusUnknown
}
