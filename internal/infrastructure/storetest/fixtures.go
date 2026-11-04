// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package storetest holds the behavioral test suites that every committee storage backend must pass.
// It is test support: import it only from _test.go files, never from code that ships in a binary,
// because it imports the testing package.
//
// A suite takes a Backend, which builds a fresh, empty store, so the same cases run against the NATS
// KV store and the PostgreSQL store and any difference between them shows up as a failing case.
package storetest

import (
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

func strPtr(s string) *string { return &s }

// FixtureTime is the creation time the fixtures use.
var FixtureTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// NewCommittee returns a committee with every field set, so a round trip shows any field that is not
// stored or read back.
func NewCommittee(uid, project, name string) *model.Committee {
	created := FixtureTime
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

// SFID15 and SFID18 are the same Salesforce account in its two spellings.
const SFID15 = "001A000001abcDE"

// SFID18 is SFID15 in its 18-character form, the one organization lookups are keyed by.
var SFID18 = utils.NormalizeAccountSFID(SFID15)

// NewMember returns a member with every field set, for the committee and email.
func NewMember(uid, committeeUID, email string) *model.CommitteeMember {
	created := FixtureTime
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID:               uid,
		CommitteeUID:      committeeUID,
		Username:          "first-last-" + uid,
		Avatar:            "https://example.com/avatar.png",
		Email:             email,
		FirstName:         "First",
		LastName:          "Last",
		JobTitle:          "Engineer",
		LinkedInProfile:   "https://example.com/in/first-last",
		Role:              model.CommitteeMemberRole{Name: "Chair", StartDate: "2026-01-01", EndDate: "2027-01-01"},
		AppointedBy:       "Community",
		Status:            "Active",
		Voting:            model.CommitteeMemberVotingInfo{Status: "Voting Rep", StartDate: "2026-02-01", EndDate: "2027-02-01"},
		Organization:      model.CommitteeMemberOrganization{ID: SFID18, Name: "Example Org", Website: "https://example.com"},
		CommitteeName:     "Committee One",
		CommitteeCategory: "Technical Steering Committee",
		ProjectUID:        "project-1",
		ProjectSlug:       "project-slug",
		CreatedAt:         created,
		UpdatedAt:         created.Add(time.Hour),
	}}
}

// normalizeMember makes a member comparable across backends: times at the shared precision.
func normalizeMember(m *model.CommitteeMember) *model.CommitteeMember {
	c := *m
	c.CreatedAt, c.UpdatedAt = micro(c.CreatedAt), micro(c.UpdatedAt)
	return &c
}

// NewInvite returns an invite with every field set, for the committee and invitee email.
func NewInvite(uid, committeeUID, email string) *model.CommitteeInvite {
	created := FixtureTime
	return &model.CommitteeInvite{
		UID:                  uid,
		CommitteeUID:         committeeUID,
		CommitteeName:        "Committee One",
		OrganizationRequired: true,
		InviteeEmail:         email,
		Role:                 "Member",
		Organization:         &model.CommitteeMemberOrganization{ID: SFID18, Name: "Example Org", Website: "https://example.com"},
		Status:               "pending",
		CreatedAt:            created,
		Inviter:              &model.CommitteeUser{Name: "First Last", Username: "first-last", Email: "first.last@example.com"},
		ExpiresAt:            created.Add(30 * 24 * time.Hour),
	}
}

// normalizeInvite makes an invite comparable across backends: times at the shared precision.
func normalizeInvite(i *model.CommitteeInvite) *model.CommitteeInvite {
	c := *i
	c.CreatedAt, c.ExpiresAt = micro(c.CreatedAt), micro(c.ExpiresAt)
	return &c
}

// NewApplication returns an application with every field set, for the committee and applicant email.
func NewApplication(uid, committeeUID, email string) *model.CommitteeApplication {
	return &model.CommitteeApplication{
		UID:               uid,
		CommitteeUID:      committeeUID,
		ApplicantEmail:    email,
		ApplicantUsername: "first-last-" + uid,
		Message:           "Please let me join",
		Status:            "pending",
		ReviewerNotes:     "Looks good",
		CreatedAt:         FixtureTime,
		Organization:      &model.CommitteeMemberOrganization{ID: SFID18, Name: "Example Org", Website: "https://example.com"},
	}
}

// normalizeApplication makes an application comparable across backends: times at the shared precision.
func normalizeApplication(a *model.CommitteeApplication) *model.CommitteeApplication {
	c := *a
	c.CreatedAt = micro(c.CreatedAt)
	return &c
}

// NewAuditUser is the audit user the link, folder and document fixtures use.
func NewAuditUser() *model.CommitteeUser {
	return &model.CommitteeUser{Name: "First Last", Username: "first-last", Email: "first.last@example.com", Avatar: "https://example.com/avatar.png"}
}

