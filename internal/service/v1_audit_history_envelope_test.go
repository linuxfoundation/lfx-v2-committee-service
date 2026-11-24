// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// v1HistoryPlannedEvents plans the fixture row and returns its events.
func v1HistoryPlannedEvents(t *testing.T, member bool, action string) []model.V1AuditPlanEvent {
	t.Helper()
	row, mappings := v1HistoryFixture(member, action)
	var events []model.V1AuditPlanEvent
	_, err := PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, func(event model.V1AuditPlanEvent) error {
		events = append(events, event)
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, events)
	return events
}

func TestBuildV1AuditImportEvent(t *testing.T) {
	t.Run("envelope fields", func(t *testing.T) {
		for _, tc := range []struct {
			member         bool
			action         string
			accessRelation map[model.ResourceType]string
		}{
			{false, "UPDATE", map[model.ResourceType]string{model.ResourceCommittee: "viewer", model.ResourceCommitteeSettings: "auditor"}},
			{true, "DELETE", map[model.ResourceType]string{model.ResourceCommitteeMember: "viewer"}},
		} {
			for _, event := range v1HistoryPlannedEvents(t, tc.member, tc.action) {
				built, err := BuildV1AuditImportEvent(event)
				require.NoError(t, err)
				require.Equal(t, string(event.ResourceType), built.ObjectType)

				var envelope indexertypes.IndexerMessageEnvelope
				require.NoError(t, json.Unmarshal(built.Event, &envelope))
				require.Equal(t, indexertypes.AuditSchemaVersion, envelope.SchemaVersion)
				require.Empty(t, envelope.Headers, "frozen events carry no credential")
				require.Equal(t, indexertypes.AuditOriginHistoricalImport, envelope.Audit.Origin)
				require.Equal(t, V1AuditImportPublisher, envelope.Audit.Publisher)
				require.Equal(t, event.EventID, envelope.Audit.EventID)
				require.Equal(t, event.ResourceRevision, envelope.Audit.ResourceRevision)
				require.Equal(t, event.ActorRef, envelope.Audit.Attribution.ActorRef)
				require.Equal(t, event.ActorRef, envelope.Audit.Attribution.EffectiveActorRef)
				require.Equal(t, event.HistoryCheckObject, envelope.IndexingConfig.HistoryCheckObject)
				require.Equal(t, event.HistoryCheckObject, envelope.IndexingConfig.AccessCheckObject)
				require.Equal(t, tc.accessRelation[event.ResourceType], envelope.IndexingConfig.AccessCheckRelation)
				require.Equal(t, event.ResourceUID, envelope.IndexingConfig.ObjectID)
				if event.Action == model.ActionDeleted {
					require.Equal(t, event.ResourceUID, envelope.Data, "a delete's data is the resource UID")
				} else {
					require.Equal(t, map[string]any{"uid": event.ResourceUID}, envelope.Data)
				}
			}
		}
	})
	t.Run("rejects what the indexer would reject", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			modify func(*model.V1AuditPlanEvent)
		}{
			{"actor with a colon in a legacy ID", func(e *model.V1AuditPlanEvent) { e.ActorRef = "legacy:fixture-v1/sha256:abc" }},
			{"actor with an email", func(e *model.V1AuditPlanEvent) { e.ActorRef = "user:first.last@example.com" }},
			{"zero revision", func(e *model.V1AuditPlanEvent) { e.ResourceRevision = 0 }},
			{"wrong schema", func(e *model.V1AuditPlanEvent) { e.Schema = "committee.v1" }},
			{"unlisted snapshot field", func(e *model.V1AuditPlanEvent) {
				e.After.Fields["private_key_hint"] = json.RawMessage(`"private-marker"`)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				event := v1HistoryPlannedEvents(t, true, "UPDATE")[0]
				after := *event.After
				after.Fields = map[string]json.RawMessage{}
				for key, value := range event.After.Fields {
					after.Fields[key] = value
				}
				event.After = &after
				tc.modify(&event)
				_, err := BuildV1AuditImportEvent(event)
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-marker")
			})
		}
	})
}

