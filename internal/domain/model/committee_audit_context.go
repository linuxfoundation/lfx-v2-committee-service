// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// AuthenticatedIdentity is populated only from a successfully verified token.
// Principal remains the effective identity used for authorization.
type AuthenticatedIdentity struct {
	Principal string
	Email     string
	Actor     *AuthenticatedActor
	ExpiresAt time.Time
}

// AuthenticatedActor is the original actor attested by Heimdall's signed act claim.
type AuthenticatedActor struct {
	Subject  string `json:"sub"`
	Username string `json:"username"`
}

// WithCommitteeSystemActor identifies trusted background mutations explicitly.
// It must only be used at internal message/CLI boundaries, never as an HTTP fallback.
func WithCommitteeSystemActor(ctx context.Context) context.Context {
	return context.WithValue(ctx, constants.AuditAttributionContextID, indexertypes.AuditAttribution{
		ActorRef: "service:committee-service", EffectiveActorRef: "service:committee-service",
	})
}

// WithAuditAttribution captures refs and optional evidence from trusted boundaries.
// It does not infer a service identity from missing authentication.
func WithAuditAttribution(ctx context.Context, attribution indexertypes.AuditAttribution) (context.Context, error) {
	captured, err := indexertypes.NewAuditAttribution(
		indexertypes.AuditActor{Ref: attribution.ActorRef, JWT: attribution.ActorJWT},
		&indexertypes.AuditActor{Ref: attribution.EffectiveActorRef, JWT: attribution.EffectiveActorJWT})
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, constants.AuditAttributionContextID, captured), nil
}

// WithCommitteeProjection marks a trusted derived-data refresh, not a business edit.
// It must never be selected from caller-controlled HTTP headers or payload fields.
func WithCommitteeProjection(ctx context.Context) context.Context {
	return context.WithValue(ctx, constants.AuditProjectionContextID, true)
}

// WithAuditServiceActor explicitly attributes trusted background source changes
// to a service. Callers must identify the actual system operation, not a user
// copied from an unrelated request.
func WithAuditServiceActor(ctx context.Context, service string) (context.Context, error) {
	ref := "service:" + service
	return WithAuditAttribution(ctx, indexertypes.AuditAttribution{ActorRef: ref, EffectiveActorRef: ref})
}
