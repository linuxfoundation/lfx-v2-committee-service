// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"

	"github.com/go-viper/mapstructure/v2"
)

// MessageAction is a type for the action of a project message.
type MessageAction string

// MessageAction constants for the action of a project message.
const (
	// ActionCreated is the action for a resource creation message.
	ActionCreated MessageAction = "created"
	// ActionUpdated is the action for a resource update message.
	ActionUpdated MessageAction = "updated"
	// ActionDeleted is the action for a resource deletion message.
	ActionDeleted MessageAction = "deleted"
)

// CommitteeMemberMessageData is a wrapper that contains context for publishing messages
type CommitteeMemberMessageData struct {
	Member    *CommitteeMember
	OldMember *CommitteeMember // Only used for ActionUpdated
}

// CommitteeIndexerMessage is a NATS message schema for sending messages related to committees CRUD operations.
type CommitteeIndexerMessage struct {
	Action  MessageAction     `json:"action"`
	Headers map[string]string `json:"headers"`
	Data    any               `json:"data"`
	// Tags is a list of tags to be set on the indexed resource for search.
	Tags []string `json:"tags"`
}

func (c *CommitteeIndexerMessage) Build(ctx context.Context, input any) (*CommitteeIndexerMessage, error) {

	headers := make(map[string]string)
	if authorization, ok := ctx.Value(constants.AuthorizationContextID).(string); ok {
		headers[constants.AuthorizationHeader] = authorization
	}
	if principal, ok := ctx.Value(constants.PrincipalContextID).(string); ok {
		headers[constants.XOnBehalfOfHeader] = principal
	}
	c.Headers = headers

	var payload any

	switch c.Action {
	case ActionCreated, ActionUpdated:
		data, err := json.Marshal(input)
		if err != nil {
			slog.ErrorContext(ctx, "error marshalling data into JSON", "error", err)
			return nil, err
		}
		var jsonData any
		if err := json.Unmarshal(data, &jsonData); err != nil {
			slog.ErrorContext(ctx, "error unmarshalling data into JSON", "error", err)
			return nil, err
		}
		// Decode the JSON data into a map[string]any since that is what the indexer expects.
		config := mapstructure.DecoderConfig{
			TagName: "json",
			Result:  &payload,
		}
		decoder, err := mapstructure.NewDecoder(&config)
		if err != nil {
			slog.ErrorContext(ctx, "error creating decoder", "error", err)
			return nil, err
		}
		err = decoder.Decode(jsonData)
		if err != nil {
			slog.ErrorContext(ctx, "error decoding data", "error", err)
			return nil, err
		}
	case ActionDeleted:
		// The data should just be a string of the UID being deleted.
		payload = input
	}

	c.Data = payload

	return c, nil

}

// CommitteeAccessMessage is the schema for the data in the message sent to the fga-sync service.
// These are the fields that the fga-sync service needs in order to update the OpenFGA permissions.
type CommitteeAccessMessage struct {
	UID string `json:"uid"`
	// object_type is the type of the object that the message is about, e.g. "committee" or "project".
	ObjectType string `json:"object_type"`
	// public is the public flag for the object.
	Public bool `json:"public"`
	// relations are used to store the relations of the object, e.g. "writer"
	// and it's value is a list of principals.
	Relations map[string][]string `json:"relations"`
	// references are used to store the references of the object,
	// e.g. "project" and it's value is the project UID.
	// e.g. "parent" and it's value is the parent UID.
	References map[string]string `json:"references"`
	// Self stores OpenFGA self-relation tuples that enable conditional member-to-member visibility.
	// When populated, members can view other members' basic profiles based on committee visibility settings.
	Self []string `json:"self"`
}

// CommitteeMemberUpdateEventData represents the data structure for committee member update events
type CommitteeMemberUpdateEventData struct {
	MemberUID string           `json:"member_uid"`
	OldMember *CommitteeMember `json:"old_member"`
	Member    *CommitteeMember `json:"member"`
}

// CommitteeEvent represents a generic event emitted for committee service operations
type CommitteeEvent struct {
	// EventType identifies the type of event (e.g., committee_member.created)
	EventType string `json:"event_type"`
	// Subject is the subject of the event (e.g. lfx.committee-api.committee_member.created)
	Subject string `json:"subject"`
	// Timestamp is when the event occurred
	Timestamp time.Time `json:"timestamp"`
	// Version is the event schema version
	Version string `json:"version"`
	// Data contains the event data
	Data any `json:"data,omitempty"`
}

// ResourceType is a type for the resource type of a committee event.
type ResourceType string

// ResourceType constants for the resource type of a committee event.
const (
	ResourceCommitteeMember ResourceType = "committee_member"
)

// Build creates a CommitteeEvent from the resource type, action and input data
func (e *CommitteeEvent) Build(ctx context.Context, resource ResourceType, action MessageAction, input any) (*CommitteeEvent, error) {
	e.buildVersion()
	e.buildTimestamp()

	// Build events depending on the resource type
	switch resource {
	case ResourceCommitteeMember:
		return e.buildCommitteeMembers(ctx, resource, action, input)
	default:
		return nil, fmt.Errorf("unsupported resource type: %s", resource)
	}
}

func (e *CommitteeEvent) buildVersion() {
	e.Version = "1"
}

func (e *CommitteeEvent) buildTimestamp() {
	e.Timestamp = time.Now().UTC()
}

func (e *CommitteeEvent) buildEventType(resource ResourceType, action MessageAction) {
	e.EventType = fmt.Sprintf("%s.%s", resource, action)
}

func (e *CommitteeEvent) buildCommitteeMembers(ctx context.Context, resource ResourceType, action MessageAction, input any) (*CommitteeEvent, error) {
	switch action {
	case ActionCreated:
		e.Subject = constants.CommitteeMemberCreatedSubject
	case ActionUpdated:
		e.Subject = constants.CommitteeMemberUpdatedSubject
	case ActionDeleted:
		e.Subject = constants.CommitteeMemberDeletedSubject
	default:
		return nil, fmt.Errorf("unsupported action: %s", action)
	}

	e.buildEventType(resource, action)

	// Handle different input types based on action
	switch action {
	case ActionCreated, ActionDeleted:
		// For create/delete, expect CommitteeMember
		member, ok := input.(*CommitteeMember)
		if !ok || member == nil {
			slog.ErrorContext(ctx, "invalid input type for CommitteeEvent",
				"resource", resource,
				"action", action,
				"expected", "*CommitteeMember",
				"got", fmt.Sprintf("%T", input),
			)
			return nil, fmt.Errorf("invalid input type, got %T", input)
		}
		e.Data = member
	case ActionUpdated:
		// For updates, expect CommitteeMemberUpdateEventData
		updateData, ok := input.(*CommitteeMemberUpdateEventData)
		if !ok || updateData == nil {
			slog.ErrorContext(ctx, "invalid input type for CommitteeEvent update",
				"resource", resource,
				"action", action,
				"expected", "*CommitteeMemberUpdateEventData",
				"got", fmt.Sprintf("%T", input),
			)
			return nil, fmt.Errorf("invalid input type for update action, got %T", input)
		}
		e.Data = updateData
	}

	return e, nil
}
