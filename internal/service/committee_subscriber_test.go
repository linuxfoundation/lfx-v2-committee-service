// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"testing"

	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

// mockEmailSender records SendEmail calls for assertions.
type mockEmailSender struct {
	calls  []emailapi.SendEmailRequest
	retErr error
}

func (m *mockEmailSender) SendEmail(_ context.Context, req emailapi.SendEmailRequest) error {
	m.calls = append(m.calls, req)
	return m.retErr
}

func buildMemberCreatedPayload(t *testing.T, member *model.CommitteeMember) []byte {
	t.Helper()
	event := model.CommitteeEvent{}
	built, err := event.Build(context.Background(), model.ResourceCommitteeMember, model.ActionCreated, member)
	require.NoError(t, err)
	data, err := json.Marshal(built)
	require.NoError(t, err)
	return data
}

func TestHandleCommitteeMemberCreated(t *testing.T) {
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			Email:         "alice@example.com",
			FirstName:     "Alice",
			LastName:      "Smith",
			CommitteeUID:  "committee-1",
			CommitteeName: "TSC Committee",
			ProjectSlug:   "demo-project",
			Role:          model.CommitteeMemberRole{Name: "writer"},
		},
	}

	tests := []struct {
		name            string
		msgData         []byte
		emailSender     *mockEmailSender
		omitEmailSender bool
		wantSendCount   int
	}{
		{
			name:          "member with email — notification sent",
			msgData:       buildMemberCreatedPayload(t, member),
			emailSender:   &mockEmailSender{},
			wantSendCount: 1,
		},
		{
			name: "member without email — no notification sent",
			msgData: buildMemberCreatedPayload(t, &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					Username:      "noemail",
					CommitteeUID:  "committee-1",
					CommitteeName: "TSC Committee",
					Role:          model.CommitteeMemberRole{Name: "writer"},
				},
			}),
			emailSender:   &mockEmailSender{},
			wantSendCount: 0,
		},
		{
			name:          "send error — handler still returns nil",
			msgData:       buildMemberCreatedPayload(t, member),
			emailSender:   &mockEmailSender{retErr: assert.AnError},
			wantSendCount: 1,
		},
		{
			name:          "invalid JSON — handler returns nil",
			msgData:       []byte("not json"),
			emailSender:   &mockEmailSender{},
			wantSendCount: 0,
		},
		{
			name:            "no email sender configured — handler returns nil",
			msgData:         buildMemberCreatedPayload(t, member),
			omitEmailSender: true,
			wantSendCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &messageHandlerOrchestrator{
				lfxSelfServeBaseURL: "https://dev.app.lfx.dev",
			}
			if !tt.omitEmailSender {
				h.emailSender = tt.emailSender
			}

			msg := newMockTransportMessenger(constants.CommitteeMemberCreatedSubject, tt.msgData)
			resp, err := h.HandleCommitteeMemberCreated(context.Background(), msg)

			assert.NoError(t, err)
			assert.Nil(t, resp)

			if tt.emailSender != nil {
				assert.Len(t, tt.emailSender.calls, tt.wantSendCount)
				if tt.wantSendCount > 0 {
					assert.Equal(t, "alice@example.com", tt.emailSender.calls[0].To)
					assert.Contains(t, tt.emailSender.calls[0].Subject, "TSC Committee")
					assert.Contains(t, tt.emailSender.calls[0].HTML, "Alice Smith")
					assert.Contains(t, tt.emailSender.calls[0].HTML, "https://dev.app.lfx.dev/projects/demo-project/committees")
				}
			}
		})
	}
}

func TestBuildCommitteeURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		projectSlug string
		wantURL     string
	}{
		{
			name:        "with project slug",
			baseURL:     "https://dev.app.lfx.dev",
			projectSlug: "demo-project",
			wantURL:     "https://dev.app.lfx.dev/projects/demo-project/committees",
		},
		{
			name:        "without project slug falls back to overview",
			baseURL:     "https://dev.app.lfx.dev",
			projectSlug: "",
			wantURL:     "https://dev.app.lfx.dev/projects/overview",
		},
		{
			name:        "trailing slash stripped from base URL",
			baseURL:     "https://dev.app.lfx.dev/",
			projectSlug: "demo-project",
			wantURL:     "https://dev.app.lfx.dev/projects/demo-project/committees",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCommitteeURL(tt.baseURL, tt.projectSlug)
			assert.Equal(t, tt.wantURL, got)
		})
	}
}
