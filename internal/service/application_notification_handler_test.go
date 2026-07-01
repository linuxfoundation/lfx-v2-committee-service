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

// buildApplicationSubmittedPayload marshals a CommitteeEvent for committee_application.submitted.
func buildApplicationSubmittedPayload(t *testing.T, app *model.CommitteeApplication) []byte {
	t.Helper()
	event := model.CommitteeEvent{}
	built, err := event.Build(context.Background(), model.ResourceCommitteeApplication, model.ActionCreated, app)
	require.NoError(t, err)
	data, err := json.Marshal(built)
	require.NoError(t, err)
	return data
}

// buildApplicationUpdatedPayload marshals a CommitteeEvent for committee_application.updated.
func buildApplicationUpdatedPayload(t *testing.T, app *model.CommitteeApplication) []byte {
	t.Helper()
	event := model.CommitteeEvent{}
	built, err := event.Build(context.Background(), model.ResourceCommitteeApplication, model.ActionUpdated, app)
	require.NoError(t, err)
	data, err := json.Marshal(built)
	require.NoError(t, err)
	return data
}

func TestHandleCommitteeApplicationSubmitted(t *testing.T) {
	repo := mock.NewMockRepository()
	repo.AddCommittee(&model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:         "committee-1",
			Name:        "TSC Committee",
			ProjectUID:  "project-1",
			ProjectName: "Kubernetes",
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID: "committee-1",
			Writers: []model.CommitteeUser{
				{Username: "writer1", Email: "writer1@example.com", Name: "Writer One"},
				{Username: "writer2", Email: "writer2@example.com", Name: "Writer Two"},
			},
		},
	})
	reader := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repo)))

	app := &model.CommitteeApplication{
		UID:            "app-1",
		CommitteeUID:   "committee-1",
		ApplicantEmail: "applicant@example.com",
		Message:        "Please let me join",
		Status:         "pending",
	}

	t.Run("fans out one email per LFID writer with email and includes project name", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, buildApplicationSubmittedPayload(t, app))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err)
		assert.Len(t, sender.calls, 2)
		var tos []string
		for _, c := range sender.calls {
			tos = append(tos, c.To)
			assert.Contains(t, c.Subject, "New application to TSC Committee")
			assert.Contains(t, c.HTML, "Kubernetes")
			assert.Contains(t, c.Text, "Kubernetes")
			assert.NotEmpty(t, c.HTML)
			assert.NotEmpty(t, c.Text)
		}
		assert.ElementsMatch(t, []string{"writer1@example.com", "writer2@example.com"}, tos)
	})

	t.Run("falls back to project writers when no committee writers are eligible", func(t *testing.T) {
		repoFallback := mock.NewMockRepository()
		repoFallback.AddCommittee(&model.Committee{
			CommitteeBase: model.CommitteeBase{
				UID:         "committee-no-writers",
				Name:        "Empty Writers Committee",
				ProjectUID:  "project-fallback",
				ProjectName: "CNCF",
			},
			CommitteeSettings: &model.CommitteeSettings{
				UID:     "committee-no-writers",
				Writers: []model.CommitteeUser{},
			},
		})
		repoFallback.AddProjectWriters("project-fallback", []model.CommitteeUser{
			{Username: "projwriter", Email: "projwriter@example.com", Name: "Project Writer"},
		})

		readerFallback := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repoFallback)))
		projectReader := mock.NewMockProjectRetriever(repoFallback)
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     readerFallback,
			projectReader:       projectReader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		appFallback := &model.CommitteeApplication{
			UID:            "app-fallback",
			CommitteeUID:   "committee-no-writers",
			ApplicantEmail: "applicant@example.com",
			Status:         "pending",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, buildApplicationSubmittedPayload(t, appFallback))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err)
		assert.Len(t, sender.calls, 1)
		assert.Equal(t, "projwriter@example.com", sender.calls[0].To)
	})

	t.Run("skips writer without LFID", func(t *testing.T) {
		repo2 := mock.NewMockRepository()
		repo2.AddCommittee(&model.Committee{
			CommitteeBase: model.CommitteeBase{UID: "committee-2", Name: "TC"},
			CommitteeSettings: &model.CommitteeSettings{
				UID: "committee-2",
				Writers: []model.CommitteeUser{
					{Username: "", Email: "nolfid@example.com"}, // no LFID
					{Username: "w1", Email: "w1@example.com"},   // LFID + email
				},
			},
		})
		reader2 := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repo2)))
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader2,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		app2 := &model.CommitteeApplication{UID: "app-2", CommitteeUID: "committee-2", ApplicantEmail: "a@b.com", Status: "pending"}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, buildApplicationSubmittedPayload(t, app2))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err)
		assert.Len(t, sender.calls, 1)
		assert.Equal(t, "w1@example.com", sender.calls[0].To)
	})

	t.Run("skips writer without email", func(t *testing.T) {
		repo3 := mock.NewMockRepository()
		repo3.AddCommittee(&model.Committee{
			CommitteeBase: model.CommitteeBase{UID: "committee-3", Name: "TC"},
			CommitteeSettings: &model.CommitteeSettings{
				UID: "committee-3",
				Writers: []model.CommitteeUser{
					{Username: "nomail", Email: ""},
				},
			},
		})
		reader3 := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repo3)))
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader3,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		app3 := &model.CommitteeApplication{UID: "app-3", CommitteeUID: "committee-3", ApplicantEmail: "a@b.com", Status: "pending"}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, buildApplicationSubmittedPayload(t, app3))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err)
		assert.Empty(t, sender.calls)
	})

	t.Run("no-ops when email sender is nil", func(t *testing.T) {
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         nil,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, buildApplicationSubmittedPayload(t, app))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err)
	})

	t.Run("bad payload is discarded without error", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, []byte("not json"))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err)
		assert.Empty(t, sender.calls)
	})

	t.Run("send failure is logged and does not propagate", func(t *testing.T) {
		sender := &mockEmailSender{retErr: assert.AnError}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, buildApplicationSubmittedPayload(t, app))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err) // best-effort: error must not propagate
	})
}

