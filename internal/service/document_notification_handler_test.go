// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildDocumentCreatedPayload builds a marshalled CommitteeEvent for committee_document.created.
func buildDocumentCreatedPayload(t *testing.T, doc *model.CommitteeDocument) []byte {
	t.Helper()
	event := model.CommitteeEvent{}
	built, err := event.Build(context.Background(), model.ResourceCommitteeDocument, model.ActionCreated, doc)
	require.NoError(t, err)
	data, err := json.Marshal(built)
	require.NoError(t, err)
	return data
}

// buildLinkCreatedPayload builds a marshalled CommitteeEvent for committee_link.created.
func buildLinkCreatedPayload(t *testing.T, link *model.CommitteeLink) []byte {
	t.Helper()
	event := model.CommitteeEvent{}
	built, err := event.Build(context.Background(), model.ResourceCommitteeLink, model.ActionCreated, link)
	require.NoError(t, err)
	data, err := json.Marshal(built)
	require.NoError(t, err)
	return data
}

func TestHandleCommitteeDocumentCreated(t *testing.T) {
	repo := mock.NewMockRepository()
	repo.AddCommittee(&model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:  "committee-1",
			Name: "TSC Committee",
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID: "committee-1",
			Writers: []model.CommitteeUser{
				{Username: "writer1", Email: "writer1@example.com", Name: "Writer One"},
			},
			Auditors: []model.CommitteeUser{
				{Username: "auditor1", Email: "auditor1@example.com", Name: "Auditor One"},
			},
		},
	})
	repo.AddCommitteeMember("committee-1", &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			UID:          "m1",
			Username:     "member1",
			Email:        "member1@example.com",
			FirstName:    "Member",
			LastName:     "One",
			CommitteeUID: "committee-1",
		},
	})

	reader := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repo)))

	doc := &model.CommitteeDocument{
		UID:                "doc-1",
		CommitteeUID:       "committee-1",
		Name:               "Q1 Report",
		FileName:           "q1-report.pdf",
		UploadedByUsername: "uploader1",
	}

	t.Run("sends one email per distinct LFID recipient", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, buildDocumentCreatedPayload(t, doc))
		resp, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)

		assert.NoError(t, err)
		assert.Nil(t, resp)
		// member1 + writer1 + auditor1 = 3 distinct LFIDs
		assert.Len(t, sender.calls, 3, "expected one email per distinct LFID recipient")
	})

	t.Run("send failure does not abort remaining recipients", func(t *testing.T) {
		sender := &mockEmailSender{retErr: assert.AnError}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, buildDocumentCreatedPayload(t, doc))
		resp, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)

		// Best-effort — no error propagated, all sends attempted
		assert.NoError(t, err)
		assert.Nil(t, resp)
		assert.Len(t, sender.calls, 3, "all recipients attempted despite send errors")
	})

	t.Run("deduplicates LFID appearing in multiple role lists", func(t *testing.T) {
		// Add writer1 also as a member — should only get one email.
		dupRepo := mock.NewMockRepository()
		dupRepo.AddCommittee(&model.Committee{
			CommitteeBase: model.CommitteeBase{UID: "c-dup", Name: "Dup Committee"},
			CommitteeSettings: &model.CommitteeSettings{
				UID: "c-dup",
				Writers: []model.CommitteeUser{
					{Username: "shared-user", Email: "shared@example.com", Name: "Shared"},
				},
			},
		})
		dupRepo.AddCommitteeMember("c-dup", &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "m-shared",
				Username:     "shared-user",
				Email:        "shared@example.com",
				CommitteeUID: "c-dup",
			},
		})

		dupReader := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(dupRepo)))
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     dupReader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		dupDoc := &model.CommitteeDocument{UID: "d1", CommitteeUID: "c-dup", Name: "Doc", FileName: "doc.pdf", UploadedByUsername: "shared-user"}
		msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, buildDocumentCreatedPayload(t, dupDoc))
		resp, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)

		assert.NoError(t, err)
		assert.Nil(t, resp)
		assert.Len(t, sender.calls, 1, "shared LFID should only receive one email")
	})

	t.Run("skips users without LFID", func(t *testing.T) {
		noLFIDRepo := mock.NewMockRepository()
		noLFIDRepo.AddCommittee(&model.Committee{
			CommitteeBase:     model.CommitteeBase{UID: "c-nolfid", Name: "NoLFID Committee"},
			CommitteeSettings: &model.CommitteeSettings{UID: "c-nolfid"},
		})
		noLFIDRepo.AddCommitteeMember("c-nolfid", &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "m-nolfid",
				Username:     "", // no LFID
				Email:        "nolfid@example.com",
				CommitteeUID: "c-nolfid",
			},
		})

		noLFIDReader := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(noLFIDRepo)))
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     noLFIDReader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		noLFIDDoc := &model.CommitteeDocument{UID: "d1", CommitteeUID: "c-nolfid", Name: "Doc", FileName: "doc.pdf"}
		msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, buildDocumentCreatedPayload(t, noLFIDDoc))
		_, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)

		assert.NoError(t, err)
		assert.Empty(t, sender.calls, "users without LFID should not receive emails")
	})

	t.Run("member with no stored email is skipped", func(t *testing.T) {
		noEmailRepo := mock.NewMockRepository()
		noEmailRepo.AddCommittee(&model.Committee{
			CommitteeBase:     model.CommitteeBase{UID: "c-noemail", Name: "NoEmail Committee"},
			CommitteeSettings: &model.CommitteeSettings{UID: "c-noemail"},
		})
		noEmailRepo.AddCommitteeMember("c-noemail", &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "m-noemail",
				Username:     "user-with-lfid",
				Email:        "", // no stored email
				CommitteeUID: "c-noemail",
			},
		})

		noEmailReader := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(noEmailRepo)))
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     noEmailReader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		noEmailDoc := &model.CommitteeDocument{UID: "d1", CommitteeUID: "c-noemail", Name: "Doc", FileName: "doc.pdf", UploadedByUsername: "uploader"}
		msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, buildDocumentCreatedPayload(t, noEmailDoc))
		_, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)

		assert.NoError(t, err)
		assert.Empty(t, sender.calls, "member with no stored email should be skipped")
	})

	t.Run("invalid JSON — returns nil, no panic", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{emailSender: sender, committeeReader: reader}
		msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, []byte("not json"))
		resp, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)

		assert.NoError(t, err)
		assert.Nil(t, resp)
		assert.Empty(t, sender.calls)
	})

	t.Run("no email sender configured — handler returns nil without panic", func(t *testing.T) {
		h := &messageHandlerOrchestrator{committeeReader: reader, lfxSelfServeBaseURL: "https://app.dev.lfx.dev"}
		msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, buildDocumentCreatedPayload(t, doc))
		resp, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)

		assert.NoError(t, err)
		assert.Nil(t, resp)
	})

	t.Run("email contains document name and committee URL", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, buildDocumentCreatedPayload(t, doc))
		resp, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)
		assert.NoError(t, err)
		assert.Nil(t, resp)
		require.NotEmpty(t, sender.calls)
		assert.Contains(t, sender.calls[0].Subject, "TSC Committee")
		assert.Contains(t, sender.calls[0].HTML, "Q1 Report")
		assert.Contains(t, sender.calls[0].HTML, "https://app.dev.lfx.dev/project/groups/committee-1")
	})
}

