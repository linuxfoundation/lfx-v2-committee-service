// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"os"
	"sync"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/auth"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	usecase "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// StartCommitteeAuditRelay joins the API's cancellation/waitgroup lifecycle.
// Capture and dispatch are separate gates so rollout can durably hold events.
func StartCommitteeAuditRelay(ctx context.Context, wg *sync.WaitGroup) error {
	flag := os.Getenv("COMMITTEE_AUDIT_DISPATCH_ENABLED")
	if flag != "" && flag != "false" && flag != "true" {
		return errs.NewValidation("COMMITTEE_AUDIT_DISPATCH_ENABLED must be true or false")
	}
	enabled, err := postgres.AuditEnabled(os.Getenv)
	if err != nil {
		return err
	}
	if !enabled && flag != "true" {
		return nil
	}
	if postgresPool == nil || natsClient == nil {
		return errs.NewValidation("audit maintenance requires PostgreSQL storage and NATS")
	}
	retention, err := postgres.AuditRetentionFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	if flag == "" || flag == "false" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			usecase.CommitteeAuditRetention(ctx, postgres.NewStore(postgresPool), retention)
		}()
		return nil
	}
	source, err := committeeAuditCredentialSource(os.Getenv)
	if err != nil {
		return err
	}
	// Static configuration is validated above without reading a short-lived
	// credential or contacting JWKS. Transient credential failures pause only
	// dispatch: retained events retry with fresh evidence on each attempt.
	deliver := func(ctx context.Context, kind model.ResourceType, message *indexertypes.MessageV2) error {
		authorization, err := source.Authorization(ctx)
		if err != nil {
			return err
		}
		return natsClient.PublishCommitteeAuditEvent(ctx, kind, message, authorization)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		usecase.CommitteeAuditRelay(ctx, postgres.NewStore(postgresPool), deliver)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		usecase.CommitteeAuditRetention(ctx, postgres.NewStore(postgresPool), retention)
	}()
	return nil
}

// committeeAuditCredential supplies the relay's service bearer for each delivery.
type committeeAuditCredential interface {
	Authorization(ctx context.Context) (string, error)
}

// committeeAuditCredentialSource selects how the relay authenticates: a token
// file refreshed externally (COMMITTEE_AUDIT_TOKEN_FILE), or tokens minted for
// COMMITTEE_AUDIT_PRINCIPAL with the Heimdall signing key
// (COMMITTEE_AUDIT_SIGNER_KEY_FILE, optional COMMITTEE_AUDIT_SIGNER_KEY_ID).
// Exactly one must be configured.
func committeeAuditCredentialSource(getenv func(string) string) (committeeAuditCredential, error) {
	tokenFile, signerKeyFile := getenv("COMMITTEE_AUDIT_TOKEN_FILE"), getenv("COMMITTEE_AUDIT_SIGNER_KEY_FILE")
	principal, audience, jwksURL := getenv("COMMITTEE_AUDIT_PRINCIPAL"), getenv("COMMITTEE_AUDIT_AUDIENCE"), getenv("JWKS_URL")
	switch {
	case tokenFile != "" && signerKeyFile != "":
		return nil, errs.NewValidation("set only one of COMMITTEE_AUDIT_TOKEN_FILE and COMMITTEE_AUDIT_SIGNER_KEY_FILE")
	case signerKeyFile != "":
		return auth.NewAuditSigningSource(signerKeyFile, principal, audience, jwksURL, getenv("COMMITTEE_AUDIT_SIGNER_KEY_ID"))
	default:
		return auth.NewAuditAuthorizationSource(tokenFile, principal, audience, jwksURL)
	}
}
