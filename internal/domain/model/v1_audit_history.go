// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// V1AuditSnapshot preserves only the historical fields actually present in a
// source image. An omitted field is unknown; JSON null, false and empty string
// remain distinct. Fields use legacy names deliberately, not guessed V2 values.
type V1AuditSnapshot struct {
	UID          string                     `json:"uid"`
	CommitteeUID string                     `json:"committee_uid"`
	ProjectUID   string                     `json:"project_uid"`
	Fields       map[string]json.RawMessage `json:"fields"`
}

// V1AuditPlanEvent is a private, offline review artifact, NOT an indexer wire
// envelope or a dispatchable outbox event. Revision/anchor approval and SDK
// preflight remain required before any later publishing implementation.
type V1AuditPlanEvent struct {
	EventID              string           `json:"event_id"`
	ResourceType         ResourceType     `json:"resource_type"`
	ResourceUID          string           `json:"resource_uid"`
	Action               MessageAction    `json:"action"`
	OccurredAt           time.Time        `json:"occurred_at"`
	ResourceRevision     uint64           `json:"resource_revision"`
	ActorRef             string           `json:"actor_ref"`
	HistoryCheckObject   string           `json:"history_check_object"`
	HistoryCheckRelation string           `json:"history_check_relation"`
	ScopeBefore          []string         `json:"scope_before,omitempty"`
	ScopeAfter           []string         `json:"scope_after,omitempty"`
	Schema               string           `json:"schema"`
	Before               *V1AuditSnapshot `json:"before,omitempty"`
	After                *V1AuditSnapshot `json:"after,omitempty"`
}

// V1AuditSchema names immutable, committee-owned historical snapshot shapes.
// These are separate from the complete native V2 resource schemas.
func V1AuditSchema(resource ResourceType) string {
	switch resource {
	case ResourceCommittee, ResourceCommitteeSettings, ResourceCommitteeMember:
		return string(resource) + ".legacy.v1"
	default:
		return ""
	}
}

// Values are field kinds, not defaults: b=boolean, s=string, u=credential-free
// URL/text (see v1AuditSafeURLText). Null is allowed for every captured source field. Never copy a raw
// database row, arbitrary profile or webhook into these closed field sets.
var v1AuditFields = map[ResourceType]map[string]byte{
	ResourceCommittee: {
		"mailing_list__c": 's', "public_name": 's', "type__c": 's',
		"description__c": 's', "committee_website__c": 'u', "mailing_list_email": 's',
		"chat_channel": 'u', "enable_voting__c": 'b', "sso_group_enabled": 'b',
		"sso_group_name": 's', "public_enabled": 'b', "join_mode": 's',
	},
	ResourceCommitteeSettings: {"business_email_required__c": 'b'},
	ResourceCommitteeMember: {
		"contact_name__c": 's', "contactemail__c": 's', "member_name": 's',
		"account__c": 's', "account_name": 's', "title": 's', "type__c": 's',
		"role__c": 's', "appointed_by__c": 's', "status__c": 's',
		"start_date__c": 's', "end_date__c": 's', "voting_status__c": 's',
		"votind_start_date__c": 's', "voting_end_date__c": 's',
	},
}

// V1AuditFields selects and canonicalizes the closed historical field inventory.
// Unlisted source fields are excluded, never forwarded to history. Strings are
// bounded but never truncated; sensitive URL forms reject the source image.
func V1AuditFields(resource ResourceType, source map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	allowed, ok := v1AuditFields[resource]
	if !ok {
		return nil, errs.NewValidation("unsupported historical resource schema")
	}
	fields := make(map[string]json.RawMessage)
	for key, kind := range allowed {
		raw, present := source[key]
		if !present {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return nil, errs.NewValidation("invalid historical snapshot value")
		}
		if value != nil {
			if kind == 'b' {
				if _, ok := value.(bool); !ok {
					return nil, errs.NewValidation("historical snapshot requires boolean")
				}
			} else {
				text, ok := value.(string)
				if !ok || len(text) > 65536 {
					return nil, errs.NewValidation("historical snapshot requires bounded string")
				}
				if kind == 'u' && !v1AuditSafeURLText(key, text) {
					return nil, errs.NewValidation("unsafe historical snapshot URL")
				}
			}
		}
		canonical, _ := json.Marshal(value)
		fields[key] = canonical
	}
	return fields, nil
}

// Older chat_channel values can be plain channel names rather than URLs.
var v1AuditChannelName = regexp.MustCompile(`^#[^\s#?/@:]{1,256}$`)

// v1AuditSafeURLText rejects URL forms that can carry credentials: userinfo,
// queries, fragments, opaque http(s) values such as "https:user:pass@host",
// unsupported schemes and incoming-webhook URLs, whose secret is in the path.
func v1AuditSafeURLText(key, text string) bool {
	if key == "chat_channel" && v1AuditChannelName.MatchString(text) {
		return true
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.Opaque != "" || (parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host, path := parsed.Hostname(), parsed.Path
	if parsed.Scheme == "" && parsed.Host == "" {
		// A bare "host/path" value parses entirely as a path.
		var rest string
		host, rest, _ = strings.Cut(parsed.Path, "/")
		path = "/" + rest
	}
	host, path = strings.TrimSuffix(strings.ToLower(host), "."), strings.ToLower(path)
	if host == "hooks.slack.com" || strings.HasSuffix(host, ".webhook.office.com") {
		return false
	}
	// Webhook tokens often sit in the path, including on self-hosted chat servers
	// (Mattermost, Rocket.Chat), Discord (/api/webhooks/) and legacy Office 365
	// connectors (outlook.office.com/webhook/ or /webhookb2/), so reject those
	// segments on any host.
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "webhook", "webhooks", "webhookb2", "hooks":
			return false
		}
	}
	return true
}

// ValidateV1AuditSnapshot rejects unregistered fields and invalid mapped IDs.
// Passing is local schema validation, not indexer policy approval or admission.
func ValidateV1AuditSnapshot(resource ResourceType, snapshot V1AuditSnapshot) error {
	for _, id := range []string{snapshot.UID, snapshot.CommitteeUID, snapshot.ProjectUID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return errs.NewValidation("historical snapshot requires canonical mapped UUIDs")
		}
	}
	if resource != ResourceCommitteeMember && snapshot.UID != snapshot.CommitteeUID {
		return errs.NewValidation("historical committee/settings identity must match committee scope")
	}
	fields, err := V1AuditFields(resource, snapshot.Fields)
	if err != nil {
		return err
	}
	if snapshot.Fields == nil || len(fields) != len(snapshot.Fields) {
		return errs.NewValidation("historical snapshot requires a closed fields object")
	}
	return nil
}
