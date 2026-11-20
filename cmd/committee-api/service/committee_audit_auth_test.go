// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/require"
)

type auditBoundaryAuth struct {
	port.Authenticator
	fail bool
}

func (a *auditBoundaryAuth) ParseIdentity(context.Context, string, *slog.Logger) (*model.AuthenticatedIdentity, error) {
	if a.fail {
		return nil, fmt.Errorf("invalid signature")
	}
	return &model.AuthenticatedIdentity{Principal: "target-user", Actor: &model.AuthenticatedActor{Subject: "auth0|original-user", Username: "first-last"}}, nil
}

func TestJWTAuth_AuditContext(t *testing.T) {
	t.Setenv("COMMITTEE_AUDIT_ENABLED", "true")
	t.Run("authenticated read does not retain actor evidence", func(t *testing.T) {
		svc := &committeeServicesrvc{auth: &auditBoundaryAuth{}}
		ctx := context.WithValue(context.Background(), constants.ReadOnlyRequestContextID, true)
		ctx, err := svc.JWTAuth(ctx, "verified-by-authenticator", nil)
		require.NoError(t, err)
		require.Equal(t, "target-user", ctx.Value(constants.PrincipalContextID))
		require.Nil(t, ctx.Value(constants.AuditAttributionContextID))
	})
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			svc := &committeeServicesrvc{auth: &auditBoundaryAuth{fail: fail}}
			ctx, err := svc.JWTAuth(context.Background(), "verified-by-authenticator", nil)
			if fail {
				require.Error(t, err)
				require.Nil(t, ctx.Value(constants.AuditAttributionContextID))
				return
			}
			require.NoError(t, err)
			require.Equal(t, "target-user", ctx.Value(constants.PrincipalContextID))
			attribution := ctx.Value(constants.AuditAttributionContextID).(indexertypes.AuditAttribution)
			require.Equal(t, "user:first-last", attribution.ActorRef)
			require.Equal(t, "user:target-user", attribution.EffectiveActorRef)
			require.Empty(t, attribution.ActorJWT)
			require.Equal(t, "verified-by-authenticator", attribution.EffectiveActorJWT)
		})
	}
}
