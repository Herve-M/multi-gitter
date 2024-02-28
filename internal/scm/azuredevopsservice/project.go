package azuredevopsservice

import (
	"context"
	"slices"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
)

type project struct {
	projectId   string
	projectName string
}

func (a *AzureDevOpsService) GetProjects(ctx context.Context) ([]project, error) {
	coreClient, err := newCoreClient(ctx, a)
	if err != nil {
		return nil, err
	}

	projects, err := coreClient.GetProjects(ctx, core.GetProjectsArgs{
		StateFilter: &core.ProjectStateValues.WellFormed,
	})
	if err != nil {
		return nil, err
	}

	var result []project
	for _, p := range projects.Value {
		if slices.Index(a.Projects, *p.Name) != -1 {
			result = append(result, project{
				projectId:   p.Id.String(),
				projectName: *p.Name,
			})
		}
	}
	return result, nil
}
