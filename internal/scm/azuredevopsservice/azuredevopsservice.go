package azuredevopsservice

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/lindell/multi-gitter/internal/scm"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/graph"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/identity"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/location"
	"golang.org/x/exp/maps"

	internalHTTP "github.com/lindell/multi-gitter/internal/http"
	log "github.com/sirupsen/logrus"
)

// AzureDevOpsService is a service for interacting with Azure DevOps Service (cloud)
type AzureDevOpsService struct {
	RepositoryListing
	Config Config
	Cache  Cache

	connection *azuredevops.Connection
	client     *azuredevops.Client
}

type Config struct {
	PatToken string
	SSHAuth  bool // Use SSH for cloning
}

type RepositoryListing struct {
	Projects     []string
	Repositories map[string][]string
	SkipForks    bool
}

type Cache struct {
	Author        *descriptor
	Reviewers     *[]git.IdentityRefWithVote
	TeamReviewers *[]git.IdentityRefWithVote
	Assignees     *[]git.IdentityRefWithVote
	prefetch      sync.Once
}

func ParseRepositoryReference(projectToFetch []string, repositoryToFetch []string) ([]string, map[string][]string, error) {
	repositories := make(map[string][]string)

	for _, repository := range repositoryToFetch {
		split := strings.Split(repository, "/")
		if len(split) != 2 {
			return nil, nil, fmt.Errorf("could not parse repository reference: %s", repository)
		}

		projectName := split[0]
		repositoryName := split[1]

		if _, exist := repositories[projectName]; exist {
			repositories[projectName] = append(repositories[projectName], repositoryName)
		} else {
			repositories[projectName] = []string{repositoryName}
		}
	}

	for _, project := range projectToFetch {
		if _, exist := repositories[project]; !exist {
			repositories[project] = []string{}
		}
	}

	log.Debugf("Parsed %+v", repositories)

	return maps.Keys(repositories), repositories, nil
}

func New(token, baseUrl string, config Config, filter RepositoryListing) (*AzureDevOpsService, error) {
	var options []azuredevops.ClientOptionFunc
	options = append(options, azuredevops.WithHTTPClient(
		&http.Client{
			Transport: internalHTTP.LoggingRoundTripper{},
		},
	))

	connection := azuredevops.NewPatConnection(baseUrl, config.PatToken)
	client := azuredevops.NewClientWithOptions(connection, baseUrl, options...)

	return &AzureDevOpsService{
		Config:            config,
		connection:        connection,
		RepositoryListing: filter,
		client:            client,
		Cache:             Cache{},
	}, nil
}

// https://learn.microsoft.com/en-us/rest/api/azure/devops/core/
func newCoreClient(ctx context.Context, ados *AzureDevOpsService) (core.Client, error) {
	return core.NewClient(ctx, ados.connection)
}

// see: https://learn.microsoft.com/en-us/rest/api/azure/devops/git/
func newGitClient(ctx context.Context, ados *AzureDevOpsService) (git.Client, error) {
	return git.NewClient(ctx, ados.connection)
}

// see https://learn.microsoft.com/en-us/rest/api/azure/devops/graph/
func newGraphClient(ctx context.Context, ados *AzureDevOpsService) (graph.Client, error) {
	return graph.NewClient(ctx, ados.connection)
}

// see https://learn.microsoft.com/en-us/rest/api/azure/devops/ims/
func newIdentityClient(ctx context.Context, ados *AzureDevOpsService) (identity.Client, error) {
	return identity.NewClient(ctx, ados.connection)
}

// undocumented API
func newLocationClient(ctx context.Context, ados *AzureDevOpsService) (location.Client, error) {
	return location.NewClient(ctx, ados.connection), nil
}

func (g *AzureDevOpsService) PrefetchData(ctx context.Context, forPR scm.NewPullRequest) error {
	// Fetch current logged user identity
	author, err := g.getCurrentIdentity(ctx)
	if err != nil {
		return err
	}
	g.Cache.Author = author

	// Fetch all identities for all reviewers
	userReviewers, err := g.getLegacyIdentities(ctx, forPR.Reviewers)
	if err != nil {
		return err
	}
	g.Cache.Reviewers = g.converToIdentityWithVoteForNewPullRequest(userReviewers, false)

	teamReviewers, err := g.getLegacyIdentities(ctx, forPR.TeamReviewers)
	if err != nil {
		return err
	}
	g.Cache.TeamReviewers = g.converToIdentityWithVoteForNewPullRequest(teamReviewers, false)

	assignees, err := g.getLegacyIdentities(ctx, forPR.Assignees)
	if err != nil {
		return err
	}
	g.Cache.Assignees = g.converToIdentityWithVoteForNewPullRequest(assignees, true)

	return nil
}

