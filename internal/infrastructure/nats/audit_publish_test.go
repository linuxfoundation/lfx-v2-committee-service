// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func auditPublishFixture(t *testing.T) *indexertypes.MessageV2 {
	t.Helper()
	base := model.CommitteeBase{UID: "committee-1", ProjectUID: "project-1", Name: "Group"}
	snapshot, err := model.SnapshotCommittee(base)
	require.NoError(t, err)
	message, err := model.NewCommitteeAuditMutation(model.CommitteeAuditMutation{
		ObjectType: model.ResourceCommittee, EventID: "event-1", OccurredAt: time.Now().UTC(), Revision: 1,
		Action: model.ActionCreated, Data: base, After: snapshot,
		Attribution:    indexertypes.AuditAttribution{ActorRef: "service:importer", EffectiveActorRef: "service:importer"},
		Scope:          indexertypes.AuditScope{After: []string{"project:project-1", "committee:committee-1"}},
		IndexingConfig: base.IndexingConfig(),
	})
	require.NoError(t, err)
	return message
}

func TestAuditDispatchCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, stderrors.New("sensitive upstream detail: secret-token")} {
		err := errs.NewServiceUnavailable("audit delivery failed", auditDispatchCause{cause})
		require.True(t, stderrors.Is(err, cause))
		var unavailable errs.ServiceUnavailable
		require.True(t, stderrors.As(err, &unavailable))
		require.NotContains(t, err.Error(), "secret-token")
	}
}

func TestNATSClient_PublishCommitteeAuditEvent(t *testing.T) {
	ctx := context.Background()
	js := startJetStreamTestServer(t)
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "audit-publish-test", Subjects: []string{constants.IndexCommitteeSubject, constants.IndexCommitteeMemberSubject}})
	require.NoError(t, err)
	client := &NATSClient{conn: js.Conn()}
	message := auditPublishFixture(t)
	frozen, err := json.Marshal(message)
	require.NoError(t, err)
	for i, token := range []string{"Bearer fresh-service-one", "Bearer fresh-service-two"} {
		require.NoError(t, client.PublishCommitteeAuditEvent(ctx, model.ResourceCommittee, message, token))
		stored, err := stream.GetMsg(ctx, uint64(i+1))
		require.NoError(t, err, "successful delivery must already be durably readable from the stream")
		envelope, err := indexertypes.DecodeV2Envelope(stored.Data, "committee", model.ValidateCommitteeAuditContent)
		require.NoError(t, err)
		assert.Equal(t, token, envelope.Headers[constants.AuthorizationHeader])
		assert.Equal(t, "event-1", envelope.Audit.EventID)
		assert.Equal(t, "service:importer", envelope.Audit.Attribution.ActorRef)
	}
	again, err := json.Marshal(message)
	require.NoError(t, err)
	assert.Equal(t, frozen, again)
	assert.NotContains(t, string(again), "fresh-service")

	for _, tc := range []struct {
		name  string
		kind  model.ResourceType
		token string
	}{
		{"missing auth", model.ResourceCommittee, ""},
		{"unsupported subject", model.ResourceCommitteeDocument, "Bearer service"},
		{"wrong resource subject", model.ResourceCommitteeMember, "Bearer service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := client.PublishCommitteeAuditEvent(ctx, tc.kind, message, tc.token)
			require.Error(t, err)
			if tc.kind == model.ResourceCommitteeMember {
				require.EqualError(t, err, "invalid committee audit dispatch context")
			}
		})
	}
	info, err := stream.Info(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 2, info.State.Msgs)

	t.Run("no matching stream is not successful delivery", func(t *testing.T) {
		require.NoError(t, js.DeleteStream(ctx, "audit-publish-test"))
		bounded, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		err := client.PublishCommitteeAuditEvent(bounded, model.ResourceCommittee, message, "Bearer secret-token")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret-token")
	})
}