func TestHandleCommitteeApplicationUpdated(t *testing.T) {
	repo := mock.NewMockRepository()
	repo.AddCommittee(&model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:  "committee-1",
			Name: "TSC Committee",
		},
		CommitteeSettings: &model.CommitteeSettings{UID: "committee-1"},
	})
	reader := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repo)))

	t.Run("sends accepted email to applicant", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		app := &model.CommitteeApplication{
			UID:            "app-1",
			CommitteeUID:   "committee-1",
			ApplicantEmail: "applicant@example.com",
			Status:         "approved",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationUpdatedSubject, buildApplicationUpdatedPayload(t, app))
		_, err := h.HandleCommitteeApplicationUpdated(context.Background(), msg)
		require.NoError(t, err)
		require.Len(t, sender.calls, 1)
		assert.Equal(t, "applicant@example.com", sender.calls[0].To)
		assert.Contains(t, sender.calls[0].Subject, "accepted")
		assert.NotEmpty(t, sender.calls[0].HTML)
		assert.NotEmpty(t, sender.calls[0].Text)
	})

	t.Run("sends rejected email to applicant with reviewer notes", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		app := &model.CommitteeApplication{
			UID:            "app-2",
			CommitteeUID:   "committee-1",
			ApplicantEmail: "applicant@example.com",
			Status:         "rejected",
			ReviewerNotes:  "Not a fit at this time.",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationUpdatedSubject, buildApplicationUpdatedPayload(t, app))
		_, err := h.HandleCommitteeApplicationUpdated(context.Background(), msg)
		require.NoError(t, err)
		require.Len(t, sender.calls, 1)
		assert.Equal(t, "applicant@example.com", sender.calls[0].To)
		assert.Contains(t, sender.calls[0].Subject, "Update on your application")
		assert.Contains(t, sender.calls[0].HTML, "Not a fit at this time.")
		assert.Contains(t, sender.calls[0].Text, "Not a fit at this time.")
	})

	t.Run("skips pending status (reinstatement)", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		app := &model.CommitteeApplication{
			UID:            "app-3",
			CommitteeUID:   "committee-1",
			ApplicantEmail: "applicant@example.com",
			Status:         "pending",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationUpdatedSubject, buildApplicationUpdatedPayload(t, app))
		_, err := h.HandleCommitteeApplicationUpdated(context.Background(), msg)
		require.NoError(t, err)
		assert.Empty(t, sender.calls)
	})

	t.Run("no-ops when email sender is nil", func(t *testing.T) {
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         nil,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		app := &model.CommitteeApplication{
			UID:            "app-4",
			CommitteeUID:   "committee-1",
			ApplicantEmail: "applicant@example.com",
			Status:         "approved",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationUpdatedSubject, buildApplicationUpdatedPayload(t, app))
		_, err := h.HandleCommitteeApplicationUpdated(context.Background(), msg)
		require.NoError(t, err)
	})

	t.Run("bad payload is discarded without error", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationUpdatedSubject, []byte("not json"))
		_, err := h.HandleCommitteeApplicationUpdated(context.Background(), msg)
		require.NoError(t, err)
		assert.Empty(t, sender.calls)
	})

	t.Run("send failure is logged and does not propagate", func(t *testing.T) {
		sender := &mockEmailSender{retErr: assert.AnError}
		h := &messageHandlerOrchestrator{
			committeeReader:     reader,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://lfx.linuxfoundation.org",
		}
		app := &model.CommitteeApplication{
			UID:            "app-5",
			CommitteeUID:   "committee-1",
			ApplicantEmail: "applicant@example.com",
			Status:         "approved",
		}
		msg := newMockTransportMessenger(constants.CommitteeApplicationUpdatedSubject, buildApplicationUpdatedPayload(t, app))
		_, err := h.HandleCommitteeApplicationUpdated(context.Background(), msg)
		require.NoError(t, err) // best-effort: error must not propagate
	})
}

