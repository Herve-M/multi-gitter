package azuredevopsservice

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/graph"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/identity"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/location"
	log "github.com/sirupsen/logrus"
)

type descriptor struct {
	VSId        *string // Graph descriptor
	IMSId       *string // Legacy identity management service, still used for PR creation (last checked: v7.2)
	DisplayName *string
	Type        *string
}

func (dt descriptor) String() string {
	return fmt.Sprintf("[%v] %d #%d", dt.Type, dt.DisplayName, dt.VSId)
}

func (a *AzureDevOpsService) converToIdentityWithVoteForNewPullRequest(descriptors *[]descriptor, isRequired bool) *[]git.IdentityRefWithVote {
	identities := make([]git.IdentityRefWithVote, len(*descriptors))
	defaultVote := 0
	for i, d := range *descriptors {
		identities[i] = git.IdentityRefWithVote{
			Id:         d.IMSId, // using legacy ID until ADO-API allow new descriptor (last checked: v7.2)
			Vote:       &defaultVote,
			IsRequired: &isRequired,
		}
	}
	return &identities
}

// TODO: add user vs group filtering
func (a *AzureDevOpsService) getIdentities(ctx context.Context, identities []string) (*[]descriptor, error) {
	graphClient, err := newGraphClient(ctx, a)
	if err != nil {
		return nil, err
	}

	identityClient, err := newIdentityClient(ctx, a)
	if err != nil {
		return nil, err
	}

	vsid := []string{}
	descriptors := []descriptor{}
	for _, wantedIdentity := range identities {
		possibleIdentities, err := graphClient.QuerySubjects(ctx, graph.QuerySubjectsArgs{
			SubjectQuery: &graph.GraphSubjectQuery{
				Query: &wantedIdentity,
				SubjectKind: &([]string{
					"User",
					"Group",
				}),
			},
		})
		if err != nil {
			return nil, err
		}

		//TODO: principalName not mapped, find way to make a matching instead of using top1
		if len(*possibleIdentities) >= 1 {
			matchedDescriptor := descriptor{
				VSId:        (*possibleIdentities)[0].Descriptor,
				DisplayName: (*possibleIdentities)[0].DisplayName,
				Type:        (*possibleIdentities)[0].SubjectKind,
			}
			log.Debugf("Matched %v with %v", wantedIdentity, *matchedDescriptor.VSId)
			vsid = append(vsid, *matchedDescriptor.VSId)
			descriptors = append(descriptors, matchedDescriptor)
		} else if len(*possibleIdentities) == 0 {
			log.Warnf("No identity found for %v", wantedIdentity)
		}
	}

	legacyIdentities, err := identityClient.ReadIdentityBatch(ctx, identity.ReadIdentityBatchArgs{
		BatchInfo: &identity.IdentityBatchInfo{
			SubjectDescriptors: &vsid,
			QueryMembership:    &identity.QueryMembershipValues.None,
		},
	})
	if err != nil {
		return nil, err
	}

	for _, descriptor := range descriptors {
		for _, legacyIdentity := range *legacyIdentities {
			if *legacyIdentity.Descriptor == *descriptor.VSId {
				idAsString := legacyIdentity.Id.String()
				descriptor.IMSId = &idAsString
				log.Debugf("Got legacy %v with %v", *legacyIdentity.Descriptor, *descriptor.VSId)
				break
			}
		}
	}

	return &descriptors, nil
}

func (a *AzureDevOpsService) getLegacyIdentities(ctx context.Context, identities []string) (*[]descriptor, error) {
	identityClient, err := newIdentityClient(ctx, a)
	if err != nil {
		return nil, err
	}

	descriptors := []descriptor{}
	searchFilter := "General"
	for _, wantedIdentity := range identities {
		possibleIdentities, err := identityClient.ReadIdentities(ctx, identity.ReadIdentitiesArgs{
			SearchFilter:    &searchFilter,
			FilterValue:     &wantedIdentity,
			QueryMembership: &identity.QueryMembershipValues.None,
		})
		if err != nil {
			return nil, err
		}

		//TODO: principalName not mapped, find way to make a matching instead of using top1
		if len(*possibleIdentities) >= 1 {
			legacyId := (*possibleIdentities)[0].Id.String()
			matchedDescriptor := descriptor{
				VSId:        (*possibleIdentities)[0].SubjectDescriptor,
				IMSId:       &legacyId,
				DisplayName: (*possibleIdentities)[0].ProviderDisplayName,
			}
			log.Debugf("Matched %v with %v", wantedIdentity, *matchedDescriptor.VSId)
			descriptors = append(descriptors, matchedDescriptor)
		} else if len(*possibleIdentities) == 0 {
			log.Warnf("No identity found for %v", wantedIdentity)
		}
	}

	return &descriptors, nil
}

func (a *AzureDevOpsService) getCurrentIdentity(ctx context.Context) (*descriptor, error) {
	locationClient, err := newLocationClient(ctx, a)
	if err != nil {
		return nil, err
	}

	conData, err := locationClient.GetConnectionData(ctx, location.GetConnectionDataArgs{})
	if err != nil {
		return nil, err
	}
	legacyId := conData.AuthenticatedUser.Id.String()

	return &descriptor{
		VSId:        (*conData).AuthenticatedUser.SubjectDescriptor,
		IMSId:       &legacyId,
		DisplayName: (*conData).AuthenticatedUser.ProviderDisplayName,
	}, nil
}
