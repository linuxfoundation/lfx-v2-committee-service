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

type b2bOrgLookupByWebsiteRequest struct {
	Name    string `json:"name"`
	Website string `json:"website"`
}

type b2bOrgLookupByWebsiteResponse struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type b2bOrgFallbackResolver struct {
	client *NATSClient
}

var _ port.B2BOrgFallbackResolver = (*b2bOrgFallbackResolver)(nil)

// NewB2BOrgFallbackResolver creates a NATS-backed b2b_org name/website resolver,
// consulted when b2bOrgResolver.ResolveByUID cannot resolve organization.id directly.
func NewB2BOrgFallbackResolver(client *NATSClient) port.B2BOrgFallbackResolver {
	return &b2bOrgFallbackResolver{client: client}
}

// ResolveSFID resolves a b2b_org SFID by name/website via member-service.
func (r *b2bOrgFallbackResolver) ResolveSFID(ctx context.Context, name, website string) (string, bool, error) {
	name = strings.TrimSpace(name)
	website = strings.TrimSpace(website)
	if name == "" && website == "" {
		return "", false, nil
	}

	payload, err := json.Marshal(b2bOrgLookupByWebsiteRequest{Name: name, Website: website})
	if err != nil {
		return "", false, fmt.Errorf("marshal b2b_org lookup by website request: %w", err)
	}

	_, msg, err := r.client.requestWithSpan(ctx, constants.MemberB2BOrgLookupByWebsiteSubject, payload)
	if err != nil {
		return "", false, fmt.Errorf("b2b_org lookup by website request failed: %w", err)
	}

	var resp b2bOrgLookupByWebsiteResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return "", false, fmt.Errorf("decode b2b_org lookup by website response: %w", err)
	}
	errMsg := strings.TrimSpace(resp.Error)
	if errMsg != "" {
		if errMsg == b2bOrgLookupNotFoundError {
			return "", false, nil
		}
		return "", false, fmt.Errorf("b2b_org lookup by website: %s", errMsg)
	}
	if strings.TrimSpace(resp.ID) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(resp.ID), true, nil
}
