// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// CaptureCommitteeAuditIdentity retains exact verified principals and optional
// Heimdall evidence. Identity lookup and verification flags belong to the indexer.
// The caller must already have verified evidence through its authenticator.
func CaptureCommitteeAuditIdentity(ctx context.Context, identity *model.AuthenticatedIdentity, evidence string) (context.Context, error) {
	if identity == nil || identity.Principal == "" || identity.Principal == "_anonymous" {
		return ctx, errors.NewForbidden("authenticated audit identity is required")
	}
	ref := "user:" + identity.Principal
	if strings.HasSuffix(identity.Principal, "@clients") {
		if identity.Actor != nil {
			return ctx, errors.NewForbidden("delegated service audit identity is unsupported")
		}
		ref = "service:" + strings.TrimSuffix(identity.Principal, "@clients")
	}
	effective := indexertypes.AuditActor{Ref: ref, JWT: evidence}
	actor := effective
	if identity.Actor != nil && identity.Actor.Username != identity.Principal {
		// The target's token attests act, but its principal is still the target.
		// It is not a matching original-actor JWT under the indexer contract.
		actor = indexertypes.AuditActor{Ref: "user:" + identity.Actor.Username}
	}
	attribution, err := indexertypes.NewAuditAttribution(actor, &effective)
	if err != nil {
		return ctx, errors.NewForbidden("invalid verified audit identity")
	}
	return model.WithAuditAttribution(ctx, attribution)
}