func (g *AzureDevOpsService) GetRepositories(ctx context.Context) ([]scm.Repository, error) {
	allProjectsUnderUser, err := g.GetProjects(ctx)
	if err != nil {
		return nil, err
	}

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return nil, err
	}

	repositories := make([]scm.Repository, 0, len(allProjectsUnderUser))
	for _, project := range allProjectsUnderUser {
		log := log.WithField("project", project.projectName)

		projectScopedRepositories, err := gitClient.GetRepositories(ctx, git.GetRepositoriesArgs{Project: &project.projectId})
		if err != nil {
			return nil, err
		}

		for _, adoRepository := range *projectScopedRepositories {
			if *adoRepository.IsDisabled {
				log.Debug("Skipping repository since it's disabled")
				continue
			}

			if g.SkipForks && adoRepository.IsFork != nil && *adoRepository.IsFork {
				log.Debug("Skipping repository since it's a fork")
				continue
			}

			if repos, exist := g.Repositories[project.projectName]; exist {
				// user seleted a specific repository
				if len(repos) != 0 && slices.Index(repos, *adoRepository.Name) != -1 {
					repository, err := g.convertRepository(&adoRepository)
					if err != nil {
						return nil, err
					}
					repositories = append(repositories, repository)
				} else if len(repos) == 0 { // user seleted a project and wish all repositories under
					repository, err := g.convertRepository(&adoRepository)
					if err != nil {
						return nil, err
					}
					repositories = append(repositories, repository)
				} else {
					continue
				}
			}
		}
	}

	return repositories, nil
}

func (g *AzureDevOpsService) GetPullRequests(ctx context.Context, branchName string) ([]scm.PullRequest, error) {
	allProjectsUnderUser, err := g.GetProjects(ctx)
	if err != nil {
		return nil, err
	}

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return nil, err
	}

	branchRef := fmt.Sprintf("refs/heads/%s", branchName)
	prs := []scm.PullRequest{}
	for _, project := range allProjectsUnderUser {
		scopedLog := log.WithField("project", project.projectName)
		scopedPrs, err := gitClient.GetPullRequestsByProject(ctx, git.GetPullRequestsByProjectArgs{
			Project: &project.projectId,
			SearchCriteria: &git.GitPullRequestSearchCriteria{
				SourceRefName: &branchRef,
			},
		})
		if err != nil {
			return nil, err
		}

		for _, pr := range *scopedPrs {
			scopedLog = scopedLog.WithField("repository", *pr.Repository.Name)
			scopedLog.Debugf("Found PR %d", *pr.PullRequestId)
			prs = append(prs, g.convertPullRequest(&pr))
		}
	}

	return prs, nil
}

func (g *AzureDevOpsService) GetOpenPullRequest(ctx context.Context, repo scm.Repository, branchName string) (scm.PullRequest, error) {
	repository := repo.(repository)

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return nil, err
	}

	topLimit := 1
	branchRef := fmt.Sprintf("refs/heads/%s", branchName)
	prs, err := gitClient.GetPullRequests(ctx, git.GetPullRequestsArgs{
		Project:      &repository.projectId,
		RepositoryId: &repository.id,
		Top:          &topLimit,
		SearchCriteria: &git.GitPullRequestSearchCriteria{
			Status:        &git.PullRequestStatusValues.Active,
			SourceRefName: &branchRef,
		},
	})

	if err != nil {
		return nil, err
	}
	if len(*prs) == 0 {
		return nil, nil
	}

	return g.convertPullRequest(&((*prs)[0])), nil
}

// TODO: Fork management
func (g *AzureDevOpsService) CreatePullRequest(ctx context.Context, repo scm.Repository, prRepo scm.Repository, newPR scm.NewPullRequest) (scm.PullRequest, error) {
	g.Cache.prefetch.Do(func() { g.PrefetchData(ctx, newPR) })
	adoRepo := repo.(repository)

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return nil, err
	}

	reviewers := []git.IdentityRefWithVote{}
	reviewers = append(reviewers, *g.Cache.Reviewers...)
	reviewers = append(reviewers, *g.Cache.TeamReviewers...)
	reviewers = append(reviewers, *g.Cache.Assignees...)

	supportIteration := true //TODO: add settings/cli param.?
	sourceRef := fmt.Sprintf("refs/heads/%s", newPR.Head)
	targetRef := fmt.Sprintf("refs/heads/%s", newPR.Base)
	createdPr, err := gitClient.CreatePullRequest(ctx, git.CreatePullRequestArgs{
		Project:            &adoRepo.projectId,
		RepositoryId:       &adoRepo.id,
		SupportsIterations: &supportIteration,
		GitPullRequestToCreate: &git.GitPullRequest{
			Title:         &newPR.Title,
			Description:   &newPR.Body,
			SourceRefName: &sourceRef,
			TargetRefName: &targetRef,
			IsDraft:       &newPR.Draft,
			Reviewers:     &reviewers,
			Labels:        g.getNewPullRequestLabels(&newPR),
		},
	})
	if err != nil {
		return nil, err
	}

	if !newPR.Draft {
		g.setPullRequestAutoComplete(ctx, createdPr)
	}

	return g.convertPullRequest(createdPr), nil
}

