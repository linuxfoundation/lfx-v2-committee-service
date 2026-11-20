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
	source, err := auth.NewAuditAuthorizationSource(os.Getenv("COMMITTEE_AUDIT_TOKEN_FILE"),
		os.Getenv("COMMITTEE_AUDIT_PRINCIPAL"), os.Getenv("COMMITTEE_AUDIT_AUDIENCE"), os.Getenv("JWKS_URL"))
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
