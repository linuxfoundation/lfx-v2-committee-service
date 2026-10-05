// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func newSettingsRC(r *mockReader, pub *mockPublisher, args ...string) commands.RunContext {
	return commands.RunContext{
		CommitteeReader: r,
		Publisher:       pub,
		Args:            args,
	}
}

func TestReindexSettings_PublishesWithPublicFalse(t *testing.T) {
	webhookURL := "https://hooks.example.com/secret"
	r := &mockReader{
		uids: []string{"c1"},
		bases: map[string]*model.CommitteeBase{
			"c1": {UID: "c1", ProjectUID: "proj-1", Public: true},
		},
		settings: map[string]*model.CommitteeSettings{
			"c1": {UID: "c1", BusinessEmailRequired: true, ChatWebhookURL: &webhookURL},
		},
	}
	pub := &mockPublisher{}

	err := (&reindexSettingsSubcommand{}).Run(context.Background(), newSettingsRC(r, pub))
	require.NoError(t, err)
	require.Equal(t, 1, pub.indexerCalls)

	msg, ok := pub.indexerMsgs[0].(*model.CommitteeIndexerMessage)
	require.True(t, ok, "expected *model.CommitteeIndexerMessage")

	require.NotNil(t, msg.IndexingConfig, "IndexingConfig must be set")
	require.NotNil(t, msg.IndexingConfig.Public, "Public must be explicitly set (not nil)")
	assert.False(t, *msg.IndexingConfig.Public, "settings must never be indexed as public")
	assert.Equal(t, "auditor", msg.IndexingConfig.AccessCheckRelation)

	data, ok := msg.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")
	assert.Nil(t, data["chat_webhook_url"], "chat_webhook_url must be stripped from the indexer payload")
}

func TestReindexSettings_DryRun_NoPublish(t *testing.T) {
	webhookURL := "https://hooks.example.com/secret"
	r := &mockReader{
		uids: []string{"c1"},
		bases: map[string]*model.CommitteeBase{
			"c1": {UID: "c1", ProjectUID: "proj-1", Public: true},
		},
		settings: map[string]*model.CommitteeSettings{
			"c1": {UID: "c1", ChatWebhookURL: &webhookURL},
		},
	}
	pub := &mockPublisher{}

	err := (&reindexSettingsSubcommand{}).Run(context.Background(), newSettingsRC(r, pub, "--dry-run"))
	require.NoError(t, err)
	assert.Equal(t, 0, pub.indexerCalls, "dry-run must not publish")
}

func TestReindexSettings_NoSettings_CountedAsSkipped(t *testing.T) {
	r := &mockReader{
		uids: []string{"c1"},
		bases: map[string]*model.CommitteeBase{
			"c1": {UID: "c1", ProjectUID: "proj-1"},
		},
		settingsErr: map[string]error{
			"c1": errs.NewNotFound("committee settings not found"),
		},
	}
	pub := &mockPublisher{}

	err := (&reindexSettingsSubcommand{}).Run(context.Background(), newSettingsRC(r, pub))
	require.NoError(t, err, "NotFound settings must not fail the run")
	assert.Equal(t, 0, pub.indexerCalls)
}

func TestReindexSettings_BaseFetchFails_CountedAsFailed(t *testing.T) {
	r := &mockReader{
		uids: []string{"c1"},
		baseErr: map[string]error{
			"c1": errs.NewUnexpected("nats timeout"),
		},
	}
	pub := &mockPublisher{}

	err := (&reindexSettingsSubcommand{}).Run(context.Background(), newSettingsRC(r, pub))
	require.Error(t, err, "base fetch failure must propagate as non-zero exit")
	assert.Equal(t, 0, pub.indexerCalls)
}

func TestReindexSettings_SingleUID_Flag(t *testing.T) {
	r := &mockReader{
		bases: map[string]*model.CommitteeBase{
			"c1": {UID: "c1", ProjectUID: "proj-1"},
			"c2": {UID: "c2", ProjectUID: "proj-2"},
		},
		settings: map[string]*model.CommitteeSettings{
			"c1": {UID: "c1"},
			"c2": {UID: "c2"},
		},
	}
	pub := &mockPublisher{}

	err := (&reindexSettingsSubcommand{}).Run(context.Background(), newSettingsRC(r, pub, "--committee-uid", "c1"))
	require.NoError(t, err)
	assert.Equal(t, 1, pub.indexerCalls, "only c1 should be published")

	// Confirm the published config targets c1
	msg := pub.indexerMsgs[0].(*model.CommitteeIndexerMessage)
	assert.Equal(t, &indexerTypes.IndexingConfig{
		ObjectID:             "c1",
		AccessCheckObject:    "committee_settings:c1",
		AccessCheckRelation:  "auditor",
		HistoryCheckObject:   "committee_settings:c1",
		HistoryCheckRelation: "auditor",
		Tags:                 msg.IndexingConfig.Tags, // tags vary; just confirm they're present
		Public:               msg.IndexingConfig.Public,
	}, msg.IndexingConfig)
	assert.False(t, *msg.IndexingConfig.Public)
}
