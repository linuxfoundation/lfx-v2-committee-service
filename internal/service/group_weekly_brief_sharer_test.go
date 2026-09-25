// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// fakeSlackSender records calls and returns a configured error.
type fakeSlackSender struct {
	sendErr error
	calls   []slackCall
}

type slackCall struct {
	text string
}

func (f *fakeSlackSender) Send(_ context.Context, _ string, text string) error {
	// Intentionally discard webhookURL — it is a bearer credential.
	f.calls = append(f.calls, slackCall{text: text})
	return f.sendErr
}

// shareableBrief returns a generated brief for the test window.
func shareableBrief(state model.GroupWeeklyBriefState) *model.GroupWeeklyBrief {
	start, end := model.WeeklyWindow(testNow)
	return &model.GroupWeeklyBrief{
		UID:          "b-share-1",
		CommitteeUID: "c-share-1",
		WindowStart:  start,
		WindowEnd:    end,
		State:        state,
		BriefText:    "## Weekly brief\n\nHello, Slack!",
		Revision:     3,
	}
}

func makeSharerOrchestrator(brief *model.GroupWeeklyBrief, webhookURL *string, slackErr error) (GroupWeeklyBriefDataSharer, *fakeSlackSender) {
	mockRepo := mock.NewMockRepository()
	mockRepo.ClearAll()
	mockRepo.AddProject("project-1", "test-project", "Test Project")

	committee := &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:        "c-share-1",
			ProjectUID: "project-1",
			Name:       "Test Committee",
			Category:   "governance",
		},
		CommitteeSettings: &model.CommitteeSettings{
			UID:            "c-share-1",
			ChatWebhookURL: webhookURL,
		},
	}
	mockRepo.AddCommittee(committee)

	sender := &fakeSlackSender{sendErr: slackErr}

	sharer := NewGroupWeeklyBriefSharerOrchestrator(
		WithGroupWeeklyBriefReaderForSharer(&fakeBriefReader{brief: brief}),
		WithCommitteeSettingsReaderForSharer(NewCommitteeReaderOrchestrator(
			WithCommitteeReader(mock.NewMockCommitteeReader(mockRepo)),
		)),
		WithSlackSenderForSharer(sender),
	)
	return sharer, sender
}

func TestShareToChat_Success(t *testing.T) {
	webhookURL := "https://hooks.slack.example.org/services/TXXXXXXXX/BXXXXXXXX/placeholder"
	brief := shareableBrief(model.GroupWeeklyBriefStateGenerated)
	sharer, sender := makeSharerOrchestrator(brief, &webhookURL, nil)

	err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
		CommitteeUID: "c-share-1",
		Revision:     3,
		Now:          testNow,
	})

	require.NoError(t, err)
	require.Len(t, sender.calls, 1)
	assert.Equal(t, brief.BriefText, sender.calls[0].text)
}

func TestShareToChat_ShareableStates(t *testing.T) {
	webhookURL := "https://hooks.slack.example.org/services/TXXXXXXXX/BXXXXXXXX/placeholder"
	shareable := []model.GroupWeeklyBriefState{
		model.GroupWeeklyBriefStateGenerated,
		model.GroupWeeklyBriefStateEdited,
		model.GroupWeeklyBriefStateApproved,
	}
	for _, state := range shareable {
		t.Run(string(state), func(t *testing.T) {
			brief := shareableBrief(state)
			sharer, _ := makeSharerOrchestrator(brief, &webhookURL, nil)
			err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
				CommitteeUID: "c-share-1",
				Revision:     3,
				Now:          testNow,
			})
			require.NoError(t, err)
		})
	}
}

