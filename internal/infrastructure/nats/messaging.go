// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

type message struct {
	client *NATSClient
}

func (m *message) Slug(ctx context.Context, uid string) (string, error) {

	// You can customize the request payload as needed. Here, we just send the uid.
	data := []byte(uid)

	// Use the NATS client to send a request and wait for a reply.
	msg, err := m.client.conn.RequestWithContext(ctx, constants.ProjectGetSlugSubject, data)
	if err != nil {
		return "", err
	}

	slug := string(msg.Data)
	if slug == "" {
		return "", errors.NewNotFound("project slug not found for uid: " + uid)
	}

	return slug, nil

}

func NewMessage(client *NATSClient) port.ProjectReader {
	return &message{
		client: client,
	}
}