func (g *AzureDevOpsService) UpdatePullRequest(ctx context.Context, repo scm.Repository, pullReq scm.PullRequest, updatedPR scm.NewPullRequest) (scm.PullRequest, error) {
	g.Cache.prefetch.Do(func() { g.PrefetchData(ctx, updatedPR) })
	adoPr := pullReq.(pullRequest)

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return nil, err
	}

	targetRef := fmt.Sprintf("refs/heads/%s", updatedPR.Base)
	var adoUpdatedPr *git.GitPullRequest
	if adoPr.targetGitRef != targetRef { // ADO-API fail if the TargetRef isn't diff.
		adoUpdatedPr, err = gitClient.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{
			Project:       &adoPr.projectName,
			RepositoryId:  &adoPr.repositoryName, //TODO: try to use RepoID
			PullRequestId: &adoPr.id,
			GitPullRequestToUpdate: &git.GitPullRequest{
				Title:         &updatedPR.Title,
				Description:   &updatedPR.Body,
				TargetRefName: &targetRef,
			},
		})
	} else {
		adoUpdatedPr, err = gitClient.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{
			Project:       &adoPr.projectName,
			RepositoryId:  &adoPr.repositoryName, //TODO: try to use RepoID
			PullRequestId: &adoPr.id,
			GitPullRequestToUpdate: &git.GitPullRequest{
				Title:       &updatedPR.Title,
				Description: &updatedPR.Body,
			},
		})
	}

	if err != nil {
		log.Errorf("Failed while updating PR %d, see: %v", adoPr.id, err)
		return nil, err
	}

	if len(updatedPR.Assignees) > 0 || len(updatedPR.TeamReviewers) > 0 || len(updatedPR.Reviewers) > 0 {
		reviewers := []git.IdentityRefWithVote{}
		reviewers = append(reviewers, *g.Cache.Reviewers...)
		reviewers = append(reviewers, *g.Cache.TeamReviewers...)
		reviewers = append(reviewers, *g.Cache.Assignees...)

		err = g.setPullRequestReviewers(ctx, adoUpdatedPr, &reviewers)
		if err != nil {
			log.Errorf("Failed while updating PR %d's reviewers, see: %v", adoPr.id, err)
			return nil, err
		}
	}

	if !updatedPR.Draft {
		g.setPullRequestAutoComplete(ctx, adoUpdatedPr)
	}

	if len(updatedPR.Labels) > 0 {
		g.setPullRequestLabels(ctx, adoUpdatedPr, updatedPR.Labels)
	}

	return g.convertPullRequest(adoUpdatedPr), nil
}

func (g *AzureDevOpsService) MergePullRequest(ctx context.Context, pr scm.PullRequest) error {
	adoPr := pr.(pullRequest)

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return err
	}

	_, err = gitClient.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{
		Project:       &adoPr.projectName,
		RepositoryId:  &adoPr.repositoryName, //TODO: try to use RepoID
		PullRequestId: &adoPr.id,
		GitPullRequestToUpdate: &git.GitPullRequest{
			Status: &git.PullRequestStatusValues.Completed,
			LastMergeSourceCommit: &git.GitCommitRef{
				CommitId: &adoPr.lastMergeSourceCommitId,
			},
		},
	})
	if err != nil {
		return err
	}

	return nil
}

func (g *AzureDevOpsService) ClosePullRequest(ctx context.Context, pr scm.PullRequest) error {
	adoPr := pr.(pullRequest)

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return err
	}

	_, err = gitClient.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{
		Project:       &adoPr.projectName,
		RepositoryId:  &adoPr.repositoryName, //TODO: try to use RepoID
		PullRequestId: &adoPr.id,
		GitPullRequestToUpdate: &git.GitPullRequest{
			Status: &git.PullRequestStatusValues.Abandoned,
		},
	})
	if err != nil {
		return err
	}

	topLimit := 1
	toSearch := strings.Replace(adoPr.sourceGitRef, "refs/", "", 1)
	sourceRefNames, err := gitClient.GetRefs(ctx, git.GetRefsArgs{
		RepositoryId: &adoPr.repositoryName,
		Project:      &adoPr.projectName,
		Filter:       &toSearch,
		Top:          &topLimit,
	})
	if err != nil {
		return err
	}

	if len(sourceRefNames.Value) == 0 {
		log.Errorf("Failed while trying to find branch to delete for PR %d, see: %v", adoPr.id, err)
		return nil
	}

	branchToDelete := sourceRefNames.Value[0].ObjectId
	branchToDeleteTarget := "0000000000000000000000000000000000000000"
	_, err = gitClient.UpdateRefs(ctx, git.UpdateRefsArgs{
		Project:      &adoPr.projectName,
		RepositoryId: &adoPr.repositoryName,
		RefUpdates: &[]git.GitRefUpdate{
			{
				Name:        sourceRefNames.Value[0].Name,
				OldObjectId: branchToDelete,
				NewObjectId: &branchToDeleteTarget,
			},
		},
	})
	if err != nil {
		return err
	}

	return nil
}

func (g *AzureDevOpsService) ForkRepository(ctx context.Context, repo scm.Repository, newOwner string) (scm.Repository, error) {
	return nil, nil
}
