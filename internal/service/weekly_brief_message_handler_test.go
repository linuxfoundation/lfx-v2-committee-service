// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

// recordingGenerator captures the GroupWeeklyBriefGenerateInput passed to Fulfill.
type recordingGenerator struct {
	gotInput GroupWeeklyBriefGenerateInput
	err      error
}

func (r *recordingGenerator) Claim(_ context.Context, _ GroupWeeklyBriefGenerateInput) (*GroupWeeklyBriefGenerateOutput, error) {
	return nil, nil
}

func (r *recordingGenerator) Fulfill(_ context.Context, in GroupWeeklyBriefGenerateInput) error {
	r.gotInput = in
	return r.err
}

func mustMarshalEvent(t *testing.T, e GenerateWeeklyBriefRequestedEvent) []byte {
	t.Helper()
	b, err := json.Marshal(e)
	require.NoError(t, err)
	return b
}

func TestHandleGenerateWeeklyBriefRequested_FieldForwarding(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	committeeUID := "c-mapping-test"

	tests := []struct {
		name               string
		event              GenerateWeeklyBriefRequestedEvent
		settingsVisibility string // "" means no settings in mock (reader returns error → defaults to hidden)
		wantInput          GroupWeeklyBriefGenerateInput
	}{
		{
			name: "all event fields forwarded including ProjectUID",
			event: GenerateWeeklyBriefRequestedEvent{
				CommitteeUID:  committeeUID,
				CommitteeName: "Test Committee",
				ProjectUID:    "proj-42",
				ProjectName:   "Test Project",
				Force:         true,
				RequestedAt:   now,
			},
			settingsVisibility: "basic_profile",
			wantInput: GroupWeeklyBriefGenerateInput{
				CommitteeUID:  committeeUID,
				CommitteeName: "Test Committee",
				ProjectUID:    "proj-42",
				ProjectName:   "Test Project",
				Force:         true,
				Now:           now,
				MembersHidden: false,
			},
		},
		{
			name: "MembersHidden true when visibility is not basic_profile",
			event: GenerateWeeklyBriefRequestedEvent{
				CommitteeUID: committeeUID,
				ProjectUID:   "proj-99",
				RequestedAt:  now,
			},
			settingsVisibility: "hidden",
			wantInput: GroupWeeklyBriefGenerateInput{
				CommitteeUID:  committeeUID,
				ProjectUID:    "proj-99",
				Now:           now,
				MembersHidden: true,
			},
		},
		{
			name: "MembersHidden true when reader returns no settings",
			event: GenerateWeeklyBriefRequestedEvent{
				CommitteeUID: committeeUID,
				ProjectUID:   "proj-77",
				RequestedAt:  now,
			},
			settingsVisibility: "", // no settings → GetSettings returns not-found
			wantInput: GroupWeeklyBriefGenerateInput{
				CommitteeUID:  committeeUID,
				ProjectUID:    "proj-77",
				Now:           now,
				MembersHidden: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mock.NewMockRepository()
			mockRepo.ClearAll()

			if tt.settingsVisibility != "" {
				mockRepo.AddCommittee(&model.Committee{
					CommitteeBase: model.CommitteeBase{UID: committeeUID},
					CommitteeSettings: &model.CommitteeSettings{
						UID:              committeeUID,
						MemberVisibility: tt.settingsVisibility,
					},
				})
			}

			rec := &recordingGenerator{}
			reader := NewCommitteeReaderOrchestrator(WithCommitteeReader(mockRepo))
			handler := NewWeeklyBriefMessageHandler(rec, reader)

			msg := &mockStreamMessenger{
				subject: constants.GenerateWeeklyBriefRequestedSubject,
				data:    mustMarshalEvent(t, tt.event),
			}

			err := handler.HandleGenerateWeeklyBriefRequested(context.Background(), msg)
			require.NoError(t, err)
			assert.Equal(t, tt.wantInput, rec.gotInput)
		})
	}
}

func TestHandleGenerateWeeklyBriefRequested_Guards(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	rec := &recordingGenerator{}
	handler := NewWeeklyBriefMessageHandler(rec, nil)

	t.Run("wrong subject is skipped", func(t *testing.T) {
		msg := &mockStreamMessenger{
			subject: "lfx.other.subject",
			data:    mustMarshalEvent(t, GenerateWeeklyBriefRequestedEvent{CommitteeUID: "c-1", RequestedAt: now}),
		}
		err := handler.HandleGenerateWeeklyBriefRequested(context.Background(), msg)
		require.NoError(t, err)
		assert.Empty(t, rec.gotInput.CommitteeUID, "Fulfill must not be called for unrelated subjects")
	})

	t.Run("missing committee_uid is discarded", func(t *testing.T) {
		msg := &mockStreamMessenger{
			subject: constants.GenerateWeeklyBriefRequestedSubject,
			data:    mustMarshalEvent(t, GenerateWeeklyBriefRequestedEvent{RequestedAt: now}),
		}
		err := handler.HandleGenerateWeeklyBriefRequested(context.Background(), msg)
		require.NoError(t, err)
		assert.Empty(t, rec.gotInput.CommitteeUID, "Fulfill must not be called when committee_uid is absent")
	})

	t.Run("invalid JSON is discarded", func(t *testing.T) {
		msg := &mockStreamMessenger{
			subject: constants.GenerateWeeklyBriefRequestedSubject,
			data:    []byte(`{not valid json`),
		}
		err := handler.HandleGenerateWeeklyBriefRequested(context.Background(), msg)
		require.NoError(t, err)
	})

	t.Run("nil reader defaults MembersHidden to true", func(t *testing.T) {
		rec2 := &recordingGenerator{}
		nilReaderHandler := NewWeeklyBriefMessageHandler(rec2, nil)
		msg := &mockStreamMessenger{
			subject: constants.GenerateWeeklyBriefRequestedSubject,
			data:    mustMarshalEvent(t, GenerateWeeklyBriefRequestedEvent{CommitteeUID: "c-2", ProjectUID: "p-2", RequestedAt: now}),
		}
		err := nilReaderHandler.HandleGenerateWeeklyBriefRequested(context.Background(), msg)
		require.NoError(t, err)
		assert.True(t, rec2.gotInput.MembersHidden, "nil reader must default to hidden")
		assert.Equal(t, "p-2", rec2.gotInput.ProjectUID)
	})
}
