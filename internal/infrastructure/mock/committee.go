// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mock

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

// Global mock repository instance to share data between all repositories
var (
	globalMockRepo     *MockRepository
	globalMockRepoOnce = &sync.Once{}
)

// NewMockRepository creates a new mock repository with sample data
func NewMockRepository() *MockRepository {

	globalMockRepoOnce.Do(func() {
		now := time.Now()
		ctx := context.Background()

		mock := &MockRepository{
			committees:            make(map[string]*model.Committee),
			committeeSettings:     make(map[string]*model.CommitteeSettings),
			committeeMembers:      make(map[string]map[string]*model.CommitteeMember),
			projectSlugs:          make(map[string]string),
			projectNames:          make(map[string]string),
			projectWriters:        make(map[string][]model.CommitteeUser),
			committeeIndexKeys:    make(map[string]*model.Committee),
			memberIndexKeys:       make(map[string]map[string]*model.CommitteeMember),
			committeeInvites:      make(map[string]*model.CommitteeInvite),
			committeeApplications: make(map[string]*model.CommitteeApplication),
			inviteIndexKeys:       make(map[string]*model.CommitteeInvite),
			applicationIndexKeys:  make(map[string]*model.CommitteeApplication),
			committeeRevisions:    make(map[string]uint64),
			settingsRevisions:     make(map[string]uint64),
			memberRevisions:       make(map[string]uint64),
			inviteRevisions:       make(map[string]uint64),
			applicationRevisions:  make(map[string]uint64),
		}

		// Add some sample data
		sampleCommittee := &model.Committee{
			CommitteeBase: model.CommitteeBase{
				UID:             "committee-1",
				ProjectUID:      "7cad5a8d-19d0-41a4-81a6-043453daf9ee",
				Name:            "Technical Advisory Committee",
				Category:        "governance",
				Description:     "Main technical governance body for the project",
				Website:         stringPtr("https://example.com/tac"),
				EnableVoting:    true,
				SSOGroupEnabled: true,
				SSOGroupName:    "7cad5a8d-19d0-41a4-81a6-043453daf9ee-technical-advisory-committee",
				RequiresReview:  false,
				Public:          false,
				Calendar: model.Calendar{
					Public: true,
				},
				DisplayName:      "TAC",
				ParentUID:        nil,
				TotalMembers:     5,
				TotalVotingRepos: 3,
				CreatedAt:        now.Add(-24 * time.Hour),
				UpdatedAt:        now,
			},
			CommitteeSettings: &model.CommitteeSettings{
				UID:                   "committee-1",
				BusinessEmailRequired: true,
				LastReviewedAt:        stringPtr("2025-08-04T09:00:00Z"),
				LastReviewedBy:        stringPtr("admin@example.com"),
				Writers: []model.CommitteeUser{
					{Username: "writer1@example.com", Email: "writer1@example.com"},
					{Username: "writer2@example.com", Email: "writer2@example.com"},
				},
				Auditors: []model.CommitteeUser{
					{Username: "auditor1@example.com", Email: "auditor1@example.com"},
				},
				CreatedAt: now.Add(-24 * time.Hour),
				UpdatedAt: now,
			},
		}

		mock.committees[sampleCommittee.CommitteeBase.UID] = sampleCommittee
		mock.committeeSettings[sampleCommittee.CommitteeBase.UID] = sampleCommittee.CommitteeSettings
		mock.projectSlugs["7cad5a8d-19d0-41a4-81a6-043453daf9ee"] = "sample-project"
		mock.projectNames["7cad5a8d-19d0-41a4-81a6-043453daf9ee"] = "Sample Project"
		mock.committeeIndexKeys[sampleCommittee.BuildIndexKey(ctx)] = sampleCommittee
		mock.committeeRevisions[sampleCommittee.CommitteeBase.UID] = 1
		mock.settingsRevisions[sampleCommittee.CommitteeBase.UID] = 1

		// Add another sample committee
		sampleCommittee2 := &model.Committee{
			CommitteeBase: model.CommitteeBase{
				UID:             "committee-2",
				ProjectUID:      "7cad5a8d-19d0-41a4-81a6-043453daf9ee",
				Name:            "Security Committee",
				Category:        "security",
				Description:     "Handles security-related matters",
				EnableVoting:    false,
				SSOGroupEnabled: true,
				SSOGroupName:    "7cad5a8d-19d0-41a4-81a6-043453daf9ee-security-committee",
				RequiresReview:  true,
				Public:          true,
				Calendar: model.Calendar{
					Public: false,
				},
				DisplayName:      "Security",
				TotalMembers:     3,
				TotalVotingRepos: 1,
				CreatedAt:        now.Add(-12 * time.Hour),
				UpdatedAt:        now,
			},
			CommitteeSettings: &model.CommitteeSettings{
				UID:                   "committee-2",
				BusinessEmailRequired: false,
				Writers: []model.CommitteeUser{
					{Username: "security@example.com", Email: "security@example.com"},
				},
				Auditors: []model.CommitteeUser{
					{Username: "auditor1@example.com", Email: "auditor1@example.com"},
				},
				CreatedAt: now.Add(-12 * time.Hour),
				UpdatedAt: now,
			},
		}

		mock.committees[sampleCommittee2.CommitteeBase.UID] = sampleCommittee2
		mock.committeeSettings[sampleCommittee2.CommitteeBase.UID] = sampleCommittee2.CommitteeSettings
		mock.committeeIndexKeys[sampleCommittee2.BuildIndexKey(ctx)] = sampleCommittee2
		mock.committeeRevisions[sampleCommittee2.CommitteeBase.UID] = 1
		mock.settingsRevisions[sampleCommittee2.CommitteeBase.UID] = 1

		// Add sample committee members
		sampleMember1 := &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:          "member-1",
				CommitteeUID: "committee-1",
				Username:     "john.doe",
				Email:        "john.doe@example.com",
				FirstName:    "John",
				LastName:     "Doe",
				JobTitle:     "Senior Developer",
				Role: model.CommitteeMemberRole{
					Name:      "Chair",
					StartDate: "2023-01-01",
					EndDate:   "2024-12-31",
				},
				AppointedBy: "Community",
				Status:      "Active",
				Voting: model.CommitteeMemberVotingInfo{
					Status:    "Voting Rep",
					StartDate: "2023-01-01",
					EndDate:   "2024-12-31",
				},
				Organization: model.CommitteeMemberOrganization{
					Name:    "Example Corp",
					Website: "https://example.com",
				},
				CreatedAt: now.Add(-24 * time.Hour),
				UpdatedAt: now,
			},
		}

		sampleMember2 := &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:       "member-2",
				Username:  "jane.smith",
				Email:     "jane.smith@example.com",
				FirstName: "Jane",
				LastName:  "Smith",
				JobTitle:  "Security Engineer",
				Role: model.CommitteeMemberRole{
					Name:      "Secretary",
					StartDate: "2023-06-01",
				},
				AppointedBy: "Vote of TSC Committee",
				Status:      "Active",
				Voting: model.CommitteeMemberVotingInfo{
					Status:    "Observer",
					StartDate: "2023-06-01",
				},
				Organization: model.CommitteeMemberOrganization{
					Name:    "Security Inc",
					Website: "https://security-inc.com",
				},
				CreatedAt: now.Add(-12 * time.Hour),
				UpdatedAt: now,
			},
		}

		// Add members to committee-1
		mock.committeeMembers["committee-1"] = make(map[string]*model.CommitteeMember)
		mock.memberIndexKeys["committee-1"] = make(map[string]*model.CommitteeMember)
		mock.committeeMembers["committee-1"][sampleMember1.UID] = sampleMember1
		mock.committeeMembers["committee-1"][sampleMember2.UID] = sampleMember2
		mock.memberIndexKeys["committee-1"][sampleMember1.BuildIndexKey(ctx)] = sampleMember1
		mock.memberIndexKeys["committee-1"][sampleMember2.BuildIndexKey(ctx)] = sampleMember2
		mock.memberRevisions[sampleMember1.UID] = 1
		mock.memberRevisions[sampleMember2.UID] = 1

		globalMockRepo = mock
	})

	return globalMockRepo
}