func TestApplicationNotificationsProjectAllowlist(t *testing.T) {
	repoAllowlisted := mock.NewMockRepository()
	repoAllowlisted.AddCommittee(&model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:         "committee-allow",
			Name:        "TSC",
			ProjectUID:  "project-allow",
			ProjectName: "AAIF",
			ProjectSlug: "aaif",
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:     "committee-allow",
			Writers: []model.CommitteeUser{{Username: "writer1", Email: "writer1@example.com", Name: "Writer One"}},
		},
	})

	repoBlocked := mock.NewMockRepository()
	repoBlocked.AddCommittee(&model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:         "committee-block",
			Name:        "TSC",
			ProjectUID:  "project-block",
			ProjectName: "PyTorch",
			ProjectSlug: "pytorch",
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:     "committee-block",
			Writers: []model.CommitteeUser{{Username: "writer1", Email: "writer1@example.com", Name: "Writer One"}},
		},
	})

	readerAllow := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repoAllowlisted)))
	readerBlock := NewCommitteeReaderOrchestrator(WithCommitteeReader(mock.NewMockCommitteeReader(repoBlocked)))

	t.Run("submitted — allowlisted project sends notification", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{committeeReader: readerAllow, emailSender: sender}
		WithNotificationProjectAllowlistForMessageHandler([]string{"aaif"})(h)
		app := &model.CommitteeApplication{CommitteeUID: "committee-allow", ApplicantEmail: "a@example.com"}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, buildApplicationSubmittedPayload(t, app))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err)
		assert.Len(t, sender.calls, 1)
	})

	t.Run("submitted — non-allowlisted project suppressed", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{committeeReader: readerBlock, emailSender: sender}
		WithNotificationProjectAllowlistForMessageHandler([]string{"aaif"})(h)
		app := &model.CommitteeApplication{CommitteeUID: "committee-block", ApplicantEmail: "a@example.com"}
		msg := newMockTransportMessenger(constants.CommitteeApplicationSubmittedSubject, buildApplicationSubmittedPayload(t, app))
		_, err := h.HandleCommitteeApplicationSubmitted(context.Background(), msg)
		require.NoError(t, err)
		assert.Len(t, sender.calls, 0)
	})

	t.Run("updated approved — allowlisted project sends notification", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     readerAllow,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		WithNotificationProjectAllowlistForMessageHandler([]string{"aaif"})(h)
		app := &model.CommitteeApplication{CommitteeUID: "committee-allow", ApplicantEmail: "a@example.com", Status: "approved"}
		msg := newMockTransportMessenger(constants.CommitteeApplicationUpdatedSubject, buildApplicationUpdatedPayload(t, app))
		_, err := h.HandleCommitteeApplicationUpdated(context.Background(), msg)
		require.NoError(t, err)
		assert.Len(t, sender.calls, 1)
	})

	t.Run("updated approved — non-allowlisted project suppressed", func(t *testing.T) {
		sender := &mockEmailSender{}
		h := &messageHandlerOrchestrator{
			committeeReader:     readerBlock,
			emailSender:         sender,
			lfxSelfServeBaseURL: "https://app.dev.lfx.dev",
		}
		WithNotificationProjectAllowlistForMessageHandler([]string{"aaif"})(h)
		app := &model.CommitteeApplication{CommitteeUID: "committee-block", ApplicantEmail: "a@example.com", Status: "approved"}
		msg := newMockTransportMessenger(constants.CommitteeApplicationUpdatedSubject, buildApplicationUpdatedPayload(t, app))
		_, err := h.HandleCommitteeApplicationUpdated(context.Background(), msg)
		require.NoError(t, err)
		assert.Len(t, sender.calls, 0)
	})
}
