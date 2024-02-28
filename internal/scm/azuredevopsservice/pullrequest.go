package azuredevopsservice

import (
	"context"
	"fmt"

	"github.com/lindell/multi-gitter/internal/scm"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"

	log "github.com/sirupsen/logrus"
)

type pullRequest struct {
	projectName             string
	repositoryName          string
	id                      int
	status                  scm.PullRequestStatus
	isDraft                 bool
	webURL                  string
	sourceGitRef            string
	targetGitRef            string
	lastMergeSourceCommitId string
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
	var webUrl string
	// GetPullRequestsByProject don't return webUrl
	if nativePullRequest.Repository != nil && nativePullRequest.Repository.WebUrl != nil {
		webUrl = fmt.Sprintf("%v/pullrequest/%d", *nativePullRequest.Repository.WebUrl, *nativePullRequest.PullRequestId)
	}

	var lastMergeCommitId string
	// Could be empty for pending merge
	if nativePullRequest.LastMergeSourceCommit != nil {
		lastMergeCommitId = *nativePullRequest.LastMergeSourceCommit.CommitId
	}

	return pullRequest{
		projectName:             *nativePullRequest.Repository.Project.Name,
		repositoryName:          *nativePullRequest.Repository.Name,
		id:                      *nativePullRequest.PullRequestId,
		status:                  convertPullRequestStatus(nativePullRequest.Status, nativePullRequest.MergeStatus),
		isDraft:                 *nativePullRequest.IsDraft,
		webURL:                  webUrl,
		sourceGitRef:            *nativePullRequest.SourceRefName,
		targetGitRef:            *nativePullRequest.TargetRefName,
		lastMergeSourceCommitId: lastMergeCommitId,
	}
}

func convertPullRequestStatus(prStatus *git.PullRequestStatus, mergeStatus *git.PullRequestAsyncStatus) scm.PullRequestStatus {
	switch {
	case *prStatus == git.PullRequestStatusValues.NotSet:
		return scm.PullRequestStatusUnknown
	case *prStatus == git.PullRequestStatusValues.Active:
		switch {
		case *mergeStatus == git.PullRequestAsyncStatusValues.RejectedByPolicy:
		case *mergeStatus == git.PullRequestAsyncStatusValues.Failure:
		case *mergeStatus == git.PullRequestAsyncStatusValues.Conflicts:
			return scm.PullRequestStatusError
		case *mergeStatus == git.PullRequestAsyncStatusValues.Succeeded:
			return scm.PullRequestStatusSuccess
		case *mergeStatus == git.PullRequestAsyncStatusValues.NotSet:
			return scm.PullRequestStatusUnknown
		case *mergeStatus == git.PullRequestAsyncStatusValues.Queued:
			return scm.PullRequestStatusPending
		}
		return scm.PullRequestStatusPending
	case *prStatus == git.PullRequestStatusValues.Abandoned:
		return scm.PullRequestStatusClosed
	case *prStatus == git.PullRequestStatusValues.Completed:
		return scm.PullRequestStatusMerged
	case *prStatus == git.PullRequestStatusValues.All:
		return scm.PullRequestStatusUnknown
	}
	return scm.PullRequestStatusUnknown
}

func (a *AzureDevOpsService) getPullRequestLabels(ctx context.Context, pr *git.GitPullRequest) ([]string, error) {
	gitClient, err := newGitClient(ctx, a)
	if err != nil {
		return nil, err
	}

	tags, err := gitClient.GetPullRequestLabels(ctx, git.GetPullRequestLabelsArgs{
		Project:       pr.Repository.Project.Name,
		RepositoryId:  pr.Repository.Name,
		PullRequestId: pr.PullRequestId,
	})
	if err != nil {
		return []string{}, err
	}

	var labels []string
	if tags != nil {
		labels = make([]string, len(*tags))
		for _, tag := range *tags {
			labels = append(labels, *tag.Name)
		}
		return labels, nil
	}
	return labels, nil
}

func (a *AzureDevOpsService) getNewPullRequestLabels(pr *scm.NewPullRequest) *[]core.WebApiTagDefinition {
	if len(pr.Labels) == 0 {
		return &[]core.WebApiTagDefinition{}
	}

	labels := make([]core.WebApiTagDefinition, len(pr.Labels))
	for i, label := range pr.Labels {
		labels[i] = core.WebApiTagDefinition{
			Name: &label,
		}
	}

	return &labels
}