// MockRepository provides a mock implementation of all repository interfaces for testing
type MockRepository struct {
	committees         map[string]*model.Committee
	committeeSettings  map[string]*model.CommitteeSettings
	committeeMembers   map[string]map[string]*model.CommitteeMember // committeeUID -> memberUID -> member
	projectSlugs       map[string]string                            // projectUID -> slug
	projectNames       map[string]string                            // projectUID -> name
	projectWriters     map[string][]model.CommitteeUser             // projectUID -> writers
	committeeIndexKeys map[string]*model.Committee                  // indexKey -> committee
	memberIndexKeys    map[string]map[string]*model.CommitteeMember // committeeUID -> indexKey -> member
	// Invite and application storage
	committeeInvites      map[string]*model.CommitteeInvite      // inviteUID -> invite
	committeeApplications map[string]*model.CommitteeApplication // applicationUID -> application
	inviteIndexKeys       map[string]*model.CommitteeInvite      // indexKey -> invite
	applicationIndexKeys  map[string]*model.CommitteeApplication // indexKey -> application
	// Revision tracking for optimistic locking
	committeeRevisions   map[string]uint64 // committeeUID -> revision
	settingsRevisions    map[string]uint64 // committeeUID -> settings revision
	memberRevisions      map[string]uint64 // memberUID -> revision
	inviteRevisions      map[string]uint64 // inviteUID -> revision
	applicationRevisions map[string]uint64 // applicationUID -> revision
	mu                   sync.RWMutex      // Protect concurrent access to maps
}

// ================== CommitteeBaseReader implementation ==================

// GetBase retrieves a committee base by UID
func (m *MockRepository) GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error) {
	slog.DebugContext(ctx, "mock repository: getting committee base", "uid", uid)

	m.mu.RLock()
	defer m.mu.RUnlock()

	committee, exists := m.committees[uid]
	if !exists {
		return nil, 0, errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", uid))
	}

	// Return a copy of the CommitteeBase to avoid data races
	baseCopy := committee.CommitteeBase
	// Return version 1 for mock (in real implementation this would be the actual version)
	return &baseCopy, 1, nil
}

// GetRevision retrieves the revision number for a committee by UID
func (m *MockRepository) GetRevision(ctx context.Context, uid string) (uint64, error) {
	slog.DebugContext(ctx, "mock repository: getting committee revision", "uid", uid)

	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.committees[uid]
	if !exists {
		return 0, errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", uid))
	}

	// Return version 1 for mock (in real implementation this would be the actual revision)
	return 1, nil
}

// ListAllUIDs returns all committee UIDs from the mock repository.
func (m *MockRepository) ListAllUIDs(ctx context.Context) ([]string, error) {
	slog.DebugContext(ctx, "mock repository: listing all committee UIDs")

	m.mu.RLock()
	defer m.mu.RUnlock()

	uids := make([]string, 0, len(m.committees))
	for uid := range m.committees {
		uids = append(uids, uid)
	}
	return uids, nil
}

// ================== CommitteeSettingsReader implementation ==================