func TestShareToChat_NonShareableState(t *testing.T) {
	webhookURL := "https://hooks.slack.example.org/services/TXXXXXXXX/BXXXXXXXX/placeholder"
	for _, state := range []model.GroupWeeklyBriefState{
		model.GroupWeeklyBriefStateEmpty,
		model.GroupWeeklyBriefStateGenerating,
		model.GroupWeeklyBriefStateError,
	} {
		t.Run(string(state), func(t *testing.T) {
			brief := shareableBrief(state)
			sharer, sender := makeSharerOrchestrator(brief, &webhookURL, nil)
			err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
				CommitteeUID: "c-share-1",
				Revision:     3,
				Now:          testNow,
			})
			var valErr errors.Validation
			assert.ErrorAs(t, err, &valErr, "expected Validation error for state %s", state)
			assert.Len(t, sender.calls, 0)
		})
	}
}

func TestShareToChat_NoBriefForWindow(t *testing.T) {
	webhookURL := "https://hooks.slack.example.org/services/TXXXXXXXX/BXXXXXXXX/placeholder"
	sharer, _ := makeSharerOrchestrator(nil, &webhookURL, nil)

	err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
		CommitteeUID: "c-share-1",
		Revision:     1,
		Now:          testNow,
	})
	var notFound errors.NotFound
	assert.ErrorAs(t, err, &notFound)
}

func TestShareToChat_RevisionMismatch(t *testing.T) {
	webhookURL := "https://hooks.slack.example.org/services/TXXXXXXXX/BXXXXXXXX/placeholder"
	brief := shareableBrief(model.GroupWeeklyBriefStateGenerated) // stored revision = 3
	sharer, sender := makeSharerOrchestrator(brief, &webhookURL, nil)

	err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
		CommitteeUID: "c-share-1",
		Revision:     99, // wrong
		Now:          testNow,
	})
	var revErr errors.RevisionMismatch
	require.ErrorAs(t, err, &revErr)
	assert.EqualValues(t, 3, revErr.Revision)
	assert.Len(t, sender.calls, 0)
}

func TestShareToChat_NoWebhookConfigured(t *testing.T) {
	brief := shareableBrief(model.GroupWeeklyBriefStateGenerated)
	sharer, sender := makeSharerOrchestrator(brief, nil, nil) // nil = no webhook

	err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
		CommitteeUID: "c-share-1",
		Revision:     3,
		Now:          testNow,
	})
	var noHook errors.NoChatWebhook
	assert.ErrorAs(t, err, &noHook)
	assert.Len(t, sender.calls, 0)
}

func TestShareToChat_SlackError(t *testing.T) {
	webhookURL := "https://hooks.slack.example.org/services/TXXXXXXXX/BXXXXXXXX/placeholder"
	brief := shareableBrief(model.GroupWeeklyBriefStateGenerated)
	slackErr := fmt.Errorf("slack webhook returned 400: invalid_payload")
	sharer, sender := makeSharerOrchestrator(brief, &webhookURL, slackErr)

	err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
		CommitteeUID: "c-share-1",
		Revision:     3,
		Now:          testNow,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_payload")
	assert.Len(t, sender.calls, 1)
}

func TestShareToChat_MissingCommitteeUID(t *testing.T) {
	webhookURL := "https://hooks.slack.example.org/services/TXXXXXXXX/BXXXXXXXX/placeholder"
	brief := shareableBrief(model.GroupWeeklyBriefStateGenerated)
	sharer, _ := makeSharerOrchestrator(brief, &webhookURL, nil)

	err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
		CommitteeUID: "",
		Revision:     3,
		Now:          testNow,
	})
	var valErr errors.Validation
	assert.ErrorAs(t, err, &valErr)
}

func TestShareToChat_WebhookURLNotInError(t *testing.T) {
	webhookURL := "https://hooks.slack.example.org/services/TXXXXXXXX/BXXXXXXXX/placeholder"
	brief := shareableBrief(model.GroupWeeklyBriefStateGenerated)
	slackErr := fmt.Errorf("slack webhook returned 400: invalid_payload")
	sharer, _ := makeSharerOrchestrator(brief, &webhookURL, slackErr)

	err := sharer.ShareToChat(context.Background(), GroupWeeklyBriefShareInput{
		CommitteeUID: "c-share-1",
		Revision:     3,
		Now:          testNow,
	})
	require.Error(t, err)
	// The webhook URL must never appear in error messages.
	assert.NotContains(t, err.Error(), webhookURL)
}
