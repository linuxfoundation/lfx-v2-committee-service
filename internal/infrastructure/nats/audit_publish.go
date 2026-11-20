// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/nats-io/nats.go/jetstream"
)

// PublishCommitteeAuditEvent attaches fresh service authorization to a dispatch
// copy and waits for JetStream PubAck. It never uses core request/reply or treats
// local buffering as durable delivery. PubAck is not indexer/history completion.
// Callers obtain the credential at dispatch, not from retained request headers.
func (c *NATSClient) PublishCommitteeAuditEvent(ctx context.Context, kind model.ResourceType, message *indexertypes.MessageV2, authorization string) error {
	if len(authorization) > model.CommitteeAuditMaxAuthorizationBytes {
		return errors.NewValidation("audit authorization exceeds dispatch limit")
	}
	var subject string
	switch kind {
	case model.ResourceCommittee:
		subject = constants.IndexCommitteeSubject
	case model.ResourceCommitteeSettings:
		subject = constants.IndexCommitteeSettingsSubject
	case model.ResourceCommitteeMember:
		subject = constants.IndexCommitteeMemberSubject
	default:
		return errors.NewValidation("unsupported committee audit subject")
	}
	if err := c.IsReady(ctx); err != nil {
		return err
	}
	envelope, err := message.Envelope(authorization, model.ValidateCommitteeAuditContent)
	if err != nil {
		return errors.NewValidation("invalid committee audit dispatch envelope")
	}
	if err := model.ValidateCommitteeAuditEnvelope(envelope, kind); err != nil {
		return errors.NewValidation("invalid committee audit dispatch context")
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return errors.NewValidation("invalid committee audit dispatch payload")
	}
	js, err := jetstream.New(c.conn)
	if err != nil {
		return errors.NewServiceUnavailable("failed to create audit JetStream publisher", auditDispatchCause{err})
	}
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()
	// Do not use the event ID as a broker deduplication-window receipt. An
	// explicit repair may need the same logical event delivered to the consumer
	// again; the indexer's immutable identity check handles duplicate deliveries.
	if _, err := js.Publish(ctx, subject, payload); err != nil {
		return errors.NewServiceUnavailable("committee audit broker delivery failed", auditDispatchCause{err})
	}
	return nil
}

// auditDispatchCause retains inspectable causes without printing external error
// text, which can contain credentials or event payloads. The relay also logs only
// its own generic failure message, never the underlying cause.
type auditDispatchCause struct{ err error }

func (auditDispatchCause) Error() string   { return "audit dispatch upstream failure" }
func (e auditDispatchCause) Unwrap() error { return e.err }
