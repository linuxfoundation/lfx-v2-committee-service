// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
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

// EmailsByPrincipal retrieves all email addresses for a user by sending their principal
// to the NATS subject lfx.auth-service.user_emails.read.
func (m *messageRequest) EmailsByPrincipal(ctx context.Context, principal string) (*model.UserEmails, error) {
	msg, err := m.client.conn.RequestWithContext(ctx, constants.AuthUserEmailsReadSubject, []byte(principal))
	if err != nil {
		return nil, err
	}

	var response UserEmailsNATSResponse
	if err := json.Unmarshal(msg.Data, &response); err != nil {
		return nil, errors.NewUnexpected("failed to parse user_emails response", err)
	}

	if !response.Success {
		errMsg := response.Error
		if errMsg == "" {
			errMsg = "user not found"
		}
		return nil, errors.NewNotFound(fmt.Sprintf("user emails not found for principal %s: %s", redaction.Redact(principal), errMsg))
	}

	if response.Data == nil {
		return nil, errors.NewNotFound(fmt.Sprintf("no email data returned for principal: %s", redaction.Redact(principal)))
	}

	result := &model.UserEmails{
		PrimaryEmail: response.Data.PrimaryEmail,
	}
	for _, alt := range response.Data.AlternateEmails {
		result.AlternateEmails = append(result.AlternateEmails, model.AlternateEmail{
			Email:    alt.Email,
			Verified: alt.Verified,
		})
	}

	return result, nil
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
