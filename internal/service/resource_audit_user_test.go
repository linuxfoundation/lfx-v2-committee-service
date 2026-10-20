// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceIndexerMessages_PublicAuditUsers(t *testing.T) {
	for _, action := range []model.MessageAction{model.ActionCreated, model.ActionUpdated, model.ActionDeleted} {
		for _, withUser := range []bool{false, true} {
			for _, resource := range []string{"link", "folder", "document"} {
				t.Run(string(action)+"/"+resource+"/"+map[bool]string{false: "nil user", true: "full user"}[withUser], func(t *testing.T) {
					var user *model.CommitteeUser
					if withUser {
						user = &model.CommitteeUser{Username: "first-last", Name: "First Last", Avatar: "https://example.com/avatar.png", Email: "first.last@example.com"}
					}
					publisher := &mock.MockCommitteePublisher{}
					ctx := context.Background()
					var subject string
					switch resource {
					case "link":
						o := &linkWriterOrchestrator{committeePublisher: publisher}
						o.publishLinkIndexerMessage(ctx, action, &model.CommitteeLink{UID: "resource-1", CommitteeUID: "committee-1", Name: "Resource", CreatedBy: user, UpdatedBy: user}, true)
						subject = constants.IndexCommitteeLinkSubject
					case "folder":
						o := &linkWriterOrchestrator{committeePublisher: publisher}
						o.publishLinkFolderIndexerMessage(ctx, action, &model.CommitteeLinkFolder{UID: "resource-1", CommitteeUID: "committee-1", Name: "Resource", CreatedBy: user, UpdatedBy: user}, true)
						subject = constants.IndexCommitteeLinkFolderSubject
					case "document":
						o := &documentWriterOrchestrator{committeePublisher: publisher}
						o.publishDocumentIndexerMessage(ctx, action, &model.CommitteeDocument{UID: "resource-1", CommitteeUID: "committee-1", Name: "Resource", CreatedBy: user, UpdatedBy: user}, true)
						subject = constants.IndexCommitteeDocumentSubject
					}
					require.Len(t, publisher.IndexerMessages, 1)
					assert.Equal(t, []string{subject}, publisher.IndexerSubjects)
					assert.Equal(t, []bool{true}, publisher.IndexerSyncValues)
					msg := publisher.LastIndexerMessage.(*model.CommitteeIndexerMessage)
					if action == model.ActionDeleted {
						assert.Equal(t, "resource-1", msg.Data)
						assert.Nil(t, msg.IndexingConfig)
					} else {
						assert.Equal(t, "committee:committee-1", msg.IndexingConfig.AccessCheckObject)
						assert.Equal(t, "viewer", msg.IndexingConfig.AccessCheckRelation)
						data := msg.Data.(map[string]any)
						for _, field := range []string{"created_by", "updated_by"} {
							if !withUser {
								assert.NotContains(t, data, field)
								continue
							}
							assert.Equal(t, map[string]any{"username": "first-last", "name": "First Last", "avatar": "https://example.com/avatar.png"}, data[field])
						}
					}
					if withUser {
						assert.Equal(t, "first.last@example.com", user.Email, "publication must not mutate stored audit users")
					}
				})
			}
		}
	}
}
