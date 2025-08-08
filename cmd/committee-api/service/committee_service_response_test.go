// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"
	"time"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func TestConvertPayloadToDomain(t *testing.T) {
	tests := []struct {
		name     string
		payload  *committeeservice.CreateCommitteePayload
		expected *model.Committee
	}{
		{
			name: "complete payload conversion",
			payload: &committeeservice.CreateCommitteePayload{
				ProjectUID:            "project-123",
				Name:                  "Test Committee",
				Category:              "governance",
				Description:           stringPtr("Test description"),
				Website:               stringPtr("https://example.com"),
				EnableVoting:          true,
				SsoGroupEnabled:       true,
				RequiresReview:        true,
				Public:                true,
				DisplayName:           stringPtr("Test Display Name"),
				ParentUID:             stringPtr("parent-123"),
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
				LastReviewedBy:        stringPtr("user-123"),
				Writers:               []string{"writer1", "writer2"},
				Auditors:              []string{"auditor1", "auditor2"},
				Calendar: &struct {
					Public bool
				}{
					Public: true,
				},
			},
			expected: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-123",
					Name:            "Test Committee",
					Category:        "governance",
					Description:     "Test description",
					Website:         stringPtr("https://example.com"),
					EnableVoting:    true,
					SSOGroupEnabled: true,
					RequiresReview:  true,
					Public:          true,
					DisplayName:     "Test Display Name",
					ParentUID:       stringPtr("parent-123"),
					Calendar: model.Calendar{
						Public: true,
					},
				},
				CommitteeSettings: &model.CommitteeSettings{
					BusinessEmailRequired: true,
					LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
					LastReviewedBy:        stringPtr("user-123"),
					Writers:               []string{"writer1", "writer2"},
					Auditors:              []string{"auditor1", "auditor2"},
				},
			},
		},
		{
			name: "minimal payload conversion",
			payload: &committeeservice.CreateCommitteePayload{
				ProjectUID:            "project-123",
				Name:                  "Minimal Committee",
				Category:              "technical",
				EnableVoting:          false,
				SsoGroupEnabled:       false,
				RequiresReview:        false,
				Public:                false,
				BusinessEmailRequired: false,
			},
			expected: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					ProjectUID:      "project-123",
					Name:            "Minimal Committee",
					Category:        "technical",
					EnableVoting:    false,
					SSOGroupEnabled: false,
					RequiresReview:  false,
					Public:          false,
				},
				CommitteeSettings: &model.CommitteeSettings{
					BusinessEmailRequired: false,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertPayloadToDomain(tt.payload)

			assert.Equal(t, tt.expected.CommitteeBase, result.CommitteeBase)
			assert.Equal(t, tt.expected.CommitteeSettings, result.CommitteeSettings)
		})
	}
}

