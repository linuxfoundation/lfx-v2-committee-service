// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"net/url"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/stretchr/testify/require"
)

// Update validates a changed charter only after reading the stored committee; see
// TestCommitteeWriterOrchestrator_Update_CharterURLValidation.
func TestCommitteeWriter_RejectsCharterCredentialsBeforeIO(t *testing.T) {
	writer := &committeeWriterOrchestrator{}
	legacyURL := (&url.URL{Scheme: "https", Host: "example.com", Path: "/charter", User: url.UserPassword("first-last", "secret-token")}).String()
	committee := &model.Committee{CommitteeBase: model.CommitteeBase{Charter: &model.Charter{URL: legacyURL}}}
	_, err := writer.Create(context.Background(), committee, false)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-token")
}
