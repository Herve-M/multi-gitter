package azuredevopsservice

import (
	"context"
	"net/http"

	"github.com/lindell/multi-gitter/internal/scm"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"

	log "github.com/sirupsen/logrus"
)

// AzureDevOpsService is a service for interacting with Azure DevOps Service (cloud)
type AzureDevOpsService struct {
	RepositoryListing
	Config Config

	connection *azuredevops.Connection
	httpClient *http.Client
}

type Config struct {
	PatToken string
	SSHAuth  bool // Use SSH for cloning
}

type RepositoryListing struct {
	Projects     []string
	Repositories []RepositoryReference
	SkipForks    bool
	SkipDisabled bool
}

type RepositoryReference struct {
	ProjectKey string
	Name       string
}

func New(token, baseUrl string, config Config, filter RepositoryListing) (*AzureDevOpsService, error) {
	connection := azuredevops.NewPatConnection(baseUrl, config.PatToken)

	return &AzureDevOpsService{
		Config:            config,
		connection:        connection,
		RepositoryListing: filter,
	}, nil
}

func newCoreClient(ctx context.Context, ados *AzureDevOpsService) (core.Client, error) {
	return core.NewClient(ctx, ados.connection)
}

// see: https://learn.microsoft.com/en-us/rest/api/azure/devops/git/?view=azure-devops-rest-7.2
func newGitClient(ctx context.Context, ados *AzureDevOpsService) (git.Client, error) {
	return git.NewClient(ctx, ados.connection)
}

func (g *AzureDevOpsService) GetRepositories(ctx context.Context) ([]scm.Repository, error) {
	coreClient, err := newCoreClient(ctx, g)
	if err != nil {
		return nil, err
	}

	allProjectsUnderUser, err := coreClient.GetProjects(ctx, core.GetProjectsArgs{StateFilter: &core.ProjectStateValues.WellFormed})
	if err != nil {
		return nil, err
	}

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return nil, err
	}

	repositories := make([]scm.Repository, 0, len(allProjectsUnderUser.Value))
	for _, project := range allProjectsUnderUser.Value {
		log := log.WithField("project", project.Name)
		if project.State != &core.ProjectStateValues.WellFormed {
			log.Debug("Skipping project since it's not usable.")
			continue
		}

		projectScopedRepositories, err := gitClient.GetRepositories(ctx, git.GetRepositoriesArgs{Project: project.Name})
		if err != nil {
			return nil, err
		}

		for _, adoRepository := range *projectScopedRepositories {
			if *adoRepository.IsDisabled {
				log.Debug("Skipping repository since it's disabled")
				continue
			}

			if g.SkipForks && *adoRepository.IsFork {
				log.Debug("Skipping repository since it's a fork")
				continue
			}

			repository, err := g.convertRepository(&adoRepository)
			if err != nil {
				return nil, err
			}

			repositories = append(repositories, repository)
		}
	}

	return repositories, nil
}

func (g *AzureDevOpsService) GetPullRequests(ctx context.Context, branchName string) ([]scm.PullRequest, error) {
	coreClient, err := newCoreClient(ctx, g)
	if err != nil {
		return nil, err
	}

	allProjectsUnderUser, err := coreClient.GetProjects(ctx, core.GetProjectsArgs{StateFilter: &core.ProjectStateValues.WellFormed})
	if err != nil {
		return nil, err
	}

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return nil, err
	}

	prs := []scm.PullRequest{}
	for _, project := range allProjectsUnderUser.Value {
		log := log.WithField("project", project.Name)
		if err != nil {
			return nil, err
		}
		if project.State != &core.ProjectStateValues.WellFormed {
			log.Debug("Skipping project since it's not usable.")
			continue
		}

		scopedPrs, scopedErr := gitClient.GetPullRequestsByProject(ctx, git.GetPullRequestsByProjectArgs{
			Project: project.Name,
			SearchCriteria: &git.GitPullRequestSearchCriteria{
				SourceRefName: &branchName,
			},
		})
		if scopedErr != nil {
			return nil, err
		}

		for _, pr := range *scopedPrs {
			prs = append(prs, g.convertPullRequest(&pr))
		}
	}

	return prs, nil
}

func (g *AzureDevOpsService) GetOpenPullRequest(ctx context.Context, repo scm.Repository, branchName string) (scm.PullRequest, error) {
	project := repo.(repository)

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return nil, err
	}

	topLimit := 1
	prs, err := gitClient.GetPullRequests(ctx, git.GetPullRequestsArgs{
		Project:      &project.projectName,
		RepositoryId: &project.id,
		Top:          &topLimit,
		SearchCriteria: &git.GitPullRequestSearchCriteria{
			Status:        &git.PullRequestStatusValues.Active,
			SourceRefName: &branchName, //TODO: check if has heads/refs/ prefix
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

func (g *AzureDevOpsService) CreatePullRequest(ctx context.Context, repo scm.Repository, prRepo scm.Repository, newPR scm.NewPullRequest) (scm.PullRequest, error) {
	return nil, nil
}

func (g *AzureDevOpsService) UpdatePullRequest(ctx context.Context, repo scm.Repository, pullReq scm.PullRequest, updatedPR scm.NewPullRequest) (scm.PullRequest, error) {
	return nil, nil
}

func (g *AzureDevOpsService) MergePullRequest(ctx context.Context, pr scm.PullRequest) error {
	return nil
}

func (g *AzureDevOpsService) ClosePullRequest(ctx context.Context, pr scm.PullRequest) error {
	adoPr := pr.(pullRequest)

	gitClient, err := newGitClient(ctx, g)
	if err != nil {
		return err
	}

	//TODO integration tests
	err = gitClient.UpdatePullRequestStatuses(ctx, git.UpdatePullRequestStatusesArgs{
		Project:       &adoPr.projectName,
		RepositoryId:  &adoPr.repositoryName,
		PullRequestId: &adoPr.id,
	})

	return err
}

func (g *AzureDevOpsService) ForkRepository(ctx context.Context, repo scm.Repository, newOwner string) (scm.Repository, error) {
	return nil, nil
}
