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
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/auth"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

type messageRequest struct {
	client *NATSClient
}

func (m *messageRequest) get(ctx context.Context, subject, uid string) (string, error) {

	data := []byte(uid)
	_, msg, err := m.client.requestWithSpan(ctx, subject, data)
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

// Writers retrieves the writers list for the given project UID via a NATS request.
// Returns an empty slice when the project has no writers configured.
func (m *messageRequest) Writers(ctx context.Context, uid string) ([]model.CommitteeUser, error) {
	_, msg, err := m.client.requestWithSpan(ctx, constants.ProjectGetWritersSubject, []byte(uid))
	if err != nil {
		return nil, fmt.Errorf("get_writers request failed for project %s: %w", uid, err)
	}

	var writers []model.CommitteeUser
	if err := json.Unmarshal(msg.Data, &writers); err != nil {
		return nil, fmt.Errorf("failed to decode get_writers response: %w", err)
	}

	return writers, nil
}

// UsernameByEmail resolves the registered LFID username for the given primary email address.
// The auth service replies with a plain-text username on success, or a JSON error envelope on miss.
func (m *messageRequest) UsernameByEmail(ctx context.Context, email string) (string, error) {
	data := []byte(email)
	_, msg, err := m.client.requestWithSpan(ctx, constants.AuthEmailToUsernameLookupSubject, data)
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

// EmailsByAuthToken retrieves all email addresses for a user by sending their Auth0 subject
// (auth0|{userID}) as auth_token to the NATS subject lfx.auth-service.user_emails.read.
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

	_, msg, err := m.client.requestWithSpan(ctx, constants.AuthUserEmailsReadSubject, payload)
	if err != nil {
		return nil, errors.NewServiceUnavailable("auth service unavailable", err)
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
//
// A bare LFID username is mapped to its deterministic "auth0|" sub before the request so auth-service
// resolves it with a cheap get-by-id rather than a rate-limited Auth0 user search; already-qualified
// principals pass through unchanged. Returned errors always redact the original principal, never the
// derived sub. A genuine "no such user" reply (see isUserMissError) is a NotFound miss; other failures
// surface as Unexpected so callers can tell an auth-service outage apart from an absent user.
func (m *messageRequest) UserMetadataByPrincipal(ctx context.Context, principal string) (*model.UserMetadata, error) {
	_, msg, err := m.client.requestWithSpan(ctx, constants.AuthUserMetadataReadSubject, []byte(auth.AuthSubLookupKey(principal)))
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(string(msg.Data)) == "" {
		return nil, errors.NewNotFound(fmt.Sprintf("user metadata not found for principal: %s", redaction.Redact(principal)))
	}

	var response UserMetadataNATSResponse
	if err := json.Unmarshal(msg.Data, &response); err != nil {
		return nil, errors.NewUnexpected("failed to parse user_metadata response", err)
	}

	if !response.Success || response.Data == nil {
		if response.Error != "" && !isUserMissError(response.Error) {
			return nil, errors.NewUnexpected(fmt.Sprintf("user metadata lookup failed for principal %s: %s", redaction.Redact(principal), response.Error))
		}
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
