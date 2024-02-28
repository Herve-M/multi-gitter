package azuredevopsservice

import (
	"fmt"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
)

type repository struct {
	project
	id               string
	url              string
	name             string
	defaultBranch    string
	defaultBranchRef string
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
	if nativeRepository.DefaultBranch == nil {
		return repository{}, fmt.Errorf("repository %s/%s is not initialized", *nativeRepository.Project.Name, *nativeRepository.Name)
	}

	var cloneURL string
	if a.Config.SSHAuth {
		cloneURL = *nativeRepository.SshUrl
	} else {
		cloneURL = *nativeRepository.RemoteUrl
	}

	return repository{
		project: project{
			projectId:   nativeRepository.Project.Id.String(),
			projectName: *nativeRepository.Project.Name,
		},
		id:               nativeRepository.Id.String(),
		url:              cloneURL,
		name:             *nativeRepository.Name,
		defaultBranch:    strings.Replace(*nativeRepository.DefaultBranch, "refs/heads/", "", 1),
		defaultBranchRef: *nativeRepository.DefaultBranch,
	}, nil
}
