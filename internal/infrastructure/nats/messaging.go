// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"fmt"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

type message struct {
	client *NATSClient
}

func (m *message) get(ctx context.Context, subject, uid string) (string, error) {

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

func (m *message) Slug(ctx context.Context, uid string) (string, error) {
	return m.get(ctx, constants.ProjectGetSlugSubject, uid)
}

func (m *message) Name(ctx context.Context, uid string) (string, error) {
	return m.get(ctx, constants.ProjectGetNameSubject, uid)
}

func NewMessage(client *NATSClient) port.ProjectReader {
	return &message{
		client: client,
	}
}
