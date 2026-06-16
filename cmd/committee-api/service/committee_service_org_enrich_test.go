// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	authpkg "github.com/linuxfoundation/lfx-v2-committee-service/pkg/auth"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

func TestEmailDomain(t *testing.T) {
	domain, ok := emailDomain("user@corp.com")
	assert.True(t, ok)
	assert.Equal(t, "corp.com", domain)

	_, ok = emailDomain("invalid")
	assert.False(t, ok)
}

func TestEnrichMemberOrganization_EmailDomainFallback(t *testing.T) {
	svc := &committeeServicesrvc{}
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			Email: "user@corp.com",
		},
	}

	svc.enrichMemberOrganization(context.Background(), member)
	assert.Equal(t, "https://corp.com", member.Organization.Website)
	assert.Equal(t, "corp.com", member.Organization.Name)
}

func TestEnrichMemberOrganization_AuthServiceMetadata(t *testing.T) {
	principal := "user@corp.com"
	authSub := authpkg.MapUsernameToAuthSub(principal)
	reader := &metadataByKeyReader{
		metadata: map[string]*model.UserMetadata{
			authSub: {Organization: "Example Corp"},
		},
	}
	svc := &committeeServicesrvc{userReader: reader}
	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, principal)
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			Email: "user@corp.com",
		},
	}

	svc.enrichMemberOrganization(ctx, member)
	assert.Equal(t, "Example Corp", member.Organization.Name)
	assert.Equal(t, "https://corp.com", member.Organization.Website)
}

type metadataByKeyReader struct {
	metadata map[string]*model.UserMetadata
}

func (r *metadataByKeyReader) UsernameByEmail(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (r *metadataByKeyReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, nil
}

func (r *metadataByKeyReader) UserMetadataByPrincipal(_ context.Context, key string) (*model.UserMetadata, error) {
	if meta, ok := r.metadata[key]; ok {
		return meta, nil
	}
	return nil, nil
}
