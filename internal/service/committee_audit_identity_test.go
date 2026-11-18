// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestCaptureCommitteeAuditIdentity(t *testing.T) {
	for _, tc := range []struct {
		name             string
		identity         *model.AuthenticatedIdentity
		wantError        bool
		actor, effective string
		actorEvidence    bool
	}{
		{"normal", &model.AuthenticatedIdentity{Principal: "first-last"}, false, "user:first-last", "user:first-last", true},
		{"impersonation", &model.AuthenticatedIdentity{Principal: "target-user", Actor: &model.AuthenticatedActor{Subject: "auth0|original-user", Username: "first-last"}}, false, "user:first-last", "user:target-user", false},
		{"equal refs", &model.AuthenticatedIdentity{Principal: "first-last", Actor: &model.AuthenticatedActor{Subject: "auth0|original-user", Username: "first-last"}}, false, "user:first-last", "user:first-last", true},
		{"machine", &model.AuthenticatedIdentity{Principal: "test-client@clients"}, false, "service:test-client", "service:test-client", true},
		{"anonymous", &model.AuthenticatedIdentity{Principal: "_anonymous"}, true, "", "", false},
		{"missing", nil, true, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, err := CaptureCommitteeAuditIdentity(context.Background(), tc.identity, "test.signed.evidence")
			if tc.wantError {
				require.Error(t, err)
				require.Nil(t, ctx.Value(constants.AuditAttributionContextID))
				return
			}
			require.NoError(t, err)
			got := ctx.Value(constants.AuditAttributionContextID).(indexertypes.AuditAttribution)
			require.Equal(t, tc.actor, got.ActorRef)
			require.Equal(t, tc.effective, got.EffectiveActorRef)
			require.Equal(t, "test.signed.evidence", got.EffectiveActorJWT)
			if tc.actorEvidence {
				require.Equal(t, got.EffectiveActorJWT, got.ActorJWT)
			} else {
				require.Empty(t, got.ActorJWT)
			}
			payload, err := json.Marshal(got)
			require.NoError(t, err)
			for _, forbidden := range []string{"actor_profile", "actor_identity", "actor_verified", "name", "avatar_url", "email"} {
				require.NotContains(t, string(payload), `"`+forbidden+`"`)
			}
		})
	}
}