func TestConvertPayloadToBase(t *testing.T) {
	tests := []struct {
		name     string
		payload  *committeeservice.CreateCommitteePayload
		expected model.CommitteeBase
	}{
		{
			name:     "nil payload",
			payload:  nil,
			expected: model.CommitteeBase{},
		},
		{
			name: "complete base payload",
			payload: &committeeservice.CreateCommitteePayload{
				ProjectUID:      "project-123",
				Name:            "Test Committee",
				Category:        "governance",
				Description:     stringPtr("Test description"),
				Website:         stringPtr("https://example.com"),
				EnableVoting:    true,
				SsoGroupEnabled: true,
				RequiresReview:  true,
				Public:          true,
				DisplayName:     stringPtr("Test Display Name"),
				ParentUID:       stringPtr("parent-123"),
				Calendar: &struct {
					Public bool
				}{
					Public: true,
				},
			},
			expected: model.CommitteeBase{
				ProjectUID:      "project-123",
				Name:            "Test Committee",
				Category:        "governance",
				Description:     "Test description",
				Website:         stringPtr("https://example.com"),
				EnableVoting:    true,
				SSOGroupEnabled: true,
				RequiresReview:  true,
				Public:          true,
				DisplayName:     "Test Display Name",
				ParentUID:       stringPtr("parent-123"),
				Calendar: model.Calendar{
					Public: true,
				},
			},
		},
		{
			name: "payload without optional fields",
			payload: &committeeservice.CreateCommitteePayload{
				ProjectUID:      "project-123",
				Name:            "Minimal Committee",
				Category:        "technical",
				EnableVoting:    false,
				SsoGroupEnabled: false,
				RequiresReview:  false,
				Public:          false,
			},
			expected: model.CommitteeBase{
				ProjectUID:      "project-123",
				Name:            "Minimal Committee",
				Category:        "technical",
				EnableVoting:    false,
				SSOGroupEnabled: false,
				RequiresReview:  false,
				Public:          false,
			},
		},
		{
			name: "payload with nil calendar",
			payload: &committeeservice.CreateCommitteePayload{
				ProjectUID:      "project-123",
				Name:            "Test Committee",
				Category:        "governance",
				EnableVoting:    true,
				SsoGroupEnabled: false,
				RequiresReview:  false,
				Public:          true,
				Calendar:        nil,
			},
			expected: model.CommitteeBase{
				ProjectUID:      "project-123",
				Name:            "Test Committee",
				Category:        "governance",
				EnableVoting:    true,
				SSOGroupEnabled: false,
				RequiresReview:  false,
				Public:          true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertPayloadToBase(tt.payload)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertPayloadToSettings(t *testing.T) {
	tests := []struct {
		name     string
		payload  *committeeservice.CreateCommitteePayload
		expected *model.CommitteeSettings
	}{
		{
			name: "complete settings payload",
			payload: &committeeservice.CreateCommitteePayload{
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
				LastReviewedBy:        stringPtr("user-123"),
				Writers:               []string{"writer1", "writer2"},
				Auditors:              []string{"auditor1", "auditor2"},
			},
			expected: &model.CommitteeSettings{
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
				LastReviewedBy:        stringPtr("user-123"),
				Writers:               []string{"writer1", "writer2"},
				Auditors:              []string{"auditor1", "auditor2"},
			},
		},
		{
			name: "minimal settings payload",
			payload: &committeeservice.CreateCommitteePayload{
				BusinessEmailRequired: false,
			},
			expected: &model.CommitteeSettings{
				BusinessEmailRequired: false,
			},
		},
		{
			name: "payload with empty LastReviewedAt",
			payload: &committeeservice.CreateCommitteePayload{
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr(""),
				LastReviewedBy:        stringPtr("user-123"),
			},
			expected: &model.CommitteeSettings{
				BusinessEmailRequired: true,
				LastReviewedBy:        stringPtr("user-123"),
			},
		},
		{
			name: "payload with nil LastReviewedAt",
			payload: &committeeservice.CreateCommitteePayload{
				BusinessEmailRequired: true,
				LastReviewedAt:        nil,
				LastReviewedBy:        stringPtr("user-123"),
			},
			expected: &model.CommitteeSettings{
				BusinessEmailRequired: true,
				LastReviewedBy:        stringPtr("user-123"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertPayloadToSettings(tt.payload)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertPayloadToUpdateBase(t *testing.T) {
	tests := []struct {
		name     string
		payload  *committeeservice.UpdateCommitteeBasePayload
		expected *model.Committee
	}{
		{
			name:     "nil payload",
			payload:  nil,
			expected: &model.Committee{},
		},
		{
			name: "complete update base payload",
			payload: &committeeservice.UpdateCommitteeBasePayload{
				UID:             stringPtr("committee-123"),
				ProjectUID:      "project-123",
				Name:            "Updated Committee",
				Category:        "governance",
				Description:     stringPtr("Updated description"),
				Website:         stringPtr("https://updated.com"),
				EnableVoting:    true,
				SsoGroupEnabled: true,
				RequiresReview:  true,
				Public:          true,
				DisplayName:     stringPtr("Updated Display Name"),
				ParentUID:       stringPtr("parent-456"),
				Calendar: &struct {
					Public bool
				}{
					Public: false,
				},
			},
			expected: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					UID:             "committee-123",
					ProjectUID:      "project-123",
					Name:            "Updated Committee",
					Category:        "governance",
					Description:     "Updated description",
					Website:         stringPtr("https://updated.com"),
					EnableVoting:    true,
					SSOGroupEnabled: true,
					RequiresReview:  true,
					Public:          true,
					DisplayName:     "Updated Display Name",
					ParentUID:       stringPtr("parent-456"),
					Calendar: model.Calendar{
						Public: false,
					},
				},
				CommitteeSettings: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertPayloadToUpdateBase(tt.payload)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertPayloadToUpdateSettings(t *testing.T) {
	tests := []struct {
		name     string
		payload  *committeeservice.UpdateCommitteeSettingsPayload
		expected *model.CommitteeSettings
	}{
		{
			name:     "nil payload",
			payload:  nil,
			expected: &model.CommitteeSettings{},
		},
		{
			name: "complete update settings payload",
			payload: &committeeservice.UpdateCommitteeSettingsPayload{
				UID:                   stringPtr("committee-123"),
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
				LastReviewedBy:        stringPtr("user-456"),
				Writers:               []string{"writer3", "writer4"},
				Auditors:              []string{"auditor3", "auditor4"},
			},
			expected: &model.CommitteeSettings{
				UID:                   "committee-123",
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
				LastReviewedBy:        stringPtr("user-456"),
				Writers:               []string{"writer3", "writer4"},
				Auditors:              []string{"auditor3", "auditor4"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertPayloadToUpdateSettings(tt.payload)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertDomainToFullResponse(t *testing.T) {
	createdAt := time.Now()
	updatedAt := createdAt.Add(time.Hour)

	tests := []struct {
		name     string
		domain   *model.Committee
		expected *committeeservice.CommitteeFullWithReadonlyAttributes
	}{
		{
			name: "complete domain to response conversion",
			domain: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					UID:              "committee-123",
					ProjectUID:       "project-123",
					ProjectName:      "Test Project",
					Name:             "Test Committee",
					Category:         "governance",
					Description:      "Test description",
					Website:          stringPtr("https://example.com"),
					EnableVoting:     true,
					SSOGroupEnabled:  true,
					SSOGroupName:     "test-sso-group",
					RequiresReview:   true,
					Public:           true,
					DisplayName:      "Test Display Name",
					ParentUID:        stringPtr("parent-123"),
					TotalMembers:     10,
					TotalVotingRepos: 5,
					Calendar: model.Calendar{
						Public: true,
					},
				},
				CommitteeSettings: &model.CommitteeSettings{
					UID:                   "committee-123",
					BusinessEmailRequired: true,
					LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
					LastReviewedBy:        stringPtr("user-123"),
					Writers:               []string{"writer1", "writer2"},
					Auditors:              []string{"auditor1", "auditor2"},
					CreatedAt:             createdAt,
					UpdatedAt:             updatedAt,
				},
			},
			expected: &committeeservice.CommitteeFullWithReadonlyAttributes{
				UID:              stringPtr("committee-123"),
				ProjectUID:       stringPtr("project-123"),
				Name:             stringPtr("Test Committee"),
				Category:         stringPtr("governance"),
				Description:      stringPtr("Test description"),
				Website:          stringPtr("https://example.com"),
				EnableVoting:     true,
				SsoGroupEnabled:  true,
				SsoGroupName:     stringPtr("test-sso-group"),
				RequiresReview:   true,
				Public:           true,
				DisplayName:      stringPtr("Test Display Name"),
				ParentUID:        stringPtr("parent-123"),
				TotalMembers:     intPtr(10),
				TotalVotingRepos: intPtr(5),
				Calendar: &struct {
					Public bool
				}{
					Public: true,
				},
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
				LastReviewedBy:        stringPtr("user-123"),
				Writers:               []string{"writer1", "writer2"},
				Auditors:              []string{"auditor1", "auditor2"},
			},
		},
		{
			name: "domain without settings",
			domain: &model.Committee{
				CommitteeBase: model.CommitteeBase{
					UID:         "committee-456",
					ProjectUID:  "project-456",
					Name:        "Minimal Committee",
					Category:    "technical",
					Description: "Minimal description",
					Calendar: model.Calendar{
						Public: false,
					},
				},
				CommitteeSettings: nil,
			},
			expected: &committeeservice.CommitteeFullWithReadonlyAttributes{
				UID:              stringPtr("committee-456"),
				ProjectUID:       stringPtr("project-456"),
				Name:             stringPtr("Minimal Committee"),
				Category:         stringPtr("technical"),
				Description:      stringPtr("Minimal description"),
				DisplayName:      stringPtr(""),
				SsoGroupName:     stringPtr(""),
				TotalMembers:     intPtr(0),
				TotalVotingRepos: intPtr(0),
				Calendar: &struct {
					Public bool
				}{
					Public: false,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertDomainToFullResponse(tt.domain)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertBaseToResponse(t *testing.T) {
	tests := []struct {
		name     string
		base     *model.CommitteeBase
		expected *committeeservice.CommitteeBaseWithReadonlyAttributes
	}{
		{
			name: "complete base to response conversion",
			base: &model.CommitteeBase{
				UID:              "committee-123",
				ProjectUID:       "project-123",
				ProjectName:      "Test Project",
				Name:             "Test Committee",
				Category:         "governance",
				Description:      "Test description",
				Website:          stringPtr("https://example.com"),
				EnableVoting:     true,
				SSOGroupEnabled:  true,
				SSOGroupName:     "test-sso-group",
				RequiresReview:   true,
				Public:           true,
				DisplayName:      "Test Display Name",
				ParentUID:        stringPtr("parent-123"),
				TotalMembers:     15,
				TotalVotingRepos: 8,
				Calendar: model.Calendar{
					Public: true,
				},
			},
			expected: &committeeservice.CommitteeBaseWithReadonlyAttributes{
				UID:              stringPtr("committee-123"),
				ProjectUID:       stringPtr("project-123"),
				ProjectName:      stringPtr("Test Project"),
				Name:             stringPtr("Test Committee"),
				Category:         stringPtr("governance"),
				Description:      stringPtr("Test description"),
				Website:          stringPtr("https://example.com"),
				EnableVoting:     true,
				SsoGroupEnabled:  true,
				SsoGroupName:     stringPtr("test-sso-group"),
				RequiresReview:   true,
				Public:           true,
				DisplayName:      stringPtr("Test Display Name"),
				ParentUID:        stringPtr("parent-123"),
				TotalMembers:     intPtr(15),
				TotalVotingRepos: intPtr(8),
				Calendar: &struct {
					Public bool
				}{
					Public: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertBaseToResponse(tt.base)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertSettingsToResponse(t *testing.T) {
	createdAt := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		settings *model.CommitteeSettings
		expected *committeeservice.CommitteeSettingsWithReadonlyAttributes
	}{
		{
			name: "complete settings to response conversion",
			settings: &model.CommitteeSettings{
				UID:                   "committee-123",
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
				LastReviewedBy:        stringPtr("user-123"),
				CreatedAt:             createdAt,
				UpdatedAt:             updatedAt,
			},
			expected: &committeeservice.CommitteeSettingsWithReadonlyAttributes{
				UID:                   stringPtr("committee-123"),
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2023-01-01T00:00:00Z"),
				LastReviewedBy:        stringPtr("user-123"),
				CreatedAt:             stringPtr("2023-01-01T12:00:00Z"),
				UpdatedAt:             stringPtr("2023-01-02T12:00:00Z"),
			},
		},
		{
			name: "settings with zero timestamps",
			settings: &model.CommitteeSettings{
				UID:                   "committee-456",
				BusinessEmailRequired: false,
				CreatedAt:             time.Time{},
				UpdatedAt:             time.Time{},
			},
			expected: &committeeservice.CommitteeSettingsWithReadonlyAttributes{
				UID:                   stringPtr("committee-456"),
				BusinessEmailRequired: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertSettingsToResponse(tt.settings)

			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper functions for creating pointers to primitives
func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}
