// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	indexconstants "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// CommitteeAuditPublisher is the namespace to bind to the authenticated relay
// in the indexer's trusted publisher policy. It is not authentication itself.
const CommitteeAuditPublisher = "committee-service"

// CommitteeAuditSnapshot is the closed, versioned resource representation used
// by committee audit events. It deliberately excludes webhook credentials and
// embedded directory profiles. Scope and attribution live in the audit envelope.
// Adding fields requires a new schema version, not expanding this struct.
type CommitteeAuditSnapshot struct {
	UID              string                         `json:"uid"`
	ProjectUID       string                         `json:"project_uid"`
	ProjectName      string                         `json:"project_name,omitempty"`
	ProjectSlug      string                         `json:"project_slug,omitempty"`
	Name             string                         `json:"name"`
	Category         string                         `json:"category"`
	Description      string                         `json:"description,omitempty"`
	Website          *string                        `json:"website,omitempty"`
	MailingList      *string                        `json:"mailing_list,omitempty"`
	ChatChannel      *string                        `json:"chat_channel,omitempty"`
	EnableVoting     bool                           `json:"enable_voting"`
	SSOGroupEnabled  bool                           `json:"sso_group_enabled"`
	SSOGroupName     string                         `json:"sso_group_name,omitempty"`
	RequiresReview   bool                           `json:"requires_review"`
	Public           bool                           `json:"public"`
	JoinMode         string                         `json:"join_mode,omitempty"`
	Calendar         CommitteeAuditCalendar         `json:"calendar"`
	DisplayName      string                         `json:"display_name,omitempty"`
	ParentUID        *string                        `json:"parent_uid,omitempty"`
	Repository       *string                        `json:"repository,omitempty"`
	Scope            []string                       `json:"scope,omitempty"`
	Deliverables     []string                       `json:"deliverables,omitempty"`
	KeyDates         []CommitteeAuditKeyDate        `json:"key_dates,omitempty"`
	ExternalSources  []CommitteeAuditExternalSource `json:"external_sources,omitempty"`
	Charter          *CommitteeAuditCharter         `json:"charter,omitempty"`
	TotalMembers     int                            `json:"total_members"`
	TotalVotingRepos int                            `json:"total_voting_repos"`
	HasMailingList   bool                           `json:"has_mailing_list"`
	CreatedAt        time.Time                      `json:"created_at"`
	UpdatedAt        time.Time                      `json:"updated_at"`
}

// CommitteeSettingsAuditSnapshot records restricted configuration without the
// webhook URL or unbounded directory profiles.
type CommitteeSettingsAuditSnapshot struct {
	UID                           string               `json:"uid"`
	ProjectUID                    string               `json:"project_uid"`
	BusinessEmailRequired         bool                 `json:"business_email_required"`
	ShowMeetingAttendees          bool                 `json:"show_meeting_attendees"`
	MembersCanViewSocialListening bool                 `json:"members_can_view_social_listening"`
	MemberVisibility              string               `json:"member_visibility"`
	HasChatWebhook                bool                 `json:"has_chat_webhook"`
	LastReviewedAt                *string              `json:"last_reviewed_at,omitempty"`
	LastReviewedBy                *string              `json:"last_reviewed_by,omitempty"`
	Writers                       []CommitteeAuditUser `json:"writers"`
	Auditors                      []CommitteeAuditUser `json:"auditors"`
	CreatedAt                     time.Time            `json:"created_at"`
	UpdatedAt                     time.Time            `json:"updated_at"`
}