// GetSettings retrieves committee settings by committee UID
func (m *MockRepository) GetSettings(ctx context.Context, committeeUID string) (*model.CommitteeSettings, uint64, error) {
	slog.DebugContext(ctx, "mock repository: getting committee settings", "committee_uid", committeeUID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	settings, exists := m.committeeSettings[committeeUID]
	if !exists {
		return nil, 0, errors.NewNotFound(fmt.Sprintf("committee settings for UID %s not found", committeeUID))
	}

	if settings == nil {
		// Committee exists but was stored without a CommitteeSettings pointer.
		// Treat this identically to "not found" so callers cannot distinguish a nil
		// value from a missing key — consistent with real NATS storage behavior.
		return nil, 0, errors.NewNotFound(fmt.Sprintf("committee settings for UID %s not found", committeeUID))
	}

	// Return a deep copy so caller mutations (e.g. in-place field promotion) do not bleed
	// back into the stored pointer and corrupt subsequent reads in the same test.
	settingsCopy := *settings
	settingsCopy.Writers = append([]model.CommitteeUser(nil), settings.Writers...)
	settingsCopy.Auditors = append([]model.CommitteeUser(nil), settings.Auditors...)
	return &settingsCopy, 1, nil
}

// ================== CommitteeMemberReader implementation ==================

// GetMember retrieves a committee member by member UID
func (m *MockRepository) GetMember(ctx context.Context, memberUID string) (*model.CommitteeMember, uint64, error) {
	slog.DebugContext(ctx, "mock repository: getting committee member", "member_uid", memberUID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Search across all committees for the member
	for _, committeeMembers := range m.committeeMembers {
		if member, exists := committeeMembers[memberUID]; exists {
			// Return a copy to avoid data races
			memberCopy := *member
			revision := m.memberRevisions[memberUID]
			if revision == 0 {
				revision = 1
			}
			return &memberCopy, revision, nil
		}
	}

	return nil, 0, errors.NewNotFound(fmt.Sprintf("member with UID %s not found", memberUID))
}

// GetMemberRevision retrieves the revision number for a committee member
func (m *MockRepository) GetMemberRevision(ctx context.Context, memberUID string) (uint64, error) {
	slog.DebugContext(ctx, "mock repository: getting member revision", "member_uid", memberUID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check if member exists across all committees
	for _, committeeMembers := range m.committeeMembers {
		if _, exists := committeeMembers[memberUID]; exists {
			revision := m.memberRevisions[memberUID]
			if revision == 0 {
				revision = 1
			}
			return revision, nil
		}
	}

	return 0, errors.NewNotFound(fmt.Sprintf("member with UID %s not found", memberUID))
}

// ListMembersByCommittee retrieves all members for a committee
func (m *MockRepository) ListMembersByCommittee(ctx context.Context, committeeUID string) ([]*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "mock repository: listing committee members", "committee_uid", committeeUID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	committeeMembers, exists := m.committeeMembers[committeeUID]
	if !exists {
		return []*model.CommitteeMember{}, nil // Return empty slice if no members
	}

	members := make([]*model.CommitteeMember, 0, len(committeeMembers))
	for _, member := range committeeMembers {
		// Return copies to avoid data races
		memberCopy := *member
		members = append(members, &memberCopy)
	}

	return members, nil
}

// ListMembersByOrganization retrieves all members held by an organization (by the SFID on
// committee_member.organization.id) across all committees (Org Lens, LFXV2-1865).
func (m *MockRepository) ListMembersByOrganization(ctx context.Context, orgSFID string) ([]*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "mock repository: listing committee members by organization", "org_sfid", orgSFID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Mirror the real NATS KV index (storage.go), which keys on the 18-char canonical SFID: normalize
	// both sides so a 15-char stored organization.id still matches an 18-char orgSFID (same record).
	normalizedOrg := utils.NormalizeAccountSFID(orgSFID)
	var members []*model.CommitteeMember
	for _, committeeMembers := range m.committeeMembers {
		for _, member := range committeeMembers {
			if member.Organization.ID == "" || utils.NormalizeAccountSFID(member.Organization.ID) != normalizedOrg {
				continue
			}
			memberCopy := *member
			members = append(members, &memberCopy)
		}
	}
	return members, nil
}

// ListAllMembers retrieves all members across all committees (full scan).
// Used for backfill/repair operations that need to read all members regardless of the index.
func (m *MockRepository) ListAllMembers(ctx context.Context) ([]*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "mock repository: listing all committee members")

	m.mu.RLock()
	defer m.mu.RUnlock()

	var members []*model.CommitteeMember
	for _, committeeMembers := range m.committeeMembers {
		for _, member := range committeeMembers {
			memberCopy := *member
			members = append(members, &memberCopy)
		}
	}
	return members, nil
}

// EachMember streams every mock member to fn one at a time (mirrors the storage streaming scan). The
// snapshot is copied under the read lock, then fn is invoked outside the lock.
func (m *MockRepository) EachMember(ctx context.Context, fn func(*model.CommitteeMember) error) error {
	members, err := m.ListAllMembers(ctx)
	if err != nil {
		return err
	}
	for _, member := range members {
		if err := fn(member); err != nil {
			return err
		}
	}
	return nil
}

// ListMembersByEmail retrieves all committee members whose normalized email matches the given
// address, scanning all committees. Mirrors the real storage index behavior for tests.
func (m *MockRepository) ListMembersByEmail(ctx context.Context, email string) ([]*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "mock repository: listing committee members by email")

	normalized := strings.TrimSpace(strings.ToLower(email))
	if normalized == "" {
		return nil, errors.NewValidation("email cannot be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	var members []*model.CommitteeMember
	for _, committeeMembers := range m.committeeMembers {
		for _, member := range committeeMembers {
			if strings.TrimSpace(strings.ToLower(member.Email)) != normalized {
				continue
			}
			memberCopy := *member
			members = append(members, &memberCopy)
		}
	}
	return members, nil
}

// MockCommitteeWriter implements CommitteeWriter interface
type MockCommitteeWriter struct {
	mock *MockRepository
}

// ================== CommitteeBaseWriter implementation ==================

// Create creates a new committee
func (w *MockCommitteeWriter) Create(ctx context.Context, committee *model.Committee) error {
	slog.DebugContext(ctx, "mock committee writer: creating committee")

	committee.CommitteeBase.UID = uuid.New().String()

	now := time.Now()
	committee.CommitteeBase.CreatedAt = now
	committee.CommitteeBase.UpdatedAt = now

	// Create committee settings as well
	committee.CommitteeSettings.UID = committee.CommitteeBase.UID
	committee.CommitteeSettings.CreatedAt = now
	committee.CommitteeSettings.UpdatedAt = now

	// Store committee and settings
	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	w.mock.committees[committee.CommitteeBase.UID] = committee
	w.mock.committeeSettings[committee.CommitteeBase.UID] = committee.CommitteeSettings
	w.mock.committeeIndexKeys[committee.BuildIndexKey(ctx)] = committee
	w.mock.committeeRevisions[committee.CommitteeBase.UID] = 1
	w.mock.settingsRevisions[committee.CommitteeBase.UID] = 1

	return nil
}

// UpdateBase updates an existing committee base
func (w *MockCommitteeWriter) UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error {
	slog.DebugContext(ctx, "mock committee writer: updating committee base", "uid", committee.CommitteeBase.UID, "revision", revision)

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	// Check if committee exists
	if _, exists := w.mock.committees[committee.CommitteeBase.UID]; !exists {
		return errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", committee.CommitteeBase.UID))
	}

	committee.CommitteeBase.UpdatedAt = time.Now()
	w.mock.committees[committee.CommitteeBase.UID] = committee
	w.mock.committeeIndexKeys[committee.BuildIndexKey(ctx)] = committee

	return nil
}

// Delete deletes a committee and its settings
func (w *MockCommitteeWriter) Delete(ctx context.Context, uid string, revision uint64) error {
	slog.DebugContext(ctx, "mock committee writer: deleting committee", "uid", uid, "revision", revision)

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	// Check if committee exists and get it to obtain the index key
	committee, exists := w.mock.committees[uid]
	if !exists {
		return errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", uid))
	}

	// Get the index key before deleting
	indexKey := committee.BuildIndexKey(ctx)

	// Delete committee and its settings
	delete(w.mock.committees, uid)
	delete(w.mock.committeeSettings, uid)
	delete(w.mock.committeeIndexKeys, indexKey)
	delete(w.mock.committeeRevisions, uid)
	delete(w.mock.settingsRevisions, uid)

	return nil
}

// UniqueNameProject verifies if a committee with the same name and project exists
// Returns conflict error if found (for uniqueness checking)
// Returns (key, nil) when the name/project combination is unique (matches NATS storage behavior)
func (w *MockCommitteeWriter) UniqueNameProject(ctx context.Context, committee *model.Committee) (string, error) {
	nameProjectKey := committee.BuildIndexKey(ctx)
	slog.DebugContext(ctx, "mock committee writer: checking uniqueness by name project key", "name_project_key", nameProjectKey)

	w.mock.mu.RLock()
	defer w.mock.mu.RUnlock()

	existing, exists := w.mock.committeeIndexKeys[nameProjectKey]
	if exists {
		// Return conflict error to indicate non-uniqueness
		return existing.CommitteeBase.UID, errors.NewConflict(fmt.Sprintf("committee with name project key %s already exists", nameProjectKey))
	}

	// Return nil error when unique (matches NATS KV Create behavior)
	return nameProjectKey, nil
}

// UpdateHasMailingList implements CommitteeBaseWriter.
func (w *MockCommitteeWriter) UpdateHasMailingList(_ context.Context, uid string, hasMailingList bool) (*model.CommitteeBase, bool, error) {
	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	committee, exists := w.mock.committees[uid]
	if !exists {
		return nil, false, errors.NewNotFound(fmt.Sprintf("committee with UID %s not found", uid))
	}

	if committee.HasMailingList == hasMailingList {
		return nil, false, nil
	}

	committee.HasMailingList = hasMailingList
	committee.CommitteeBase.UpdatedAt = time.Now()
	return &committee.CommitteeBase, true, nil
}

// UniqueSSOGroupName verifies if a committee with the same SSO group name exists
// Returns conflict error if found (for uniqueness checking)
func (w *MockCommitteeWriter) UniqueSSOGroupName(ctx context.Context, committee *model.Committee) (string, error) {
	slog.DebugContext(ctx, "mock committee writer: checking uniqueness by SSO group name", "name", committee.SSOGroupName)

	w.mock.mu.RLock()
	defer w.mock.mu.RUnlock()

	for _, existing := range w.mock.committees {
		if existing.SSOGroupName == committee.SSOGroupName {
			// Return conflict error to indicate non-uniqueness
			return existing.CommitteeBase.UID, errors.NewConflict(fmt.Sprintf("committee with SSO group name %s already exists", committee.SSOGroupName))
		}
	}

	// Return nil error when unique (matches NATS KV Create behavior)
	ssoKey := "sso:" + committee.SSOGroupName
	return ssoKey, nil
}

// ================== CommitteeSettingsWriter implementation ==================

// UpdateSetting updates committee settings
func (w *MockCommitteeWriter) UpdateSetting(ctx context.Context, settings *model.CommitteeSettings, revision uint64) error {
	slog.DebugContext(ctx, "mock committee writer: updating settings", "committee_uid", settings.UID, "revision", revision)

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	// Check if committee settings exist
	if _, exists := w.mock.committeeSettings[settings.UID]; !exists {
		return errors.NewNotFound(fmt.Sprintf("committee settings for UID %s not found", settings.UID))
	}

	settings.UpdatedAt = time.Now()
	w.mock.committeeSettings[settings.UID] = settings

	// Also update the settings in the committee
	if committee, exists := w.mock.committees[settings.UID]; exists {
		committee.CommitteeSettings = settings
		committee.CommitteeBase.UpdatedAt = time.Now()
		w.mock.committeeIndexKeys[committee.BuildIndexKey(ctx)] = committee
	}

	return nil
}

// ================== CommitteeMemberWriter implementation ==================

// CreateMember creates a new committee member
func (w *MockCommitteeWriter) CreateMember(ctx context.Context, member *model.CommitteeMember) error {
	slog.DebugContext(ctx, "mock committee writer: creating committee member", "member_uid", member.UID, "email", member.Email)

	// Generate UID if not set
	if member.UID == "" {
		member.UID = uuid.New().String()
	}

	now := time.Now()
	member.CreatedAt = now
	member.UpdatedAt = now

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	if member.CommitteeUID == "" {
		return errors.NewValidation("committee UID is required for member creation")
	}
	committeeUID := member.CommitteeUID

	// Initialize committee members map if it doesn't exist
	if w.mock.committeeMembers[committeeUID] == nil {
		w.mock.committeeMembers[committeeUID] = make(map[string]*model.CommitteeMember)
	}
	if w.mock.memberIndexKeys[committeeUID] == nil {
		w.mock.memberIndexKeys[committeeUID] = make(map[string]*model.CommitteeMember)
	}

	// Store member
	w.mock.committeeMembers[committeeUID][member.UID] = member
	w.mock.memberIndexKeys[committeeUID][member.BuildIndexKey(ctx)] = member
	w.mock.memberRevisions[member.UID] = 1

	return nil
}

// UpdateMember updates an existing committee member
func (w *MockCommitteeWriter) UpdateMember(ctx context.Context, member *model.CommitteeMember, revision uint64) (*model.CommitteeMember, error) {
	slog.DebugContext(ctx, "mock committee writer: updating committee member", "member_uid", member.UID, "revision", revision)

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	// Find the member across all committees
	var foundCommitteeUID string
	for committeeUID, committeeMembers := range w.mock.committeeMembers {
		if _, exists := committeeMembers[member.UID]; exists {
			foundCommitteeUID = committeeUID
			break
		}
	}

	if foundCommitteeUID == "" {
		return nil, errors.NewNotFound(fmt.Sprintf("member with UID %s not found", member.UID))
	}

	member.UpdatedAt = time.Now()
	w.mock.committeeMembers[foundCommitteeUID][member.UID] = member
	w.mock.memberIndexKeys[foundCommitteeUID][member.BuildIndexKey(ctx)] = member

	// Update revision
	currentRevision := w.mock.memberRevisions[member.UID]
	w.mock.memberRevisions[member.UID] = currentRevision + 1

	return member, nil
}

// DeleteMember removes a committee member
func (w *MockCommitteeWriter) DeleteMember(ctx context.Context, memberUID string, revision uint64) error {
	slog.DebugContext(ctx, "mock committee writer: deleting committee member", "member_uid", memberUID, "revision", revision)

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	// Find the member across all committees
	var foundCommitteeUID string
	var member *model.CommitteeMember
	for committeeUID, committeeMembers := range w.mock.committeeMembers {
		if m, exists := committeeMembers[memberUID]; exists {
			foundCommitteeUID = committeeUID
			member = m
			break
		}
	}

	if foundCommitteeUID == "" {
		return errors.NewNotFound(fmt.Sprintf("member with UID %s not found", memberUID))
	}

	// Get the index key before deleting
	indexKey := member.BuildIndexKey(ctx)

	// Delete member
	delete(w.mock.committeeMembers[foundCommitteeUID], memberUID)
	delete(w.mock.memberIndexKeys[foundCommitteeUID], indexKey)
	delete(w.mock.memberRevisions, memberUID)

	return nil
}

// UniqueMember verifies if a member is unique (based on email or other unique identifiers)
func (w *MockCommitteeWriter) UniqueMember(ctx context.Context, member *model.CommitteeMember) (string, error) {
	slog.DebugContext(ctx, "mock committee writer: checking member uniqueness", "member_uid", member.UID, "email", member.Email)

	w.mock.mu.RLock()
	defer w.mock.mu.RUnlock()

	// Check across all committees for existing member with same email
	for _, committeeMembers := range w.mock.committeeMembers {
		for _, existing := range committeeMembers {
			if existing.Email == member.Email && existing.UID != member.UID {
				// Return conflict error to indicate non-uniqueness
				return existing.UID, errors.NewConflict(fmt.Sprintf("member with email %s already exists", member.Email))
			}
		}
	}

	return "", nil
}

// IndexMemberByCommittee records the secondary index entry for the given member.
// In the mock this is a no-op (the mock keyed map already indexes by committee);
// it returns the key string that the real storage would write, so callers can
// track it for rollback.
func (w *MockCommitteeWriter) IndexMemberByCommittee(ctx context.Context, member *model.CommitteeMember) (string, error) {
	slog.DebugContext(ctx, "mock committee writer: indexing member by committee",
		"committee_uid", member.CommitteeUID,
		"member_uid", member.UID,
	)
	key := fmt.Sprintf(constants.KVLookupMembersByCommitteePrefix, member.CommitteeUID, member.UID)
	return key, nil
}

// IndexMemberByOrganization records the by-organization secondary index entry (Org Lens, LFXV2-1865).
// In the mock this is a no-op; it returns the key the real storage would write (empty when the member
// has no organization.id) so callers can track it for rollback.
func (w *MockCommitteeWriter) IndexMemberByOrganization(ctx context.Context, member *model.CommitteeMember) (string, error) {
	slog.DebugContext(ctx, "mock committee writer: indexing member by organization",
		"organization_id", member.Organization.ID,
		"member_uid", member.UID,
	)
	// Mirror the real NATS storage: normalize to the 18-char canonical SFID so the mock key matches
	// production. A 15-char stored org id would otherwise yield a divergent key and break rollback /
	// stale-key tracking against real behavior.
	orgSFID := utils.NormalizeAccountSFID(member.Organization.ID)
	if orgSFID == "" {
		return "", nil
	}
	key := fmt.Sprintf(constants.KVLookupMembersByOrganizationPrefix, orgSFID, member.UID)
	return key, nil
}

// IndexMemberByEmail records the by-email secondary index entry. In the mock this is a no-op; it
// returns the key the real storage would write (empty when the member has no email) so callers can
// track it for rollback.
func (w *MockCommitteeWriter) IndexMemberByEmail(ctx context.Context, member *model.CommitteeMember) (string, error) {
	slog.DebugContext(ctx, "mock committee writer: indexing member by email",
		"member_uid", member.UID,
	)
	hash := member.BuildEmailIndexKey(ctx)
	if hash == "" {
		return "", nil
	}
	key := fmt.Sprintf(constants.KVLookupMembersByEmailPrefix, hash, member.UID)
	return key, nil
}

// ================== CommitteeInviteReader implementation ==================

// GetInvite retrieves a committee invite by UID
func (m *MockRepository) GetInvite(ctx context.Context, uid string) (*model.CommitteeInvite, uint64, error) {
	slog.DebugContext(ctx, "mock repository: getting committee invite", "uid", uid)

	m.mu.RLock()
	defer m.mu.RUnlock()

	invite, exists := m.committeeInvites[uid]
	if !exists {
		return nil, 0, errors.NewNotFound(fmt.Sprintf("invite with UID %s not found", uid))
	}

	inviteCopy := *invite
	revision := m.inviteRevisions[uid]
	if revision == 0 {
		revision = 1
	}
	return &inviteCopy, revision, nil
}

// ListInvites retrieves all invites for a given committee UID
func (m *MockRepository) ListInvites(ctx context.Context, committeeUID string) ([]*model.CommitteeInvite, error) {
	slog.DebugContext(ctx, "mock repository: listing committee invites", "committee_uid", committeeUID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	var invites []*model.CommitteeInvite
	for _, invite := range m.committeeInvites {
		if invite.CommitteeUID == committeeUID {
			inviteCopy := *invite
			invites = append(invites, &inviteCopy)
		}
	}
	if invites == nil {
		invites = []*model.CommitteeInvite{}
	}
	return invites, nil
}

// ListAllInvites retrieves every invite across all committees.
func (m *MockRepository) ListAllInvites(ctx context.Context) ([]*model.CommitteeInvite, error) {
	slog.DebugContext(ctx, "mock repository: listing all committee invites")

	m.mu.RLock()
	defer m.mu.RUnlock()

	invites := make([]*model.CommitteeInvite, 0, len(m.committeeInvites))
	for _, invite := range m.committeeInvites {
		inviteCopy := *invite
		invites = append(invites, &inviteCopy)
	}
	return invites, nil
}

// ================== CommitteeApplicationReader implementation ==================

// GetApplication retrieves a committee application by UID
func (m *MockRepository) GetApplication(ctx context.Context, uid string) (*model.CommitteeApplication, uint64, error) {
	slog.DebugContext(ctx, "mock repository: getting committee application", "uid", uid)

	m.mu.RLock()
	defer m.mu.RUnlock()

	application, exists := m.committeeApplications[uid]
	if !exists {
		return nil, 0, errors.NewNotFound(fmt.Sprintf("application with UID %s not found", uid))
	}

	applicationCopy := *application
	revision := m.applicationRevisions[uid]
	if revision == 0 {
		revision = 1
	}
	return &applicationCopy, revision, nil
}

// ListApplications retrieves all applications for a given committee UID
func (m *MockRepository) ListApplications(ctx context.Context, committeeUID string) ([]*model.CommitteeApplication, error) {
	slog.DebugContext(ctx, "mock repository: listing committee applications", "committee_uid", committeeUID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	var applications []*model.CommitteeApplication
	for _, application := range m.committeeApplications {
		if application.CommitteeUID == committeeUID {
			applicationCopy := *application
			applications = append(applications, &applicationCopy)
		}
	}
	if applications == nil {
		applications = []*model.CommitteeApplication{}
	}
	return applications, nil
}

// ================== CommitteeInviteWriter implementation ==================

// CreateInvite creates a new committee invite
func (w *MockCommitteeWriter) CreateInvite(ctx context.Context, invite *model.CommitteeInvite) error {
	slog.DebugContext(ctx, "mock committee writer: creating invite", "invite_uid", invite.UID)

	if invite.UID == "" {
		invite.UID = uuid.New().String()
	}
	invite.CreatedAt = time.Now()

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	w.mock.committeeInvites[invite.UID] = invite
	w.mock.inviteIndexKeys[invite.BuildIndexKey(ctx)] = invite
	w.mock.inviteRevisions[invite.UID] = 1
	return nil
}

// UpdateInvite updates an existing committee invite
func (w *MockCommitteeWriter) UpdateInvite(ctx context.Context, invite *model.CommitteeInvite, revision uint64) error {
	slog.DebugContext(ctx, "mock committee writer: updating invite", "invite_uid", invite.UID, "revision", revision)

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	if _, exists := w.mock.committeeInvites[invite.UID]; !exists {
		return errors.NewNotFound(fmt.Sprintf("invite with UID %s not found", invite.UID))
	}

	w.mock.committeeInvites[invite.UID] = invite
	w.mock.inviteIndexKeys[invite.BuildIndexKey(ctx)] = invite
	w.mock.inviteRevisions[invite.UID] = revision + 1
	return nil
}

// UniqueInvite checks if an invite is unique
func (w *MockCommitteeWriter) UniqueInvite(ctx context.Context, invite *model.CommitteeInvite) (string, error) {
	slog.DebugContext(ctx, "mock committee writer: checking invite uniqueness")

	w.mock.mu.RLock()
	defer w.mock.mu.RUnlock()

	indexKey := invite.BuildIndexKey(ctx)
	if _, exists := w.mock.inviteIndexKeys[indexKey]; exists {
		return indexKey, errors.NewConflict("invite already exists")
	}
	return indexKey, nil
}

// ================== CommitteeApplicationWriter implementation ==================

// CreateApplication creates a new committee application
func (w *MockCommitteeWriter) CreateApplication(ctx context.Context, application *model.CommitteeApplication) error {
	slog.DebugContext(ctx, "mock committee writer: creating application", "application_uid", application.UID)

	if application.UID == "" {
		application.UID = uuid.New().String()
	}
	application.CreatedAt = time.Now()

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	w.mock.committeeApplications[application.UID] = application
	w.mock.applicationIndexKeys[application.BuildIndexKey(ctx)] = application
	w.mock.applicationRevisions[application.UID] = 1
	return nil
}

// UpdateApplication updates an existing committee application
func (w *MockCommitteeWriter) UpdateApplication(ctx context.Context, application *model.CommitteeApplication, revision uint64) error {
	slog.DebugContext(ctx, "mock committee writer: updating application", "application_uid", application.UID, "revision", revision)

	w.mock.mu.Lock()
	defer w.mock.mu.Unlock()

	if _, exists := w.mock.committeeApplications[application.UID]; !exists {
		return errors.NewNotFound(fmt.Sprintf("application with UID %s not found", application.UID))
	}

	w.mock.committeeApplications[application.UID] = application
	w.mock.applicationIndexKeys[application.BuildIndexKey(ctx)] = application
	w.mock.applicationRevisions[application.UID] = revision + 1
	return nil
}

// UniqueApplication checks if an application is unique
func (w *MockCommitteeWriter) UniqueApplication(ctx context.Context, application *model.CommitteeApplication) (string, error) {
	slog.DebugContext(ctx, "mock committee writer: checking application uniqueness")

	w.mock.mu.RLock()
	defer w.mock.mu.RUnlock()

	indexKey := application.BuildIndexKey(ctx)
	if _, exists := w.mock.applicationIndexKeys[indexKey]; exists {
		return indexKey, errors.NewConflict("application already exists")
	}
	return indexKey, nil
}

// MockProjectRetriever implements ProjectRetriever interface
type MockProjectRetriever struct {
	mock *MockRepository
}

// Name returns the project name for a given UID
func (r *MockProjectRetriever) Name(ctx context.Context, uid string) (string, error) {
	slog.DebugContext(ctx, "mock project retriever: getting name", "uid", uid)

	r.mock.mu.RLock()
	defer r.mock.mu.RUnlock()

	name, exists := r.mock.projectNames[uid]
	if !exists {
		return "", errors.NewNotFound(fmt.Sprintf("project with UID %s not found", uid))
	}

	return name, nil
}

// Slug returns the project slug for a given UID
func (r *MockProjectRetriever) Slug(ctx context.Context, uid string) (string, error) {
	slog.DebugContext(ctx, "mock project retriever: getting slug", "uid", uid)

	r.mock.mu.RLock()
	defer r.mock.mu.RUnlock()

	slug, exists := r.mock.projectSlugs[uid]
	if !exists {
		return "", errors.NewNotFound(fmt.Sprintf("project with UID %s not found", uid))
	}

	return slug, nil
}

// Writers returns the writers list for a given project UID.
func (r *MockProjectRetriever) Writers(ctx context.Context, uid string) ([]model.CommitteeUser, error) {
	slog.DebugContext(ctx, "mock project retriever: getting writers", "uid", uid)

	r.mock.mu.RLock()
	defer r.mock.mu.RUnlock()

	writers, exists := r.mock.projectWriters[uid]
	if !exists {
		return []model.CommitteeUser{}, nil
	}

	return writers, nil
}

// Helper functions

// NewMockCommitteeReader creates a mock committee reader
func NewMockCommitteeReader(mock *MockRepository) port.CommitteeReader {
	return mock
}

// NewMockCommitteeWriter creates a mock committee writer
func NewMockCommitteeWriter(mock *MockRepository) port.CommitteeWriter {
	return &MockCommitteeWriter{mock: mock}
}

// NewMockCommitteeReaderWriter creates a mock committee reader writer
func NewMockCommitteeReaderWriter(mock *MockRepository) port.CommitteeReaderWriter {
	return &MockCommitteeReaderWriter{
		CommitteeReader: mock,
		CommitteeWriter: &MockCommitteeWriter{mock: mock},
	}
}

// MockCommitteeReaderWriter combines reader and writer functionality
type MockCommitteeReaderWriter struct {
	port.CommitteeReader
	port.CommitteeWriter
}

// IsReady checks if the committee reader writer is ready
func (m *MockCommitteeReaderWriter) IsReady(ctx context.Context) error {
	// Mock implementation - always return nil (ready)
	return nil
}

// NewMockProjectRetriever creates a mock project retriever
func NewMockProjectRetriever(mock *MockRepository) port.ProjectReader {
	return &MockProjectRetriever{mock: mock}
}

// Utility functions for mock data manipulation

// AddCommittee adds a committee to the mock data (useful for testing)
func (m *MockRepository) AddCommittee(committee *model.Committee) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.committees[committee.CommitteeBase.UID] = committee
	m.committeeSettings[committee.CommitteeBase.UID] = committee.CommitteeSettings
	m.committeeIndexKeys[committee.BuildIndexKey(context.Background())] = committee
	m.committeeRevisions[committee.CommitteeBase.UID] = 1
	m.settingsRevisions[committee.CommitteeBase.UID] = 1
}

// AddProjectSlug adds a project slug mapping (useful for testing)
func (m *MockRepository) AddProjectSlug(uid, slug string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.projectSlugs[uid] = slug
}

// AddProjectName adds a project name mapping (useful for testing)
func (m *MockRepository) AddProjectName(uid, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.projectNames[uid] = name
}

// AddProjectWriters sets the writers list for a project UID (useful for testing).
func (m *MockRepository) AddProjectWriters(uid string, writers []model.CommitteeUser) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.projectWriters[uid] = writers
}

// AddProject adds both project slug and name mappings (useful for testing)
func (m *MockRepository) AddProject(uid, slug, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.projectSlugs[uid] = slug
	m.projectNames[uid] = name
}

// ClearAll clears all mock data (useful for testing)
func (m *MockRepository) ClearAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.committees = make(map[string]*model.Committee)
	m.committeeSettings = make(map[string]*model.CommitteeSettings)
	m.committeeMembers = make(map[string]map[string]*model.CommitteeMember)
	m.projectSlugs = make(map[string]string)
	m.projectNames = make(map[string]string)
	m.committeeIndexKeys = make(map[string]*model.Committee)
	m.memberIndexKeys = make(map[string]map[string]*model.CommitteeMember)
	m.committeeInvites = make(map[string]*model.CommitteeInvite)
	m.committeeApplications = make(map[string]*model.CommitteeApplication)
	m.inviteIndexKeys = make(map[string]*model.CommitteeInvite)
	m.applicationIndexKeys = make(map[string]*model.CommitteeApplication)
	m.committeeRevisions = make(map[string]uint64)
	m.settingsRevisions = make(map[string]uint64)
	m.memberRevisions = make(map[string]uint64)
	m.inviteRevisions = make(map[string]uint64)
	m.applicationRevisions = make(map[string]uint64)
}

// GetCommitteeCount returns the total number of committees
func (m *MockRepository) GetCommitteeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.committees)
}