func TestHandleCommitteeLinkCreated(t *testing.T) {
	repo := mock.NewMockRepository()
	repo.AddCommittee(&model.Committee{
		CommitteeBase: model.CommitteeBase{UID: "committee-2", Name: "TAC Committee"},
		CommitteeSettings: &model.CommitteeSettings{
			UID: "committee-2",
			Writers: []model.CommitteeUser{
				{Username: "writer2", Email: "writer2@example.com", Name: "Writer Two"},
			},
		},
	})

	reader := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repo)))

	t.Run("safe https URL included in email", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		link := &model.CommitteeLink{UID: "l1", CommitteeUID: "committee-2", Name: "LFX", URL: "https://lfx.linuxfoundation.org", CreatedByUsername: "writer2"}
		msg := newMockTransportMessenger(constants.CommitteeLinkCreatedSubject, buildLinkCreatedPayload(t, link))
		_, err := h.HandleCommitteeLinkCreated(context.Background(), msg)

		assert.NoError(t, err)
		require.Len(t, sender.calls, 1)
		assert.Contains(t, sender.calls[0].HTML, "https://lfx.linuxfoundation.org")
		assert.Contains(t, sender.calls[0].Subject, "link")
	})

	t.Run("unsafe javascript: URL omitted from email", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		link := &model.CommitteeLink{UID: "l2", CommitteeUID: "committee-2", Name: "Bad Link", URL: "javascript:alert(1)", CreatedByUsername: "writer2"}
		msg := newMockTransportMessenger(constants.CommitteeLinkCreatedSubject, buildLinkCreatedPayload(t, link))
		_, err := h.HandleCommitteeLinkCreated(context.Background(), msg)

		assert.NoError(t, err)
		require.Len(t, sender.calls, 1)
		// The unsafe URL must not appear in the email
		assert.NotContains(t, sender.calls[0].HTML, "javascript:")
		assert.NotContains(t, sender.calls[0].Text, "javascript:")
	})
}

