// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"encoding/json"
	"log/slog"

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
}
