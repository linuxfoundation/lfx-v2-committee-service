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
				UID:         stringPtr("committee-456"),
				ProjectUID:  stringPtr("project-456"),
				Name:        stringPtr("Minimal Committee"),
				Category:    stringPtr("technical"),
				Description: stringPtr("Minimal description"),
				// Optional fields with empty values should be nil
				DisplayName:      nil,
				SsoGroupName:     nil,
				TotalMembers:     nil,
				TotalVotingRepos: nil,
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

func TestConvertMemberPayloadToDomain(t *testing.T) {
	tests := []struct {
		name     string
		payload  *committeeservice.CreateCommitteeMemberPayload
		expected *model.CommitteeMember
	}{
		{
			name:     "nil payload",
			payload:  nil,
			expected: &model.CommitteeMember{},
		},
		{
			name: "complete member payload conversion",
			payload: &committeeservice.CreateCommitteeMemberPayload{
				UID:         "committee-123",
				Email:       "john.doe@example.com",
				Username:    stringPtr("johndoe"),
				FirstName:   stringPtr("John"),
				LastName:    stringPtr("Doe"),
				JobTitle:    stringPtr("Software Engineer"),
				AppointedBy: "committee-chair",
				Status:      "active",
				Role: &struct {
					Name      string
					StartDate *string
					EndDate   *string
				}{
					Name:      "contributor",
					StartDate: stringPtr("2024-01-01"),
					EndDate:   stringPtr("2024-12-31"),
				},
				Voting: &struct {
					Status    string
					StartDate *string
					EndDate   *string
				}{
					Status:    "eligible",
					StartDate: stringPtr("2024-01-01"),
					EndDate:   stringPtr("2024-12-31"),
				},
				Agency:  stringPtr("Test Agency"),
				Country: stringPtr("USA"),
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					ID:      stringPtr("abc"),
					Name:    stringPtr("Test Organization"),
					Website: stringPtr("https://test-org.com"),
				},
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-123",
					Email:        "john.doe@example.com",
					Username:     "johndoe",
					FirstName:    "John",
					LastName:     "Doe",
					JobTitle:     "Software Engineer",
					AppointedBy:  "committee-chair",
					Status:       "active",
					Role: model.CommitteeMemberRole{
						Name:      "contributor",
						StartDate: "2024-01-01",
						EndDate:   "2024-12-31",
					},
					Voting: model.CommitteeMemberVotingInfo{
						Status:    "eligible",
						StartDate: "2024-01-01",
						EndDate:   "2024-12-31",
					},
					Agency:  "Test Agency",
					Country: "USA",
					Organization: model.CommitteeMemberOrganization{
						ID:      "abc",
						Name:    "Test Organization",
						Website: "https://test-org.com",
					},
				},
			},
		},
		{
			name: "minimal member payload conversion",
			payload: &committeeservice.CreateCommitteeMemberPayload{
				UID:         "committee-456",
				Email:       "minimal@example.com",
				AppointedBy: "chair",
				Status:      "pending",
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-456",
					Email:        "minimal@example.com",
					AppointedBy:  "chair",
					Status:       "pending",
				},
			},
		},
		{
			name: "member payload with nil optional fields",
			payload: &committeeservice.CreateCommitteeMemberPayload{
				UID:          "committee-789",
				Email:        "test@example.com",
				Username:     nil,
				FirstName:    nil,
				LastName:     nil,
				JobTitle:     nil,
				AppointedBy:  "chair",
				Status:       "active",
				Role:         nil,
				Voting:       nil,
				Agency:       nil,
				Country:      nil,
				Organization: nil,
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-789",
					Email:        "test@example.com",
					AppointedBy:  "chair",
					Status:       "active",
				},
			},
		},
		{
			name: "member payload with partial role information",
			payload: &committeeservice.CreateCommitteeMemberPayload{
				UID:         "committee-abc",
				Email:       "partial@example.com",
				AppointedBy: "chair",
				Status:      "active",
				Role: &struct {
					Name      string
					StartDate *string
					EndDate   *string
				}{
					Name:      "maintainer",
					StartDate: stringPtr("2024-01-01"),
					EndDate:   nil,
				},
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-abc",
					Email:        "partial@example.com",
					AppointedBy:  "chair",
					Status:       "active",
					Role: model.CommitteeMemberRole{
						Name:      "maintainer",
						StartDate: "2024-01-01",
						EndDate:   "",
					},
				},
			},
		},
		{
			name: "member payload with partial organization information",
			payload: &committeeservice.CreateCommitteeMemberPayload{
				UID:         "committee-def",
				Email:       "org@example.com",
				AppointedBy: "chair",
				Status:      "active",
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					Name:    stringPtr("Partial Org"),
					Website: nil,
				},
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					CommitteeUID: "committee-def",
					Email:        "org@example.com",
					AppointedBy:  "chair",
					Status:       "active",
					Organization: model.CommitteeMemberOrganization{
						Name:    "Partial Org",
						Website: "",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertMemberPayloadToDomain(tt.payload)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertMemberDomainToFullResponse(t *testing.T) {
	createdAt := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		member   *model.CommitteeMember
		expected *committeeservice.CommitteeMemberFullWithReadonlyAttributes
	}{
		{
			name:     "nil member",
			member:   nil,
			expected: nil,
		},
		{
			name: "complete member domain to response conversion",
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:         "member-123",
					Username:    "johndoe",
					Email:       "john.doe@example.com",
					FirstName:   "John",
					LastName:    "Doe",
					JobTitle:    "Senior Software Engineer",
					AppointedBy: "committee-chair",
					Status:      "active",
					Role: model.CommitteeMemberRole{
						Name:      "maintainer",
						StartDate: "2024-01-01",
						EndDate:   "2024-12-31",
					},
					Voting: model.CommitteeMemberVotingInfo{
						Status:    "eligible",
						StartDate: "2024-01-01",
						EndDate:   "2024-12-31",
					},
					Agency:  "Test Agency",
					Country: "USA",
					Organization: model.CommitteeMemberOrganization{
						ID:      "org-123",
						Name:    "Test Organization",
						Website: "https://test-org.com",
					},
					CommitteeUID: "committee-123",
					CreatedAt:    createdAt,
					UpdatedAt:    updatedAt,
				},
			},
			expected: &committeeservice.CommitteeMemberFullWithReadonlyAttributes{
				UID:          stringPtr("member-123"),
				CommitteeUID: stringPtr("committee-123"),
				Username:     stringPtr("johndoe"),
				Email:        stringPtr("john.doe@example.com"),
				FirstName:    stringPtr("John"),
				LastName:     stringPtr("Doe"),
				JobTitle:     stringPtr("Senior Software Engineer"),
				AppointedBy:  "committee-chair",
				Status:       "active",
				Agency:       stringPtr("Test Agency"),
				Country:      stringPtr("USA"),
				Role: &struct {
					Name      string
					StartDate *string
					EndDate   *string
				}{
					Name:      "maintainer",
					StartDate: stringPtr("2024-01-01"),
					EndDate:   stringPtr("2024-12-31"),
				},
				Voting: &struct {
					Status    string
					StartDate *string
					EndDate   *string
				}{
					Status:    "eligible",
					StartDate: stringPtr("2024-01-01"),
					EndDate:   stringPtr("2024-12-31"),
				},
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					ID:      stringPtr("org-123"),
					Name:    stringPtr("Test Organization"),
					Website: stringPtr("https://test-org.com"),
				},
				CreatedAt: stringPtr("2024-01-01T12:00:00Z"),
				UpdatedAt: stringPtr("2024-01-02T12:00:00Z"),
			},
		},
		{
			name: "minimal member domain to response conversion",
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-456",
					Email:        "minimal@example.com",
					AppointedBy:  "chair",
					Status:       "pending",
					CommitteeUID: "committee-456",
				},
			},
			expected: &committeeservice.CommitteeMemberFullWithReadonlyAttributes{
				UID:          stringPtr("member-456"),
				CommitteeUID: stringPtr("committee-456"),
				Email:        stringPtr("minimal@example.com"),
				AppointedBy:  "chair",
				Status:       "pending",
				// Optional fields with empty values should be nil
				Username:     nil,
				FirstName:    nil,
				LastName:     nil,
				JobTitle:     nil,
				Agency:       nil,
				Country:      nil,
				Role:         nil,
				Voting:       nil,
				Organization: nil,
			},
		},
		{
			name: "member with zero timestamps",
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-789",
					Email:        "timestamps@example.com",
					AppointedBy:  "chair",
					Status:       "active",
					CommitteeUID: "committee-789",
					CreatedAt:    time.Time{},
					UpdatedAt:    time.Time{},
				},
			},
			expected: &committeeservice.CommitteeMemberFullWithReadonlyAttributes{
				UID:          stringPtr("member-789"),
				CommitteeUID: stringPtr("committee-789"),
				Email:        stringPtr("timestamps@example.com"),
				AppointedBy:  "chair",
				Status:       "active",
				// Optional fields with empty values should be nil
				Username:     nil,
				FirstName:    nil,
				LastName:     nil,
				JobTitle:     nil,
				Agency:       nil,
				Country:      nil,
				Role:         nil,
				Voting:       nil,
				Organization: nil,
				// CreatedAt and UpdatedAt should be nil when timestamps are zero
				CreatedAt: nil,
				UpdatedAt: nil,
			},
		},
		{
			name: "member with partial role and voting info",
			member: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:         "member-partial",
					Email:       "partial@example.com",
					AppointedBy: "chair",
					Status:      "active",
					Role: model.CommitteeMemberRole{
						Name:      "contributor",
						StartDate: "2024-01-01",
						// EndDate is empty
					},
					Voting: model.CommitteeMemberVotingInfo{
						Status: "eligible",
						// StartDate and EndDate are empty
					},
					CommitteeUID: "committee-partial",
				},
			},
			expected: &committeeservice.CommitteeMemberFullWithReadonlyAttributes{
				UID:          stringPtr("member-partial"),
				CommitteeUID: stringPtr("committee-partial"),
				Email:        stringPtr("partial@example.com"),
				AppointedBy:  "chair",
				Status:       "active",
				// Optional fields with empty values should be nil
				Username:  nil,
				FirstName: nil,
				LastName:  nil,
				JobTitle:  nil,
				Agency:    nil,
				Country:   nil,
				Role: &struct {
					Name      string
					StartDate *string
					EndDate   *string
				}{
					Name:      "contributor",
					StartDate: stringPtr("2024-01-01"),
					EndDate:   nil, // Empty dates should be nil
				},
				Voting: &struct {
					Status    string
					StartDate *string
					EndDate   *string
				}{
					Status:    "eligible",
					StartDate: nil, // Empty dates should be nil
					EndDate:   nil,
				},
				Organization: nil, // Empty organization should be nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertMemberDomainToFullResponse(tt.member)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertPayloadToUpdateMember(t *testing.T) {
	tests := []struct {
		name     string
		payload  *committeeservice.UpdateCommitteeMemberPayload
		expected *model.CommitteeMember
	}{
		{
			name: "complete payload conversion",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:         "committee-123",
				MemberUID:   "member-456",
				Username:    stringPtr("testuser"),
				Email:       "test@example.com",
				FirstName:   stringPtr("John"),
				LastName:    stringPtr("Doe"),
				JobTitle:    stringPtr("Engineer"),
				AppointedBy: "admin",
				Status:      "active",
				Role: &struct {
					Name      string
					StartDate *string
					EndDate   *string
				}{
					Name:      "Chair",
					StartDate: stringPtr("2023-01-01"),
					EndDate:   stringPtr("2024-01-01"),
				},
				Voting: &struct {
					Status    string
					StartDate *string
					EndDate   *string
				}{
					Status:    "eligible",
					StartDate: stringPtr("2023-01-01"),
					EndDate:   stringPtr("2024-01-01"),
				},
				Agency:  stringPtr("Test Agency"),
				Country: stringPtr("US"),
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					ID:      stringPtr("org-123"),
					Name:    stringPtr("Test Org"),
					Website: stringPtr("https://testorg.com"),
				},
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-456",
					CommitteeUID: "committee-123",
					Username:     "testuser",
					Email:        "test@example.com",
					FirstName:    "John",
					LastName:     "Doe",
					JobTitle:     "Engineer",
					AppointedBy:  "admin",
					Status:       "active",
					Role: model.CommitteeMemberRole{
						Name:      "Chair",
						StartDate: "2023-01-01",
						EndDate:   "2024-01-01",
					},
					Voting: model.CommitteeMemberVotingInfo{
						Status:    "eligible",
						StartDate: "2023-01-01",
						EndDate:   "2024-01-01",
					},
					Agency:  "Test Agency",
					Country: "US",
					Organization: model.CommitteeMemberOrganization{
						ID:      "org-123",
						Name:    "Test Org",
						Website: "https://testorg.com",
					},
				},
			},
		},
		{
			name: "minimal payload conversion",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:         "committee-123",
				MemberUID:   "member-456",
				Email:       "minimal@example.com",
				AppointedBy: "admin",
				Status:      "active",
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-456",
					CommitteeUID: "committee-123",
					Email:        "minimal@example.com",
					AppointedBy:  "admin",
					Status:       "active",
				},
			},
		},
		{
			name:     "nil payload",
			payload:  nil,
			expected: &model.CommitteeMember{},
		},
		{
			name: "payload with nil optional fields",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:          "committee-123",
				MemberUID:    "member-456",
				Email:        "test@example.com",
				AppointedBy:  "admin",
				Status:       "active",
				Username:     nil,
				FirstName:    nil,
				LastName:     nil,
				JobTitle:     nil,
				Role:         nil,
				Voting:       nil,
				Agency:       nil,
				Country:      nil,
				Organization: nil,
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-456",
					CommitteeUID: "committee-123",
					Email:        "test@example.com",
					AppointedBy:  "admin",
					Status:       "active",
				},
			},
		},
		{
			name: "payload with partial organization",
			payload: &committeeservice.UpdateCommitteeMemberPayload{
				UID:         "committee-123",
				MemberUID:   "member-456",
				Email:       "test@example.com",
				AppointedBy: "admin",
				Status:      "active",
				Organization: &struct {
					ID      *string
					Name    *string
					Website *string
				}{
					ID:      stringPtr("org-123"),
					Name:    stringPtr("Partial Org"),
					Website: nil,
				},
			},
			expected: &model.CommitteeMember{
				CommitteeMemberBase: model.CommitteeMemberBase{
					UID:          "member-456",
					CommitteeUID: "committee-123",
					Email:        "test@example.com",
					AppointedBy:  "admin",
					Status:       "active",
					Organization: model.CommitteeMemberOrganization{
						ID:   "org-123",
						Name: "Partial Org",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &committeeServicesrvc{}
			result := svc.convertPayloadToUpdateMember(tt.payload)
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