// CommitteeMemberAuditSnapshot retains the full resource-owned membership state.
// It excludes arbitrary directory profiles and request-only fields.
type CommitteeMemberAuditSnapshot struct {
	UID               string                     `json:"uid"`
	CommitteeUID      string                     `json:"committee_uid"`
	ProjectUID        string                     `json:"project_uid"`
	Username          string                     `json:"username"`
	Avatar            string                     `json:"avatar,omitempty"`
	Email             string                     `json:"email"`
	FirstName         string                     `json:"first_name"`
	LastName          string                     `json:"last_name"`
	JobTitle          string                     `json:"job_title,omitempty"`
	LinkedInProfile   string                     `json:"linkedin_profile,omitempty"`
	Role              CommitteeAuditRole         `json:"role"`
	AppointedBy       string                     `json:"appointed_by"`
	Status            string                     `json:"status"`
	Voting            CommitteeAuditVoting       `json:"voting"`
	Organization      CommitteeAuditOrganization `json:"organization"`
	CommitteeName     string                     `json:"committee_name"`
	CommitteeCategory string                     `json:"committee_category"`
	ProjectSlug       string                     `json:"project_slug,omitempty"`
	CreatedAt         time.Time                  `json:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at"`
}

// CommitteeAuditUser freezes the resource-owned settings/charter user fields.
// It is not an arbitrary directory profile or an actor authorization grant.
type CommitteeAuditUser struct {
	Avatar   string `json:"avatar,omitempty"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
}

// CommitteeAuditCalendar freezes the public calendar setting.
type CommitteeAuditCalendar struct {
	Public bool `json:"public"`
}

// CommitteeAuditKeyDate freezes a charter timeline entry.
type CommitteeAuditKeyDate struct {
	Date  string `json:"date"`
	Label string `json:"label"`
}

// CommitteeAuditExternalSource freezes the owned external-link metadata.
type CommitteeAuditExternalSource struct {
	Provider              string `json:"provider"`
	EntityType            string `json:"entity_type"`
	Label                 string `json:"label"`
	URL                   string `json:"url"`
	ExternalID            string `json:"external_id,omitempty"`
	ExternalCategory      string `json:"external_category,omitempty"`
	ExternalRegion        string `json:"external_region,omitempty"`
	ExternalEventCategory string `json:"external_event_category,omitempty"`
}

// CommitteeAuditCharter retains the charter state at the source change.
type CommitteeAuditCharter struct {
	URL       string              `json:"url"`
	Version   int                 `json:"version"`
	UpdatedAt time.Time           `json:"updated_at"`
	UpdatedBy *CommitteeAuditUser `json:"updated_by,omitempty"`
}

// CommitteeAuditOrganization freezes owned membership organization metadata.
type CommitteeAuditOrganization struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Website string `json:"website,omitempty"`
}

// SnapshotCommittee copies the full resource through an immutable field
// allowlist. Future model additions do not silently expand retained history.
func SnapshotCommittee(base CommitteeBase) (CommitteeAuditSnapshot, error) {
	if base.Charter != nil {
		charter := *base.Charter
		charter.URL = sanitizeCharterURL(charter.URL)
		base.Charter = &charter
	}
	var snapshot CommitteeAuditSnapshot
	err := copyAuditSnapshot(base, &snapshot)
	return snapshot, err
}

// SnapshotCommitteeSettings excludes ChatWebhookURL and adds project scope from
// the parent read in the mutation transaction.
func SnapshotCommitteeSettings(settings CommitteeSettings, projectUID string) (CommitteeSettingsAuditSnapshot, error) {
	var snapshot CommitteeSettingsAuditSnapshot
	err := copyAuditSnapshot(settings, &snapshot)
	snapshot.ProjectUID = projectUID
	snapshot.HasChatWebhook = settings.ChatWebhookURL != nil && *settings.ChatWebhookURL != ""
	return snapshot, err
}

// SnapshotCommitteeMember copies only the resource-owned member fields.
func SnapshotCommitteeMember(member CommitteeMember) (CommitteeMemberAuditSnapshot, error) {
	var snapshot CommitteeMemberAuditSnapshot
	err := copyAuditSnapshot(member, &snapshot)
	return snapshot, err
}

func copyAuditSnapshot(source, target any) error {
	raw, err := json.Marshal(source)
	if err != nil {
		return errs.NewValidation("invalid source for committee audit snapshot")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return errs.NewValidation("invalid source for committee audit snapshot")
	}
	return nil
}

// CommitteeAuditRole freezes the membership role schema independently of the
// mutable current-resource representation.
type CommitteeAuditRole struct {
	Name      string `json:"name"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

// CommitteeAuditVoting freezes the membership voting schema independently of
// the mutable current-resource representation.
type CommitteeAuditVoting struct {
	Status    string `json:"status"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

// CommitteeAuditMutation supplies source-assigned values captured within the
// mutation transaction. Construction does not generate identity, time, revision,
// or attribution, and must never be deferred until a delivery retry.
type CommitteeAuditMutation struct {
	ObjectType     ResourceType
	EventID        string
	OccurredAt     time.Time
	Revision       uint64
	Action         MessageAction
	Attribution    indexertypes.AuditAttribution
	Scope          indexertypes.AuditScope
	Before         any
	After          any
	Data           any
	Tags           []string
	IndexingConfig *indexertypes.IndexingConfig
}

// NewCommitteeAuditMutation freezes a v2 mutation with optional actor evidence for a
// transactional outbox. Data must be the sanitized current-index projection,
// not a raw settings/database row. Snapshots use only the closed types below.
func NewCommitteeAuditMutation(m CommitteeAuditMutation) (*indexertypes.MessageV2, error) {
	for _, value := range []any{m.Before, m.After} {
		if err := validateCommitteeAuditResourceSize(value); err != nil {
			return nil, err
		}
	}
	schema, err := committeeAuditSchema(m.ObjectType)
	if err != nil {
		return nil, err
	}
	before, err := marshalCommitteeAuditSnapshot(m.ObjectType, m.Before)
	if err != nil {
		return nil, err
	}
	after, err := marshalCommitteeAuditSnapshot(m.ObjectType, m.After)
	if err != nil {
		return nil, err
	}
	content, err := indexertypes.NewAuditSnapshots(schema, before, after)
	if err != nil {
		return nil, err
	}
	if err := validateCommitteeAuditIdentity(m, before, after); err != nil {
		return nil, err
	}
	data, err := committeeAuditProjection(m)
	if err != nil {
		return nil, err
	}
	if err := validateCommitteeAuditState(after, data); err != nil {
		return nil, err
	}
	if err := validateCommitteeAuditUpdateBudget(m, after, data); err != nil {
		return nil, err
	}
	return indexertypes.NewMessageV2(string(m.ObjectType), indexertypes.MessageV2Options{
		Action: indexconstants.MessageAction(m.Action), Data: data, Tags: m.Tags,
		IndexingConfig: m.IndexingConfig,
		Audit: indexertypes.AuditEvent{
			Publisher: CommitteeAuditPublisher, EventID: m.EventID,
			OccurredAt: m.OccurredAt, ResourceRevision: m.Revision,
			Origin: indexertypes.AuditOriginMutation, Attribution: m.Attribution,
			Scope: m.Scope, Content: content,
		},
	}, ValidateCommitteeAuditContent)
}

// NewCommitteeAuditBootstrap freezes the authoritative current state without
// inventing a historical edit. It is for adoption/reindex, not real mutations.
func NewCommitteeAuditBootstrap(m CommitteeAuditMutation) (*indexertypes.MessageV2, error) {
	return NewCommitteeAuditProjection(m, indexertypes.AuditOriginBackfill)
}

// NewCommitteeAuditProjection freezes a source-owned projection, without history
// content or a new authoritative revision. Only trusted derived writers select it.
func NewCommitteeAuditProjection(m CommitteeAuditMutation, origin indexertypes.AuditOrigin) (*indexertypes.MessageV2, error) {
	if origin != indexertypes.AuditOriginBackfill && origin != indexertypes.AuditOriginReindex {
		return nil, errs.NewValidation("unsupported committee projection origin")
	}
	if origin == indexertypes.AuditOriginBackfill && m.Action != ActionCreated || origin == indexertypes.AuditOriginReindex && m.Action != ActionUpdated {
		return nil, errs.NewValidation("unsupported committee projection action")
	}
	if m.After == nil {
		return nil, errs.NewValidation("committee audit projection requires a typed after snapshot")
	}
	for _, value := range []any{m.Before, m.After} {
		if err := validateCommitteeAuditResourceSize(value); err != nil {
			return nil, err
		}
	}
	before, err := marshalCommitteeAuditSnapshot(m.ObjectType, m.Before)
	if err != nil {
		return nil, err
	}
	after, err := marshalCommitteeAuditSnapshot(m.ObjectType, m.After)
	if err != nil {
		return nil, err
	}
	if err := validateCommitteeAuditIdentity(m, before, after); err != nil {
		return nil, err
	}
	data, err := committeeAuditProjection(m)
	if err != nil {
		return nil, err
	}
	if err := validateCommitteeAuditState(after, data); err != nil {
		return nil, err
	}
	if err := validateCommitteeAuditUpdateBudget(m, after, data); err != nil {
		return nil, err
	}
	return indexertypes.NewMessageV2(string(m.ObjectType), indexertypes.MessageV2Options{
		Action: indexconstants.MessageAction(m.Action), Data: data, IndexingConfig: m.IndexingConfig,
		Audit: indexertypes.AuditEvent{Publisher: CommitteeAuditPublisher, EventID: m.EventID,
			OccurredAt: m.OccurredAt, ResourceRevision: m.Revision, Origin: origin,
			Attribution: m.Attribution, Scope: m.Scope},
	}, ValidateCommitteeAuditContent)
}

func committeeAuditProjection(m CommitteeAuditMutation) (any, error) {
	if m.Action == ActionDeleted {
		uid, ok := m.Data.(string)
		if !ok || uid != m.IndexingConfig.ObjectID {
			return nil, errs.NewValidation("committee audit deletion requires the resource UID")
		}
		return uid, nil
	}
	switch m.ObjectType {
	case ResourceCommittee:
		base, ok := m.Data.(CommitteeBase)
		if ok && base.UID == m.IndexingConfig.ObjectID {
			public := m.IndexingConfig.Public != nil && *m.IndexingConfig.Public
			if public != base.Public {
				return nil, errs.NewValidation("committee audit indexing visibility mismatch")
			}
			if !containsAuditRef(m.Scope.After, "project:"+base.ProjectUID) ||
				!containsAuditRef(m.Scope.After, "committee:"+base.UID) {
				return nil, errs.NewValidation("committee audit projection scope mismatch")
			}
			if base.Charter != nil {
				charter := *base.Charter
				charter.URL = sanitizeCharterURL(charter.URL)
				if charter.UpdatedBy != nil {
					user := *charter.UpdatedBy
					user.Email = ""
					charter.UpdatedBy = &user
				}
				base.Charter = &charter
			}
			return checkedCommitteeAuditProjectionSize(base)
		}
	case ResourceCommitteeSettings:
		settings, ok := m.Data.(CommitteeSettings)
		if ok && settings.UID == m.IndexingConfig.ObjectID {
			if settings.ChatWebhookURL != nil {
				settings.HasChatWebhook = *settings.ChatWebhookURL != ""
			}
			settings.ChatWebhookURL = nil
			return checkedCommitteeAuditProjectionSize(settings)
		}
	case ResourceCommitteeMember:
		member, ok := m.Data.(CommitteeMember)
		if ok && member.UID == m.IndexingConfig.ObjectID {
			if !containsAuditRef(m.Scope.After, "project:"+member.ProjectUID) ||
				!containsAuditRef(m.Scope.After, "committee:"+member.CommitteeUID) {
				return nil, errs.NewValidation("committee audit projection scope mismatch")
			}
			return checkedCommitteeAuditProjectionSize(member)
		}
	}
	return nil, errs.NewValidation("committee audit requires a typed resource projection with matching identity")
}

func committeeAuditSchema(kind ResourceType) (string, error) {
	switch kind {
	case ResourceCommittee, ResourceCommitteeSettings, ResourceCommitteeMember:
		return string(kind) + ".v1", nil
	default:
		return "", errs.NewValidation("unsupported committee audit resource type")
	}
}

func marshalCommitteeAuditSnapshot(kind ResourceType, snapshot any) (json.RawMessage, error) {
	if snapshot == nil {
		return nil, nil
	}
	var valid bool
	switch kind {
	case ResourceCommittee:
		_, valid = snapshot.(CommitteeAuditSnapshot)
	case ResourceCommitteeSettings:
		_, valid = snapshot.(CommitteeSettingsAuditSnapshot)
	case ResourceCommitteeMember:
		_, valid = snapshot.(CommitteeMemberAuditSnapshot)
	}
	if !valid {
		return nil, errs.NewValidation("a closed committee audit snapshot value is required")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, errs.NewValidation("invalid committee audit snapshot")
	}
	return payload, nil
}

// ValidateCommitteeAuditContent is a closed publisher/resource/schema registry
// for the initial full-resource snapshots. It must also be registered in the indexer
// before delivery is enabled; a publisher-side callback is not policy approval.
func ValidateCommitteeAuditContent(publisher, objectType string, content indexertypes.AuditContent) error {
	schema, err := committeeAuditSchema(ResourceType(objectType))
	if err != nil || publisher != CommitteeAuditPublisher || content.Schema != schema ||
		content.Representation != indexertypes.AuditRepresentationSnapshots {
		return errs.NewValidation("unregistered committee audit content schema")
	}
	for _, raw := range []json.RawMessage{content.Before, content.After} {
		if len(raw) == 0 {
			continue
		}
		var target any
		switch ResourceType(objectType) {
		case ResourceCommittee:
			target = new(CommitteeAuditSnapshot)
		case ResourceCommitteeSettings:
			target = new(CommitteeSettingsAuditSnapshot)
		case ResourceCommitteeMember:
			target = new(CommitteeMemberAuditSnapshot)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return errs.NewValidation("invalid committee audit snapshot")
		}
		if snapshot, ok := target.(*CommitteeAuditSnapshot); ok && snapshot.Charter != nil &&
			snapshot.Charter.URL != sanitizeCharterURL(snapshot.Charter.URL) {
			return errs.NewValidation("committee audit charter contains an unsafe URL")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return errs.NewValidation("invalid committee audit snapshot")
		}
		// Re-encoding enforces required keys, canonical casing and non-null scalar
		// values in addition to the decoder's recursive unknown-field checks.
		canonical, err := json.Marshal(target)
		if err != nil || !sameAuditJSONObject(raw, canonical) {
			return errs.NewValidation("committee audit snapshot must contain exactly the schema fields")
		}
	}
	return nil
}

func sameAuditJSONObject(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func validateCommitteeAuditIdentity(m CommitteeAuditMutation, before, after json.RawMessage) error {
	if m.IndexingConfig == nil {
		return errs.NewValidation("committee audit indexing config is required")
	}
	for _, side := range []struct {
		raw   json.RawMessage
		scope []string
	}{{before, m.Scope.Before}, {after, m.Scope.After}} {
		if len(side.raw) == 0 {
			continue
		}
		var identity struct {
			UID          string `json:"uid"`
			CommitteeUID string `json:"committee_uid"`
			ProjectUID   string `json:"project_uid"`
		}
		if json.Unmarshal(side.raw, &identity) != nil || identity.UID != m.IndexingConfig.ObjectID {
			return errs.NewValidation("committee audit snapshot identity mismatch")
		}
		committeeUID := identity.UID
		if m.ObjectType == ResourceCommitteeMember {
			committeeUID = identity.CommitteeUID
		}
		if committeeUID == "" || identity.ProjectUID == "" || len(side.scope) != 2 ||
			!containsAuditRef(side.scope, "committee:"+committeeUID) ||
			!containsAuditRef(side.scope, "project:"+identity.ProjectUID) ||
			m.IndexingConfig.HistoryCheckObject != "committee:"+committeeUID ||
			m.IndexingConfig.HistoryCheckRelation != "auditor" {
			return errs.NewValidation("committee audit scope or history authorization mismatch")
		}
		accessObject, accessRelation := "committee:"+committeeUID, "viewer"
		if m.ObjectType == ResourceCommitteeSettings {
			accessObject, accessRelation = "committee:"+committeeUID, "auditor"
		}
		if m.IndexingConfig.AccessCheckObject != accessObject || m.IndexingConfig.AccessCheckRelation != accessRelation ||
			(m.ObjectType != ResourceCommittee && m.IndexingConfig.Public != nil && *m.IndexingConfig.Public) {
			return errs.NewValidation("committee audit projection authorization mismatch")
		}
	}
	if len(m.IndexingConfig.Contacts) != 0 {
		return errs.NewValidation("committee audit projections must not include arbitrary contact profiles")
	}
	return nil
}

func containsAuditRef(refs []string, want string) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

// ValidateCommitteeAuditEnvelope applies committee-owned identity, scope,
// authorization and projection exclusions when restoring an outbox event. The
// shared validator alone cannot enforce these domain-specific relationships.
func ValidateCommitteeAuditEnvelope(envelope *indexertypes.IndexerMessageEnvelope, kind ResourceType) error {
	if envelope == nil || envelope.Audit == nil ||
		envelope.Audit.Publisher != CommitteeAuditPublisher {
		return errs.NewValidation("a committee audit event is required")
	}
	m := CommitteeAuditMutation{
		ObjectType: kind, Action: MessageAction(envelope.Action),
		IndexingConfig: envelope.IndexingConfig, Scope: envelope.Audit.Scope,
	}
	content := envelope.Audit.Content
	validProjection := envelope.Audit.Origin == indexertypes.AuditOriginBackfill && m.Action == ActionCreated ||
		envelope.Audit.Origin == indexertypes.AuditOriginReindex && m.Action == ActionUpdated
	if envelope.Audit.Origin == indexertypes.AuditOriginMutation {
		if content == nil {
			return errs.NewValidation("a mutation snapshot is required")
		}
		if err := ValidateCommitteeAuditContent(CommitteeAuditPublisher, string(kind), *content); err != nil {
			return err
		}
		if err := validateCommitteeAuditIdentity(m, content.Before, content.After); err != nil {
			return err
		}
	} else if content != nil || !validProjection {
		return errs.NewValidation("unsupported committee audit projection")
	}
	raw, err := json.Marshal(envelope.Data)
	if err != nil {
		return errs.NewValidation("invalid committee audit projection")
	}
	if m.Action == ActionDeleted {
		m.Data = envelope.Data
	} else {
		var target any
		switch kind {
		case ResourceCommittee:
			target = new(CommitteeBase)
		case ResourceCommitteeSettings:
			target = new(CommitteeSettings)
		case ResourceCommitteeMember:
			target = new(CommitteeMember)
		default:
			return errs.NewValidation("unsupported committee audit projection")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(target) != nil {
			return errs.NewValidation("invalid committee audit projection")
		}
		switch value := target.(type) {
		case *CommitteeBase:
			m.Data = *value
		case *CommitteeSettings:
			m.Data = *value
		case *CommitteeMember:
			m.Data = *value
		}
	}
	if envelope.Audit.Origin != indexertypes.AuditOriginMutation {
		var snapshot any
		switch value := m.Data.(type) {
		case CommitteeBase:
			snapshot, err = SnapshotCommittee(value)
		case CommitteeMember:
			snapshot, err = SnapshotCommitteeMember(value)
		case CommitteeSettings:
			var projectUID string
			for _, ref := range m.Scope.After {
				if strings.HasPrefix(ref, "project:") {
					projectUID = strings.TrimPrefix(ref, "project:")
				}
			}
			snapshot, err = SnapshotCommitteeSettings(value, projectUID)
		}
		if err != nil || snapshot == nil {
			return errs.NewValidation("invalid committee bootstrap source")
		}
		after, err := marshalCommitteeAuditSnapshot(kind, snapshot)
		if err != nil {
			return err
		}
		if err := validateCommitteeAuditIdentity(m, nil, after); err != nil {
			return err
		}
	}
	projection, err := committeeAuditProjection(m)
	if err != nil {
		return err
	}
	if content != nil && m.Action != ActionDeleted {
		if err := validateCommitteeAuditState(content.After, projection); err != nil {
			return err
		}
	}
	canonical, err := json.Marshal(projection)
	if err != nil || !sameAuditJSONObject(raw, canonical) {
		return errs.NewValidation("committee audit projection contains excluded or noncanonical fields")
	}
	return nil
}