// AddCommitteeMember adds a committee member to the mock data (useful for testing)
func (m *MockRepository) AddCommitteeMember(committeeUID string, member *model.CommitteeMember) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.committeeMembers[committeeUID] == nil {
		m.committeeMembers[committeeUID] = make(map[string]*model.CommitteeMember)
	}
	if m.memberIndexKeys[committeeUID] == nil {
		m.memberIndexKeys[committeeUID] = make(map[string]*model.CommitteeMember)
	}

	m.committeeMembers[committeeUID][member.UID] = member
	m.memberIndexKeys[committeeUID][member.BuildIndexKey(context.Background())] = member
	m.memberRevisions[member.UID] = 1
}

// GetCommitteeMemberCount returns the total number of members for a committee
func (m *MockRepository) GetCommitteeMemberCount(committeeUID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if committeeMembers, exists := m.committeeMembers[committeeUID]; exists {
		return len(committeeMembers)
	}
	return 0
}

// MockCommitteePublisher implements CommitteePublisher interface for testing
type MockCommitteePublisher struct {
	mu sync.Mutex
	// LastIndexerMessage captures the most recently published indexer message for assertions.
	LastIndexerMessage any
}

// Indexer simulates publishing an indexer message
func (p *MockCommitteePublisher) Indexer(ctx context.Context, subject string, message any, synced bool) error {
	p.mu.Lock()
	p.LastIndexerMessage = message
	p.mu.Unlock()
	slog.InfoContext(ctx, "mock publisher: indexer message published",
		"subject", subject,
		"message_type", "indexer",
		"sync", synced,
	)
	return nil
}

