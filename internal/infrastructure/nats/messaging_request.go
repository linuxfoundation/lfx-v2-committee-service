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

func (m *messageRequest) Slug(ctx context.Context, uid string) (string, error) {
	return m.get(ctx, constants.ProjectGetSlugSubject, uid)
}

func (m *messageRequest) Name(ctx context.Context, uid string) (string, error) {
	return m.get(ctx, constants.ProjectGetNameSubject, uid)
}

func (m *messageRequest) SubByEmail(ctx context.Context, email string) (string, error) {
	data := []byte(email)
	msg, err := m.client.conn.RequestWithContext(ctx, constants.AuthEmailToSubLookupSubject, data)
	if err != nil {
		return "", err
	}

	sub := string(msg.Data)
	if sub == "" {
		return "", errors.NewNotFound(fmt.Sprintf("user sub not found for email: %s", redaction.RedactEmail(email)))
	}

	return sub, nil
}

func NewMessageRequest(client *NATSClient) port.ProjectReader {
	return &messageRequest{
		client: client,
	}
}

func NewUserRequest(client *NATSClient) port.UserReader {
	return &messageRequest{
		client: client,
	}
}
