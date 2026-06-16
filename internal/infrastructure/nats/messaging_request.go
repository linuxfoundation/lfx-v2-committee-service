// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// UsernameByEmail resolves the registered LFID username for the given primary email address.
// The auth service replies with a plain-text username on success, or a JSON error envelope on miss.
func (m *messageRequest) UsernameByEmail(ctx context.Context, email string) (string, error) {
	msg, err := m.client.conn.RequestWithContext(ctx, constants.AuthEmailToUsernameLookupSubject, []byte(email))
	if err != nil {
		return "", fmt.Errorf("email_to_username request failed: %w", err)
	}

	body := strings.TrimSpace(string(msg.Data))
	if body == "" {
		return "", errors.NewNotFound(fmt.Sprintf("user not found for email: %s", redaction.RedactEmail(email)))
	}

	// Auth-service error responses are JSON objects; success replies are plain-text usernames.
	if body[0] == '{' {
		var errorMessage ErrorMessageNATSResponse
		if err := errorMessage.CheckError(body); err != nil {
			return "", err
		}
		return "", errors.NewUnexpected("unexpected email_to_username success envelope")
	}

	return body, nil
}

// EmailsByAuthToken retrieves all email addresses for a user by sending their bearer token
// (without the "Bearer " prefix) to the NATS subject lfx.auth-service.user_emails.read.
func (m *messageRequest) EmailsByAuthToken(ctx context.Context, authToken string) (*model.UserEmails, error) {
	if authToken == "" {
		return nil, errors.NewValidation("auth token must not be empty")
	}
	req := UserEmailsNATSRequest{
		User: UserEmailsNATSRequestUser{AuthToken: authToken},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, errors.NewUnexpected("failed to marshal user_emails request", err)
	}

	msg, err := m.client.conn.RequestWithContext(ctx, constants.AuthUserEmailsReadSubject, payload)
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
		return nil, errors.NewNotFound(fmt.Sprintf("user emails not found: %s", errMsg))
	}

	if response.Data == nil {
		return nil, errors.NewNotFound("no email data returned for user")
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

// UserMetadataByPrincipal retrieves profile metadata for a user from the auth service by principal.
func (m *messageRequest) UserMetadataByPrincipal(ctx context.Context, principal string) (*model.UserMetadata, error) {
	msg, err := m.client.conn.RequestWithContext(ctx, constants.AuthUserMetadataReadSubject, []byte(principal))
	if err != nil {
		return nil, err
	}

	var response UserMetadataNATSResponse
	if err := json.Unmarshal(msg.Data, &response); err != nil {
		return nil, errors.NewUnexpected("failed to parse user_metadata response", err)
	}

	if !response.Success || response.Data == nil {
		return nil, errors.NewNotFound(fmt.Sprintf("user metadata not found for principal: %s", redaction.Redact(principal)))
	}

	d := response.Data
	result := &model.UserMetadata{}
	if d.Picture != nil {
		result.Picture = *d.Picture
	}
	if d.Zoneinfo != nil {
		result.Zoneinfo = *d.Zoneinfo
	}
	if d.Name != nil {
		result.Name = *d.Name
	}
	if d.GivenName != nil {
		result.GivenName = *d.GivenName
	}
	if d.FamilyName != nil {
		result.FamilyName = *d.FamilyName
	}
	if d.JobTitle != nil {
		result.JobTitle = *d.JobTitle
	}
	if d.Organization != nil {
		result.Organization = *d.Organization
	}
	if d.Country != nil {
		result.Country = *d.Country
	}
	if d.StateProvince != nil {
		result.StateProvince = *d.StateProvince
	}
	if d.City != nil {
		result.City = *d.City
	}
	if d.Address != nil {
		result.Address = *d.Address
	}
	if d.PostalCode != nil {
		result.PostalCode = *d.PostalCode
	}
	if d.PhoneNumber != nil {
		result.PhoneNumber = *d.PhoneNumber
	}
	if d.TShirtSize != nil {
		result.TShirtSize = *d.TShirtSize
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
