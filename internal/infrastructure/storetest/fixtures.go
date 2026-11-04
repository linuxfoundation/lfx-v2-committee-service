// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package storetest holds the behavioral test suites that every committee storage backend must pass.
// A suite takes a Backend, which builds a fresh, empty store, so the same cases run against the NATS
// KV store and the PostgreSQL store and any difference between them shows up as a failing case.
package storetest

import (
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func strPtr(s string) *string { return &s }

// Committee returns a committee with every field set, so a round trip shows any field that is not
// stored or read back.
func Committee(uid, project, name string) *model.Committee {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &model.Committee{
		CommitteeBase: model.CommitteeBase{
			UID:              uid,
			ProjectUID:       project,
			ProjectName:      "Project Name",
			ProjectSlug:      "project-slug",
			Name:             name,
			Category:         "Technical Steering Committee",
			Description:      "A committee",
			Website:          strPtr("https://example.com"),
			MailingList:      strPtr("first-last@example.com"),
			ChatChannel:      strPtr("#first-last"),
			EnableVoting:     true,
			SSOGroupEnabled:  true,
			SSOGroupName:     "sso-" + uid,
			RequiresReview:   true,
			Public:           true,
			JoinMode:         "invite_only",
			Calendar:         model.Calendar{Public: true},
			DisplayName:      "Display Name",
			ParentUID:        strPtr("parent-uid"),
			Repository:       strPtr("https://example.com/repo"),
			Scope:            []string{"scope-a", "scope-b"},
			Deliverables:     []string{"deliverable"},
			KeyDates:         []model.KeyDate{{Date: "2026-06-01", Label: "Kickoff"}},
			ExternalSources:  []model.ExternalSource{{Provider: "provider", EntityType: "group", Label: "Label", URL: "https://example.com/source", ExternalID: "id"}},
			Charter:          &model.Charter{URL: "https://example.com/charter", Version: 3, UpdatedAt: created, UpdatedBy: &model.CommitteeUser{Name: "First Last", Username: "first-last", Email: "first.last@example.com"}},
			TotalMembers:     4,
			TotalVotingRepos: 2,
			HasMailingList:   true,
			CreatedAt:        created,
			UpdatedAt:        created.Add(time.Hour),
		},
		CommitteeSettings: &model.CommitteeSettings{
			BusinessEmailRequired: true,
			ShowMeetingAttendees:  true,
			MemberVisibility:      "basic_profile",
			ChatWebhookURL:        strPtr("https://example.com/hook"),
			HasChatWebhook:        true,
			LastReviewedAt:        strPtr("2026-05-01"),
			LastReviewedBy:        strPtr("first-last"),
			Writers:               []model.CommitteeUser{{Name: "First Last", Username: "first-last", Email: "first.last@example.com"}},
			Auditors:              []model.CommitteeUser{{Name: "First Last", Username: "first-last-2", Email: "first.last2@example.com"}},
			CreatedAt:             created,
			UpdatedAt:             created.Add(time.Hour),
		},
	}
}

// micro truncates a time to the precision every backend can keep (the PostgreSQL column holds
// microseconds, KV keeps nanoseconds) and puts it in UTC.
func micro(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC().Truncate(time.Microsecond)
}

// normalizeBase makes a committee comparable across backends: times at the shared precision.
func normalizeBase(b *model.CommitteeBase) *model.CommitteeBase {
	c := *b
	c.CreatedAt, c.UpdatedAt = micro(c.CreatedAt), micro(c.UpdatedAt)
	if c.Charter != nil {
		charter := *c.Charter
		charter.UpdatedAt = micro(charter.UpdatedAt)
		c.Charter = &charter
	}
	return &c
}

// normalizeSettings is normalizeBase for settings.
func normalizeSettings(s *model.CommitteeSettings) *model.CommitteeSettings {
	c := *s
	c.CreatedAt, c.UpdatedAt = micro(c.CreatedAt), micro(c.UpdatedAt)
	return &c
}
