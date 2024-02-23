package azuredevopsservice

import (
	"fmt"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/stretchr/testify/assert"
)

func ptr(s string) *string { return &s }

func TestADO_convertRepository(t *testing.T) {
	const (
		sshURL         = "git@ssh.dev.azure.com:v3/my-org/my-project/my-repo"
		httpRemoteURL  = "https://user@dev.azure.com/my-org/my-project/_git/my-repo"
		projectName    = "my-project"
		repositoryName = "my-repo"
		defaultBranch  = "refs/heads/main"
	)

	fakeRepository := &git.GitRepository{
		SshUrl:        ptr(sshURL),
		RemoteUrl:     ptr(httpRemoteURL),
		Name:          ptr(repositoryName),
		Project:       &core.TeamProjectReference{Name: ptr(projectName)},
		DefaultBranch: ptr(defaultBranch),
	}

	testCases := []struct {
		ado      *AzureDevOpsService
		given    *git.GitRepository
		expected repository
	}{
		{
			ado: &AzureDevOpsService{
				Config: Config{
					SSHAuth: false,
				},
			},
			given: fakeRepository,
			expected: repository{
				url:           httpRemoteURL,
				name:          repositoryName,
				projectName:   projectName,
				defaultBranch: defaultBranch,
			},
		},
		{
			ado: &AzureDevOpsService{
				Config: Config{
					SSHAuth: true,
				},
			},
			given: fakeRepository,
			expected: repository{
				url:           sshURL,
				name:          repositoryName,
				projectName:   projectName,
				defaultBranch: defaultBranch,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("convertRepository with SSHAuth: %t", tc.ado.Config.SSHAuth), func(t *testing.T) {
			actual, err := tc.ado.convertRepository(tc.given)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected.name, actual.name)
			assert.Equal(t, tc.expected.projectName, actual.projectName)
			assert.Equal(t, tc.expected.defaultBranch, actual.defaultBranch)
			assert.Equal(t, tc.expected.url, actual.url)
		})
	}
}