func TestHandleContentCreatedProjectAllowlist(t *testing.T) {
	buildRepo := func(committeeUID, projectSlug string) CommitteeReader {
		r := mock.NewMockRepository()
		r.AddCommittee(&model.Committee{
			CommitteeBase: model.CommitteeBase{
				UID:         committeeUID,
				Name:        "Test Committee",
				ProjectSlug: projectSlug,
			},
			CommitteeSettings: &model.CommitteeSettings{
				UID: committeeUID,
				Writers: []model.CommitteeUser{
					{Username: "writer1", Email: "writer1@example.com", Name: "Writer One"},
				},
			},
		})
		return NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(r)))
	}

	doc := func(committeeUID string) *model.CommitteeDocument {
		return &model.CommitteeDocument{UID: "d1", CommitteeUID: committeeUID, Name: "Doc", FileName: "doc.pdf"}
	}

	tests := []struct {
		name          string
		committeeUID  string
		projectSlug   string
		allowlist     []string
		expectedCalls int
	}{
		{"allowlisted project sends content notification", "c-allowed", "aaif", []string{"aaif"}, 1},
		{"non-allowlisted project suppresses content notification", "c-blocked", "other-project", []string{"aaif"}, 0},
		{"empty allowlist sends to all projects", "c-any", "any-project", nil, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &mockEmailSender{}
			h := &messageHandlerOrchestrator{
				committeeReader:     buildRepo(tt.committeeUID, tt.projectSlug),
				emailSender:         sender,
				lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
			}
			WithNotificationProjectAllowlistForMessageHandler(tt.allowlist)(h)
			msg := newMockTransportMessenger(constants.CommitteeDocumentCreatedSubject, buildDocumentCreatedPayload(t, doc(tt.committeeUID)))
			_, err := h.HandleCommitteeDocumentCreated(context.Background(), msg)
			assert.NoError(t, err)
			assert.Len(t, sender.calls, tt.expectedCalls)
		})
	}
}

func TestIsSafeURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://example.com", true},
		{"http://example.com/path?q=1", true},
		{"javascript:alert(1)", false},
		{"ftp://files.example.com", false},
		{"data:text/html,<h1>hi</h1>", false},
		{"", false},
		{"not a url at all", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			assert.Equal(t, tt.want, isSafeURL(tt.url))
		})
	}
}