func (a *AzureDevOpsService) setPullRequestLabels(ctx context.Context, pr *git.GitPullRequest, labels []string) {
	gitClient, err := newGitClient(ctx, a)
	if err != nil {
		log.Error("Failed to create git client")
		return
	}

	currentLabels, err := a.getPullRequestLabels(ctx, pr)
	if err != nil {
		log.Error("Failed to get PR labels")
		return
	}

	// [tagName]ToKeep
	existingLabels := make(map[string]bool)
	for _, label := range currentLabels {
		existingLabels[label] = false
	}

	// Check and create labels if needed
	for _, newLabel := range labels {
		// Label already exist and is needed, keep it
		if _, exist := existingLabels[newLabel]; exist {
			existingLabels[newLabel] = true
		} else { // Label doesn't exist, create it and keep it
			_, err := gitClient.CreatePullRequestLabel(ctx, git.CreatePullRequestLabelArgs{
				Project:       pr.Repository.Project.Name,
				RepositoryId:  pr.Repository.Name,
				PullRequestId: pr.PullRequestId,
				Label: &core.WebApiCreateTagRequestData{
					Name: &newLabel,
				},
			})
			if err != nil {
				log.Warnf("Failed to assign label: %s to PR: %d", newLabel, *pr.PullRequestId)
			}
			existingLabels[newLabel] = true
		}
	}

	log.Debugf("Action over labels: %+v", existingLabels)

	// Delete unwanted labels
	for label, keep := range existingLabels {
		if !keep {
			err := gitClient.DeletePullRequestLabels(ctx, git.DeletePullRequestLabelsArgs{
				Project:       pr.Repository.Project.Name,
				RepositoryId:  pr.Repository.Name,
				PullRequestId: pr.PullRequestId,
				LabelIdOrName: &label,
			})
			if err != nil {
				log.Warnf("Failed to delete label: %s from PR: %d", label, *pr.PullRequestId)
			}
		}
	}
}

func (a *AzureDevOpsService) setPullRequestAutoComplete(ctx context.Context, pr *git.GitPullRequest) (scm.PullRequest, error) {
	gitClient, err := newGitClient(ctx, a)
	if err != nil {
		return nil, err
	}

	deleteBranchAfterMerge := true                                //TODO: add settings/cli param.?
	mergeStrategy := git.GitPullRequestMergeStrategyValues.Squash //TODO: limited for the moment to cmd-merge, should expose?
	transitionWorkItems := true                                   //TODO: add settings/cli param.?
	mergeCommitMessage := fmt.Sprintf("Merged PR %d: %s", *pr.PullRequestId, *pr.Title)

	adoUpdatedPr, err := gitClient.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{
		Project:       pr.Repository.Project.Name,
		RepositoryId:  pr.Repository.Name,
		PullRequestId: pr.PullRequestId,
		GitPullRequestToUpdate: &git.GitPullRequest{
			AutoCompleteSetBy: &webapi.IdentityRef{
				Id: a.Cache.Author.IMSId,
			},
			CompletionOptions: &git.GitPullRequestCompletionOptions{
				DeleteSourceBranch:  &deleteBranchAfterMerge,
				MergeStrategy:       &mergeStrategy,
				TransitionWorkItems: &transitionWorkItems,
				MergeCommitMessage:  &mergeCommitMessage,
			},
		},
	})
	if err != nil {
		log.Warn("Failed to set auto complete")
	}

	return a.convertPullRequest(adoUpdatedPr), nil
}

func (a *AzureDevOpsService) setPullRequestReviewers(ctx context.Context, pr *git.GitPullRequest, reviewers *[]git.IdentityRefWithVote) error {
	gitClient, err := newGitClient(ctx, a)
	if err != nil {
		return err
	}

	existingReviewers, err := gitClient.GetPullRequestReviewers(ctx, git.GetPullRequestReviewersArgs{
		Project:       pr.Repository.Project.Name,
		RepositoryId:  pr.Repository.Name,
		PullRequestId: pr.PullRequestId,
	})
	if err != nil {
		return err
	}

	// [reviewerId]ToKeep
	existingReviewersMap := make(map[string]bool)
	for _, reviewer := range *existingReviewers {
		existingReviewersMap[*reviewer.Id] = false
	}

	// Check and add reviewer if needed
	for _, reviewerToAdd := range *reviewers {
		// Reviewer already exist and is needed, keep it
		if _, exist := existingReviewersMap[*reviewerToAdd.Id]; exist {
			existingReviewersMap[*reviewerToAdd.Id] = true
		} else { // Label doesn't exist, create it and keep it
			_, err = gitClient.CreatePullRequestReviewer(ctx, git.CreatePullRequestReviewerArgs{
				Project:       pr.Repository.Project.Name,
				RepositoryId:  pr.Repository.Name,
				PullRequestId: pr.PullRequestId,
				ReviewerId:    reviewerToAdd.Id,
				Reviewer:      &reviewerToAdd,
			})
			if err != nil {
				log.Warnf("Failed to add reviewer: %s to PR: %d", *reviewerToAdd.DisplayName, *pr.PullRequestId)
			}
			existingReviewersMap[*reviewerToAdd.Id] = true
		}
	}

	log.Debugf("Action over reviewers: %+v", existingReviewersMap)

	// Delete unwanted labels
	for reviewerId, keep := range existingReviewersMap {
		if !keep {
			err := gitClient.DeletePullRequestReviewer(ctx, git.DeletePullRequestReviewerArgs{
				Project:       pr.Repository.Project.Name,
				RepositoryId:  pr.Repository.Name,
				PullRequestId: pr.PullRequestId,
				ReviewerId:    &reviewerId,
			})
			if err != nil {
				log.Warnf("Failed to remove reviewer: %s from PR: %d", reviewerId, *pr.PullRequestId)
			}
		}
	}

	return nil
}