// Access simulates publishing an access message
func (p *MockCommitteePublisher) Access(ctx context.Context, subject string, message any, sync bool) error {
	slog.InfoContext(ctx, "mock publisher: access message published",
		"subject", subject,
		"message_type", "access",
		"sync", sync,
	)
	return nil
}

// Event simulates publishing an event message
func (p *MockCommitteePublisher) Event(ctx context.Context, subject string, event any, sync bool) error {
	slog.InfoContext(ctx, "mock publisher: event message published",
		"subject", subject,
		"message_type", "event",
		"sync", sync,
	)
	return nil
}

// NewMockCommitteePublisher creates a mock committee publisher
func NewMockCommitteePublisher() port.CommitteePublisher {
	return &MockCommitteePublisher{}
}

// GetSettingsPtr returns the settings pointer for a committee.
// WARNING: The returned pointer is NOT safe to mutate without external synchronization.
// Prefer SetJoinMode() for thread-safe updates in tests.
// Returns nil if no settings exist for the given committee UID.
func (m *MockRepository) GetSettingsPtr(committeeUID string) *model.CommitteeSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.committeeSettings[committeeUID]
}

// SetJoinMode safely updates the join_mode for a committee's base.
func (m *MockRepository) SetJoinMode(committeeUID, joinMode string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if committee, exists := m.committees[committeeUID]; exists {
		committee.JoinMode = joinMode
	}
}

// AddCommitteeInvite adds a committee invite to the mock data (useful for testing)
func (m *MockRepository) AddCommitteeInvite(invite *model.CommitteeInvite) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.committeeInvites[invite.UID] = invite
	m.inviteIndexKeys[invite.BuildIndexKey(context.Background())] = invite
	m.inviteRevisions[invite.UID] = 1
}

// AddCommitteeApplication adds a committee application to the mock data (useful for testing)
func (m *MockRepository) AddCommitteeApplication(application *model.CommitteeApplication) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.committeeApplications[application.UID] = application
	m.applicationIndexKeys[application.BuildIndexKey(context.Background())] = application
	m.applicationRevisions[application.UID] = 1
}

// stringPtr is a helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}
