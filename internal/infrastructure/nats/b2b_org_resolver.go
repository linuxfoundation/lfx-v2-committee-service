// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

const b2bOrgLookupNotFoundError = "b2b org not found"

type b2bOrgLookupRequest struct {
	ID string `json:"id"`
}

type b2bOrgLookupResponse struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type b2bOrgResolver struct {
	client *NATSClient
}

var _ port.B2BOrgResolver = (*b2bOrgResolver)(nil)

// NewB2BOrgResolver creates a NATS-backed b2b_org resolver.
func NewB2BOrgResolver(client *NATSClient) port.B2BOrgResolver {
	return &b2bOrgResolver{client: client}
}

// ResolveByUID reports whether uid resolves to a b2b_org via member-service.
func (r *b2bOrgResolver) ResolveByUID(ctx context.Context, uid string) (string, bool, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return "", false, nil
	}

	payload, err := json.Marshal(b2bOrgLookupRequest{ID: uid})
	if err != nil {
		return "", false, fmt.Errorf("marshal b2b_org lookup request: %w", err)
	}

	_, msg, err := r.client.requestWithSpan(ctx, constants.MemberB2BOrgLookupSubject, payload)
	if err != nil {
		return "", false, fmt.Errorf("b2b_org lookup request failed: %w", err)
	}

	var resp b2bOrgLookupResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return "", false, fmt.Errorf("decode b2b_org lookup response: %w", err)
	}
	errMsg := strings.TrimSpace(resp.Error)
	if errMsg != "" {
		if errMsg == b2bOrgLookupNotFoundError {
			return "", false, nil
		}
		return "", false, fmt.Errorf("b2b_org lookup: %s", errMsg)
	}
	if strings.TrimSpace(resp.ID) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(resp.ID), true, nil
}
