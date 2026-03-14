// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"fmt"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

type messageRequest struct {
	client *NATSClient
}

func (m *messageRequest) get(ctx context.Context, subject, uid string) (string, error) {

	data := []byte(uid)
	msg, err := m.client.conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return "", err
	}

	attribute := string(msg.Data)
	if attribute == "" {
		return "", errors.NewNotFound(fmt.Sprintf("project attribute %s not found for uid: %s", subject, uid))
	}

	return attribute, nil

}

// Slug retrieves the project slug for the given project UID via a NATS request.
func (m *messageRequest) Slug(ctx context.Context, uid string) (string, error) {
	return m.get(ctx, constants.ProjectGetSlugSubject, uid)
}

// Name retrieves the project name for the given project UID via a NATS request.
func (m *messageRequest) Name(ctx context.Context, uid string) (string, error) {
	return m.get(ctx, constants.ProjectGetNameSubject, uid)
}

// SubByEmail retrieves a user's sub (subject identifier) for the given email address via a NATS request.
func (m *messageRequest) SubByEmail(ctx context.Context, email string) (string, error) {

	data := []byte(email)
	msg, err := m.client.conn.RequestWithContext(ctx, constants.AuthEmailToSubLookupSubject, data)
	if err != nil {
		return "", err
	}

	response := string(msg.Data)
	if response == "" {
		return "", errors.NewNotFound(fmt.Sprintf("user sub not found for email: %s", redaction.RedactEmail(email)))
	}

	// handling errors if exists
	var errorMessage ErrorMessageNATSResponse
	if err := errorMessage.CheckError(response); err != nil {
		return "", err
	}

	return response, nil
}

// NewMessageRequest creates a new NATS-backed ProjectReader for retrieving project attributes.
func NewMessageRequest(client *NATSClient) port.ProjectReader {
	return &messageRequest{
		client: client,
	}
}

// NewUserRequest creates a new NATS-backed UserReader for looking up user attributes.
func NewUserRequest(client *NATSClient) port.UserReader {
	return &messageRequest{
		client: client,
	}
}
