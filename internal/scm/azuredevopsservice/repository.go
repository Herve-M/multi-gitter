package azuredevopsservice

import (
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
)

type repository struct {
	id            string
	url           string
	name          string
	projectName   string
	projectId     string
	defaultBranch string
}

func (r repository) CloneURL() string {
	return r.url
}

func (r repository) DefaultBranch() string {
	return r.defaultBranch
}

func (r repository) FullName() string {
	return fmt.Sprintf("%s/%s", r.projectName, r.name)
}

func (a *AzureDevOpsService) convertRepository(nativeRepository *git.GitRepository) (repository, error) {

	var cloneURL string
	if a.Config.SSHAuth {
		cloneURL = *nativeRepository.SshUrl
	} else {
		cloneURL = *nativeRepository.RemoteUrl
	}

	return repository{
		id:            (*nativeRepository.Id).String(),
		projectId:     (*nativeRepository.Project.Id).String(),
		url:           cloneURL,
		name:          *nativeRepository.Name,
		projectName:   *nativeRepository.Project.Name,
		defaultBranch: *nativeRepository.DefaultBranch,
	}, nil
}
