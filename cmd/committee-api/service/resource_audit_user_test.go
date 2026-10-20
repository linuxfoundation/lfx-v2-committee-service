// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"testing"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	server "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditReadUserReader struct {
	*mockUserReader
	emailCalls int
}

func (r *auditReadUserReader) EmailsByAuthToken(ctx context.Context, token string) (*model.UserEmails, error) {
	r.emailCalls++
	return r.mockUserReader.EmailsByAuthToken(ctx, token)
}

func TestCommitteeService_PublicResourceAuditUsers(t *testing.T) {
	uid, resourceUID := "committee-1", "resource-1"
	endpoints := []struct {
		name string
		call func(context.Context, *committeeServicesrvc) (any, error)
	}{
		{"get link", func(ctx context.Context, s *committeeServicesrvc) (any, error) {
			res, err := s.GetCommitteeLink(ctx, &committeeservice.GetCommitteeLinkPayload{UID: &uid, LinkUID: &resourceUID})
			if err != nil {
				return nil, err
			}
			return server.NewGetCommitteeLinkResponseBody(res), nil
		}},
		{"list links", func(ctx context.Context, s *committeeServicesrvc) (any, error) {
			res, err := s.ListCommitteeLinks(ctx, &committeeservice.ListCommitteeLinksPayload{UID: &uid})
			if err != nil {
				return nil, err
			}
			return server.NewListCommitteeLinksResponseBody(res), nil
		}},
		{"get folder", func(ctx context.Context, s *committeeServicesrvc) (any, error) {
			res, err := s.GetCommitteeLinkFolder(ctx, &committeeservice.GetCommitteeLinkFolderPayload{UID: &uid, FolderUID: &resourceUID})
			if err != nil {
				return nil, err
			}
			return server.NewGetCommitteeLinkFolderResponseBody(res), nil
		}},
		{"list folders", func(ctx context.Context, s *committeeServicesrvc) (any, error) {
			res, err := s.ListCommitteeLinkFolders(ctx, &committeeservice.ListCommitteeLinkFoldersPayload{UID: &uid})
			if err != nil {
				return nil, err
			}
			return server.NewListCommitteeLinkFoldersResponseBody(res), nil
		}},
		{"get document", func(ctx context.Context, s *committeeServicesrvc) (any, error) {
			res, err := s.GetCommitteeDocument(ctx, &committeeservice.GetCommitteeDocumentPayload{UID: &uid, DocumentUID: &resourceUID})
			if err != nil {
				return nil, err
			}
			return server.NewGetCommitteeDocumentResponseBody(res), nil
		}},
	}
	for _, principal := range []string{"", "viewer"} {
		for _, storedEmail := range []string{"", "first.last@example.com"} {
			for _, endpoint := range endpoints {
				t.Run(principal+"/"+storedEmail+"/"+endpoint.name, func(t *testing.T) {
					ctx := context.WithValue(context.Background(), constants.PrincipalContextID, principal)
					user := &model.CommitteeUser{Username: "first-last", Email: storedEmail}
					links := mock.NewMockLinkRepository()
					docs := mock.NewMockDocumentRepository()
					require.NoError(t, links.CreateLink(ctx, &model.CommitteeLink{UID: resourceUID, CommitteeUID: uid, CreatedBy: user, UpdatedBy: user}))
					require.NoError(t, links.CreateLinkFolder(ctx, &model.CommitteeLinkFolder{UID: resourceUID, CommitteeUID: uid, CreatedBy: user, UpdatedBy: user}))
					require.NoError(t, docs.CreateDocumentMetadata(ctx, &model.CommitteeDocument{UID: resourceUID, CommitteeUID: uid, CreatedBy: user, UpdatedBy: user}))
					reader := &auditReadUserReader{mockUserReader: mockReaderForPrincipalEmail("first-last", "first.last@example.com").withMetadata("first-last", &model.UserMetadata{Name: "First Last", Picture: "https://example.com/avatar.png"})}
					svc := &committeeServicesrvc{
						linkReader: links, docReader: docs, userReader: reader,
						committeeReaderOrchestrator: &stubCommitteeReader{base: &model.CommitteeBase{UID: uid, Public: true}},
					}
					res, err := endpoint.call(ctx, svc)
					require.NoError(t, err)
					body, err := json.Marshal(res)
					require.NoError(t, err)
					assert.NotContains(t, string(body), `"email"`)
					assert.NotContains(t, string(body), "first.last@example.com")
					assert.Contains(t, string(body), `"created_by"`)
					assert.Contains(t, string(body), `"updated_by"`)
					assert.Contains(t, string(body), `"username":"first-last"`)
					assert.Zero(t, reader.emailCalls, "read endpoints must never request primary emails")
					assert.Equal(t, storedEmail, user.Email, "stored audit user must not be mutated")
				})
			}
		}
	}
}