func TestBuildV1AuditImportEvent_SizeLimit(t *testing.T) {
	// fill sets every committee string field on both sides to n characters and
	// returns the snapshots' combined JSON size.
	fill := func(event model.V1AuditPlanEvent, n int) int {
		value := json.RawMessage(`"` + strings.Repeat("a", n) + `"`)
		total := 0
		for _, snapshot := range []*model.V1AuditSnapshot{event.Before, event.After} {
			snapshot.Fields = map[string]json.RawMessage{}
			for key, kind := range model.V1AuditFieldTypes(model.ResourceCommittee) {
				if kind == "string" {
					snapshot.Fields[key] = value
				}
			}
			encoded, err := json.Marshal(snapshot)
			require.NoError(t, err)
			total += len(encoded)
		}
		return total
	}
	committee := func() model.V1AuditPlanEvent {
		event := v1HistoryPlannedEvents(t, false, "UPDATE")[0]
		require.Equal(t, model.ResourceCommittee, event.ResourceType)
		return event
	}

	// Snapshots alone over the frozen limit, and over the indexer's own 1 MiB.
	for _, n := range []int{55000, 60000} {
		event := committee()
		require.Greater(t, fill(event, n), v1AuditMaxFrozenBytes)
		_, err := BuildV1AuditImportEvent(event)
		require.ErrorIs(t, err, errV1AuditImportTooLarge, "an oversized event must be reported as a size failure (%d)", n)
	}

	// Snapshots just under the limit, so only the envelope's overhead pushes the
	// frozen event over it: this exercises the final frozen-size check.
	event := committee()
	base := fill(event, 0)
	values := 0 // string fields on both sides
	for _, kind := range model.V1AuditFieldTypes(model.ResourceCommittee) {
		if kind == "string" {
			values += 2
		}
	}
	n := (v1AuditMaxFrozenBytes - 200 - base) / values
	require.Less(t, fill(event, n), v1AuditMaxFrozenBytes)
	_, err := BuildV1AuditImportEvent(event)
	require.ErrorIs(t, err, errV1AuditImportTooLarge, "envelope overhead past the limit must be a size failure")
}

func TestValidateV1AuditImportContent(t *testing.T) {
	event := v1HistoryPlannedEvents(t, true, "UPDATE")[0]
	after, err := json.Marshal(event.After)
	require.NoError(t, err)
	content := indexertypes.AuditContent{Schema: "committee_member.legacy.v1", Representation: indexertypes.AuditRepresentationSnapshots, After: after}
	require.NoError(t, ValidateV1AuditImportContent(V1AuditImportPublisher, "committee_member", content))

	for name, check := range map[string]func() error{
		"live publisher": func() error { return ValidateV1AuditImportContent("committee-service", "committee_member", content) },
		"other resource": func() error { return ValidateV1AuditImportContent(V1AuditImportPublisher, "committee", content) },
		"trailing data": func() error {
			bad := content
			bad.After = append(append(json.RawMessage(nil), after...), []byte(" {}")...)
			return ValidateV1AuditImportContent(V1AuditImportPublisher, "committee_member", bad)
		},
		"unknown key": func() error {
			bad := content
			bad.After = json.RawMessage(strings.Replace(string(after), `"uid":`, `"private_marker":1,"uid":`, 1))
			return ValidateV1AuditImportContent(V1AuditImportPublisher, "committee_member", bad)
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, check())
		})
	}
}

func TestV1AuditImportPolicy(t *testing.T) {
	for _, principal := range []string{"", "client-1", "first.last@example.com", "a:b@clients", "@clients"} {
		_, err := V1AuditImportPolicy(principal)
		require.Error(t, err, principal)
	}

	raw, err := V1AuditImportPolicy("client-1@clients")
	require.NoError(t, err)
	var policy struct {
		Publishers []struct {
			Publisher       string `json:"publisher"`
			Principal       string `json:"principal"`
			CanAttestActors bool   `json:"can_attest_actors"`
			Resources       map[string]struct {
				Origins []string `json:"origins"`
				Schemas map[string]struct {
					Fields map[string]v1AuditPolicyField `json:"fields"`
				} `json:"schemas"`
			} `json:"resources"`
		} `json:"publishers"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&policy))
	require.Len(t, policy.Publishers, 1)
	publisher := policy.Publishers[0]
	require.Equal(t, V1AuditImportPublisher, publisher.Publisher)
	require.Equal(t, "client-1@clients", publisher.Principal)
	require.True(t, publisher.CanAttestActors)

	var resources []string
	for name, resource := range publisher.Resources {
		resources = append(resources, name)
		require.Equal(t, []string{"historical_import"}, resource.Origins, "the import publisher may only import history")
		schema, ok := resource.Schemas[model.V1AuditSchema(model.ResourceType(name))]
		require.True(t, ok)
		require.Len(t, resource.Schemas, 1)
		for _, key := range []string{"uid", "committee_uid", "project_uid", "fields"} {
			require.True(t, schema.Fields[key].Required, key)
		}
		require.Len(t, schema.Fields, 4)
		fields := schema.Fields["fields"].Fields
		want := model.V1AuditFieldTypes(model.ResourceType(name))
		require.Len(t, fields, len(want), "the policy must list exactly the snapshot inventory")
		for key, kind := range want {
			require.Equal(t, v1AuditPolicyField{Type: kind, Nullable: true}, fields[key], key)
		}
	}
	sort.Strings(resources)
	require.Equal(t, []string{"committee", "committee_member", "committee_settings"}, resources)
}