// NewLink returns a link with every field set, in folder f-1.
func NewLink(uid, committeeUID string) *model.CommitteeLink {
	folder := "f-1"
	return &model.CommitteeLink{
		UID: uid, CommitteeUID: committeeUID, FolderUID: &folder, Name: "Charter", URL: "https://example.com/charter",
		Description: "The charter", CreatedBy: NewAuditUser(), UpdatedBy: NewAuditUser(),
		CreatedAt: FixtureTime, UpdatedAt: FixtureTime.Add(time.Hour),
	}
}

// normalizeLink makes a link comparable across backends: times at the shared precision.
func normalizeLink(l *model.CommitteeLink) *model.CommitteeLink {
	c := *l
	c.CreatedAt, c.UpdatedAt = micro(c.CreatedAt), micro(c.UpdatedAt)
	return &c
}

// NewFolder returns a folder with every field set, for the committee and name.
func NewFolder(uid, committeeUID, name string) *model.CommitteeLinkFolder {
	return &model.CommitteeLinkFolder{
		UID: uid, CommitteeUID: committeeUID, Name: name, CreatedBy: NewAuditUser(), UpdatedBy: NewAuditUser(),
		CreatedAt: FixtureTime, UpdatedAt: FixtureTime.Add(time.Hour),
	}
}

// normalizeFolder makes a folder comparable across backends: times at the shared precision.
func normalizeFolder(f *model.CommitteeLinkFolder) *model.CommitteeLinkFolder {
	c := *f
	c.CreatedAt, c.UpdatedAt = micro(c.CreatedAt), micro(c.UpdatedAt)
	return &c
}

// NewDocument returns document metadata with every field set, in folder f-1, for the committee and name.
func NewDocument(uid, committeeUID, name string) *model.CommitteeDocument {
	folder := "f-1"
	return &model.CommitteeDocument{
		UID: uid, CommitteeUID: committeeUID, FolderUID: &folder, Name: name, Description: "Meeting minutes",
		FileName: "minutes.pdf", FileSize: 2048, ContentType: "application/pdf",
		CreatedBy: NewAuditUser(), UpdatedBy: NewAuditUser(), CreatedAt: FixtureTime, UpdatedAt: FixtureTime.Add(time.Hour),
	}
}

// normalizeDocument makes document metadata comparable across backends: times at the shared precision.
func normalizeDocument(d *model.CommitteeDocument) *model.CommitteeDocument {
	c := *d
	c.CreatedAt, c.UpdatedAt = micro(c.CreatedAt), micro(c.UpdatedAt)
	return &c
}

// FixtureWindow is the start of the weekly window the brief fixtures use: a Sunday, 00:00 UTC.
var FixtureWindow = time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)

// NewBrief returns a brief with every field set, for the committee and the week starting at window.
func NewBrief(uid, committeeUID string, window time.Time) *model.GroupWeeklyBrief {
	edited := FixtureTime.Add(2 * time.Hour)
	return &model.GroupWeeklyBrief{
		UID: uid, CommitteeUID: committeeUID, WindowStart: window, WindowEnd: window.AddDate(0, 0, 7),
		State: model.GroupWeeklyBriefStateEdited, ErrorReason: "none", BriefText: "The group met twice.",
		SourceRefs:    []model.SourceRef{{Kind: "meeting", ID: "m-1"}, {Kind: "doc", ID: "https://example.com/doc", Title: "Doc", Excerpt: "An excerpt"}},
		PromptVersion: "v1", Model: "model-1", RegenerationCount: 2, PrivateSourcePresent: true,
		CreatedAt: FixtureTime, UpdatedAt: FixtureTime.Add(time.Hour), LastEditedAt: &edited, LastEditedBy: "first-last",
	}
}

// NewThrottle returns a throttle entry for the committee and the week starting at window.
func NewThrottle(committeeUID string, window time.Time) *model.GroupWeeklyBriefThrottle {
	return &model.GroupWeeklyBriefThrottle{
		CommitteeUID: committeeUID, WindowStart: window, WindowEnd: window.AddDate(0, 0, 7),
		GeneratesUsed: 1, RegenerationsUsed: 2, WindowResetsAt: window.AddDate(0, 0, 7),
	}
}

// normalizeBrief makes a brief comparable across backends: times at the shared precision, and the
// revision, which the store assigns, left out.
func normalizeBrief(b *model.GroupWeeklyBrief) *model.GroupWeeklyBrief {
	c := *b
	c.WindowStart, c.WindowEnd = micro(c.WindowStart), micro(c.WindowEnd)
	c.CreatedAt, c.UpdatedAt = micro(c.CreatedAt), micro(c.UpdatedAt)
	if c.LastEditedAt != nil {
		edited := micro(*c.LastEditedAt)
		c.LastEditedAt = &edited
	}
	c.Revision = 0
	return &c
}

// normalizeThrottle is normalizeBrief for a throttle entry.
func normalizeThrottle(t *model.GroupWeeklyBriefThrottle) *model.GroupWeeklyBriefThrottle {
	c := *t
	c.WindowStart, c.WindowEnd, c.WindowResetsAt = micro(c.WindowStart), micro(c.WindowEnd), micro(c.WindowResetsAt)
	c.Revision = 0
	return &c
}
