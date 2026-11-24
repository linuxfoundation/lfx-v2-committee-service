// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

const v1HistoryEventID = "00000000-0000-4000-8000-000000000010"

func v1HistoryFixture(member bool, action string) (map[string]any, *V1AuditMappings) {
	before := map[string]any{"sfid": "committee-1", "project_name__c": "project-1", "mailing_list__c": "Earlier", "public_enabled": nil, "business_email_required__c": false, "chat_webhook_url": "private-marker"}
	after := map[string]any{"sfid": "committee-1", "project_name__c": "project-1", "mailing_list__c": "Later", "public_enabled": false, "business_email_required__c": true, "chat_webhook_url": "private-marker"}
	recordType, subID := "COMMITTEE", ""
	if member {
		recordType, subID = "COMMITTEEMEMBER", "member-record-1"
		before = map[string]any{"sfid": subID, "project_name__c": "project-1", "collaboration_name__c": "committee-1", "contact_name__c": "contact-1", "contactemail__c": "first.last@example.com", "member_name": "First Last", "role__c": nil, "lastmodifiedbyid": "private-marker"}
		after = map[string]any{"sfid": subID, "project_name__c": "project-1", "collaboration_name__c": "committee-1", "contact_name__c": "contact-1", "contactemail__c": "first.last@example.com", "member_name": "First Last", "role__c": "Chair", "lastmodifiedbyid": "private-marker"}
	}
	row := map[string]any{
		"id": v1HistoryEventID, "record_id": "committee-1", "sub_record_id": subID, "record_type": recordType, "action": action,
		"created_at": "2025-03-17T14:27:04.533187-07:00", "created_by": "actor-1", "created_by_username": "first-last",
		"created_by_email": "private-marker", "created_by_name": "private-marker", "old_record": before, "new_record": after,
	}
	if action == "INSERT" {
		row["old_record"] = nil
	}
	if action == "DELETE" {
		row["new_record"] = nil
	}
	mappings := &V1AuditMappings{
		Version: 1, Source: "fixture-v1", Projects: map[string]string{"project-1": "00000000-0000-4000-8000-000000000002"},
		Committees: map[string]string{"committee-1": "00000000-0000-4000-8000-000000000001"}, Members: map[string]string{subID: "00000000-0000-4000-8000-000000000003"},
		Revisions: map[string]uint64{v1HistoryEventID + "/committee": 1, v1HistoryEventID + "/committee_settings": 1, v1HistoryEventID + "/committee_member": 1},
	}
	if !member {
		mappings.Members = nil
	}
	return row, mappings
}

func v1HistoryJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func TestPlanV1AuditHistory(t *testing.T) {
	t.Run("lifecycle", func(t *testing.T) {
		for _, member := range []bool{false, true} {
			for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
				t.Run(fmt.Sprintf("member=%v/%s", member, action), func(t *testing.T) {
					row, mappings := v1HistoryFixture(member, action)
					var events []model.V1AuditPlanEvent
					run := func() {
						report, err := PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, func(event model.V1AuditPlanEvent) error {
							events = append(events, event)
							return nil
						})
						require.NoError(t, err)
						require.Equal(t, 1, report.Importable)
						require.Zero(t, report.Invalid)
						summary := v1HistoryJSON(t, report)
						for _, private := range []string{"first-last", "first.last@example.com", "First Last", "private-marker", "actor-1"} {
							require.NotContains(t, summary, private)
						}
					}
					run()
					first := v1HistoryJSON(t, events)
					if member {
						require.Len(t, events, 1)
					} else {
						require.Len(t, events, 2)
						require.NotEqual(t, events[0].EventID, events[1].EventID, "split resources need distinct source-derived identities")
					}
					for _, event := range events {
						require.Equal(t, "2025-03-17T21:27:04.533187Z", event.OccurredAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
						require.Equal(t, "committee:"+mappings.Committees["committee-1"], event.HistoryCheckObject)
						require.Equal(t, "auditor", event.HistoryCheckRelation)
						require.Equal(t, "user:first-last", event.ActorRef)
						require.NotContains(t, v1HistoryJSON(t, event), "private-marker")
						if action == "INSERT" {
							require.Nil(t, event.Before)
							require.Empty(t, event.ScopeBefore)
						} else {
							require.NoError(t, model.ValidateV1AuditSnapshot(event.ResourceType, *event.Before))
						}
						if action == "DELETE" {
							require.Nil(t, event.After)
							require.Empty(t, event.ScopeAfter)
						} else {
							require.NoError(t, model.ValidateV1AuditSnapshot(event.ResourceType, *event.After))
						}
					}
					events = nil
					run()
					require.Equal(t, first, v1HistoryJSON(t, events), "fresh previews cannot invent new identity, time or historical values")
				})
			}
		}
	})
	t.Run("gaps and validation", func(t *testing.T) {
		for _, tc := range []struct {
			name, reason string
			invalid      bool
			modify       func(map[string]any, *V1AuditMappings)
		}{
			{"committee mapping", "missing_committee_mapping", false, func(_ map[string]any, m *V1AuditMappings) { m.Committees = nil }},
			{"project mapping", "missing_project_mapping", false, func(_ map[string]any, m *V1AuditMappings) { m.Projects = nil }},
			{"member mapping", "missing_member_mapping", false, func(_ map[string]any, m *V1AuditMappings) { m.Members = nil }},
			{"revision", "missing_approved_revision", false, func(_ map[string]any, m *V1AuditMappings) { m.Revisions = nil }},
			{"missing project", "missing_historical_project_scope", false, func(r map[string]any, _ *V1AuditMappings) {
				delete(r["old_record"].(map[string]any), "project_name__c")
				delete(r["new_record"].(map[string]any), "project_name__c")
			}},
			{"ownership move", "ownership_move_requires_review", false, func(r map[string]any, _ *V1AuditMappings) {
				r["old_record"].(map[string]any)["project_name__c"] = "other-project"
			}},
			{"payload ID", "inconsistent_source_resource_id", true, func(r map[string]any, _ *V1AuditMappings) { r["new_record"].(map[string]any)["sfid"] = "other-member" }},
			{"snapshot side", "invalid_source_snapshot_sides", true, func(r map[string]any, _ *V1AuditMappings) { r["old_record"] = nil }},
			{"bad date", "invalid_source_record", true, func(r map[string]any, _ *V1AuditMappings) { r["created_at"] = "private-marker" }},
			// Valid RFC 3339 input that becomes year 10000 in UTC cannot be encoded.
			{"unencodable time", "historical_draft_not_encodable", true, func(r map[string]any, _ *V1AuditMappings) { r["created_at"] = "9999-12-31T23:59:59-01:00" }},
			{"bad action", "unsupported_source_action", true, func(r map[string]any, _ *V1AuditMappings) { r["action"] = "other" }},
			{"unadopted type", "unsupported_source_record_type", false, func(r map[string]any, _ *V1AuditMappings) { r["record_type"] = "OTHER" }},
			{"operational only", "no_supported_historical_change", false, func(r map[string]any, _ *V1AuditMappings) { r["new_record"] = r["old_record"] }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				row, mappings := v1HistoryFixture(true, "UPDATE")
				tc.modify(row, mappings)
				report, err := PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, nil)
				require.NoError(t, err)
				require.Equal(t, 1, report.Reasons[tc.reason])
				require.Zero(t, report.Importable)
				if tc.invalid {
					require.Equal(t, 1, report.Invalid)
				} else {
					require.Equal(t, 1, report.Skipped)
				}
			})
		}
	})
	t.Run("actors", func(t *testing.T) {
		for _, tc := range []struct{ username, actorID, override, want string }{
			{"first-last", "actor-1", "", "user:first-last"},
			{"client-1@clients", "client-1", "", "service:client-1"},
			{"", "actor-1", "", "legacy:fixture-v1/actor-1"},
			{"first-last", "actor-1", "user:current-placeholder", "user:current-placeholder"},
			{"", "actor-1", "service:client-1", "service:client-1"},
			// A literal ID shaped like the old hashed form stays literal.
			{"", "sha256-actor", "", "legacy:fixture-v1/sha256-actor"},
			{"", "actor 1", "", "legacy:fixture-v1/sha256:" + v1AuditHash([]byte("actor 1"))},
			{"", "", "", "legacy:fixture-v1/event:" + v1AuditHash([]byte("fixture-v1\x00"+v1HistoryEventID))},
		} {
			t.Run(tc.want, func(t *testing.T) {
				row, mappings := v1HistoryFixture(true, "DELETE")
				row["created_by"], row["created_by_username"] = tc.actorID, tc.username
				if tc.override != "" {
					mappings.Actors = map[string]string{tc.actorID: tc.override}
				}
				var events []model.V1AuditPlanEvent
				_, err := PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, func(event model.V1AuditPlanEvent) error {
					events = append(events, event)
					return nil
				})
				require.NoError(t, err)
				require.Len(t, events, 1)
				require.Equal(t, tc.want, events[0].ActorRef)
			})
		}
	})
	t.Run("duplicates and bounds", func(t *testing.T) {
		for _, scenario := range []string{"duplicate", "source conflict", "revision conflict", "limit", "limit then oversized", "limit then blank", "limit then unicode blank", "scope", "scoped source conflict", "no manifest"} {
			t.Run(scenario, func(t *testing.T) {
				row, mappings := v1HistoryFixture(true, "UPDATE")
				first := v1HistoryJSON(t, row)
				options := V1AuditOptions{Source: mappings.Source, Mappings: mappings}
				switch scenario {
				case "source conflict":
					row["new_record"].(map[string]any)["role__c"] = "Other"
				case "revision conflict":
					row["id"] = "00000000-0000-4000-8000-000000000011"
					mappings.Revisions[row["id"].(string)+"/committee_member"] = 1
				case "limit", "limit then oversized", "limit then blank", "limit then unicode blank":
					options.MaxRecords = 1
				case "scope":
					options.CommitteeID = "other-committee"
				case "scoped source conflict":
					options.CommitteeID = "other-committee"
					row["new_record"].(map[string]any)["role__c"] = "Other"
				case "no manifest":
					options.Mappings = nil
				}
				second := v1HistoryJSON(t, row)
				switch scenario {
				case "limit then oversized":
					second = strings.Repeat("x", v1AuditMaxBytes+1)
				case "limit then blank":
					second = " \r\n\t\n"
				case "limit then unicode blank":
					// bytes.TrimSpace treats these as blank before the limit too.
					second = "\u00a0\u2003\n\u3000"
				}
				report, err := PlanV1AuditHistory(context.Background(), strings.NewReader(first+"\n"+second), options, nil)
				require.NoError(t, err)
				switch scenario {
				case "duplicate":
					require.Equal(t, 1, report.Importable)
					require.Equal(t, 1, report.Reasons["duplicate_source_record"])
				case "source conflict":
					require.Equal(t, 1, report.Reasons["conflicting_source_identity"])
				case "revision conflict":
					require.Equal(t, 1, report.Reasons["conflicting_resource_revision"])
				case "limit", "limit then oversized":
					require.Equal(t, 1, report.Rows)
					require.Equal(t, 1, report.Importable)
					require.True(t, report.Limited)
				case "limit then blank", "limit then unicode blank":
					require.Equal(t, 1, report.Rows)
					require.False(t, report.Limited)
				case "scope":
					require.Equal(t, 2, report.Reasons["outside_committee_scope"])
				case "scoped source conflict":
					require.Equal(t, 1, report.Reasons["outside_committee_scope"])
					require.Equal(t, 1, report.Reasons["conflicting_source_identity"])
					require.Equal(t, 1, report.Invalid)
				case "no manifest":
					require.Zero(t, report.Importable)
					require.Equal(t, 1, report.Reasons["missing_committee_mapping"])
				}
			})
		}
	})
	t.Run("preserves unknown null and false", func(t *testing.T) {
		row, mappings := v1HistoryFixture(false, "UPDATE")
		delete(row["old_record"].(map[string]any), "public_enabled")
		var events []model.V1AuditPlanEvent
		report, err := PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, func(event model.V1AuditPlanEvent) error {
			events = append(events, event)
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 1, report.Importable)
		require.NotContains(t, events[0].Before.Fields, "public_enabled")
		require.JSONEq(t, `false`, string(events[0].After.Fields["public_enabled"]))
		// An image predating a setting's capture is not a fabricated false value.
		delete(row["old_record"].(map[string]any), "business_email_required__c")
		events = nil
		_, err = PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, func(event model.V1AuditPlanEvent) error {
			events = append(events, event)
			return nil
		})
		require.NoError(t, err)
		require.Empty(t, events[1].Before.Fields)
		require.JSONEq(t, `true`, string(events[1].After.Fields["business_email_required__c"]))
	})
	t.Run("untrusted input", func(t *testing.T) {
		row, mappings := v1HistoryFixture(true, "UPDATE")
		valid := v1HistoryJSON(t, row)
		for _, data := range []string{
			strings.Replace(valid, `"id":`, `"id":"`+v1HistoryEventID+`","id":`, 1),
			strings.Replace(valid, `"id":`, `"ID":`, 1),
			valid + " {}", `[]`, `null`, `{"private-marker":"value"}`,
			strings.Replace(valid, `"role__c":"Chair"`, `"role__c":"Chair","role__c":null`, 1),
			strings.Replace(valid, `"member_name":"First Last"`, `"member_name":`+strings.Repeat(`[`, 70)+`0`+strings.Repeat(`]`, 70), 1),
			// Unpaired surrogate escapes would silently decode to U+FFFD.
			strings.Replace(valid, `"member_name":"First Last"`, `"member_name":"First \ud800Last"`, 1),
			strings.Replace(valid, `"member_name":"First Last"`, `"member_name":"First Last\udc00"`, 1),
			strings.Replace(valid, `"member_name":"First Last"`, `"member_name":"First \uD800\u0041"`, 1),
		} {
			report, err := PlanV1AuditHistory(context.Background(), strings.NewReader(data), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, nil)
			require.NoError(t, err)
			require.Equal(t, 1, report.Invalid)
			require.NotContains(t, v1HistoryJSON(t, report), "private-marker")
		}
		_, err := PlanV1AuditHistory(context.Background(), strings.NewReader(strings.Repeat("private-marker", v1AuditMaxBytes)), V1AuditOptions{Source: mappings.Source}, nil)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-marker")
	})
	t.Run("unicode escapes kept", func(t *testing.T) {
		row, mappings := v1HistoryFixture(true, "INSERT")
		valid := v1HistoryJSON(t, row)
		// A valid surrogate pair, an escaped backslash before "ud800", and an explicit
		// U+FFFD escape are all legitimate input.
		for _, name := range []string{`First \ud83d\ude00 Last`, `First \\ud800 Last`, `First \ufffd Last`} {
			var events []model.V1AuditPlanEvent
			report, err := PlanV1AuditHistory(context.Background(), strings.NewReader(strings.Replace(valid, `"member_name":"First Last"`, `"member_name":"`+name+`"`, 1)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, func(event model.V1AuditPlanEvent) error {
				events = append(events, event)
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 1, report.Importable, name)
			var want string
			require.NoError(t, json.Unmarshal([]byte(`"`+name+`"`), &want))
			require.JSONEq(t, v1HistoryJSON(t, want), string(events[0].After.Fields["member_name"]))
		}
	})
	t.Run("cancellation and sink failure", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			cancelAt  string
			sinkErr   error
			sinkCalls int
		}{
			{"canceled before reading", "start", nil, 0},
			{"canceled at end of input", "eof", nil, 1},
			{"sink failure", "", fmt.Errorf("private-marker"), 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				row, mappings := v1HistoryFixture(true, "DELETE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tc.cancelAt == "start" {
					cancel()
				}
				var input io.Reader = strings.NewReader(v1HistoryJSON(t, row))
				if tc.cancelAt == "eof" {
					input = &v1CancelAtEOFReader{Reader: input, cancel: cancel}
				}
				calls := 0
				_, err := PlanV1AuditHistory(ctx, input, V1AuditOptions{Source: mappings.Source, Mappings: mappings}, func(model.V1AuditPlanEvent) error {
					calls++
					return tc.sinkErr
				})
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-marker")
				require.Equal(t, tc.sinkCalls, calls)
			})
		}
	})
	t.Run("direct caller mappings", func(t *testing.T) {
		row, mappings := v1HistoryFixture(true, "DELETE")
		// Keys that JSON re-encoding would expand past the manifest bound stay valid.
		for i := 0; i < 1000; i++ {
			mappings.Projects[fmt.Sprintf("%s%d", strings.Repeat("<", 250), i)] = mappings.Projects["project-1"]
		}
		report, err := PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, nil)
		require.NoError(t, err)
		require.Equal(t, 1, report.Importable)
		mappings.Actors = map[string]string{"\xff": "user:first-last"}
		_, err = PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, nil)
		require.Error(t, err)
	})
}

func TestDecodeV1AuditMappings(t *testing.T) {
	_, mappings := v1HistoryFixture(true, "UPDATE")
	valid := v1HistoryJSON(t, mappings)
	for _, data := range []string{
		strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(valid, `"version":1`, `"Version":1`, 1),
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, mappings.Members["member-record-1"], "private-marker", 1),
		valid + " {}", strings.Repeat(" ", v1AuditMaxBytes+1),
		strings.Replace(valid, `"project-1"`, `"project-\ud800"`, 1),
	} {
		_, err := DecodeV1AuditMappings(strings.NewReader(data))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-marker")
	}
}

// v1CancelAtEOFReader cancels its context when the underlying input is exhausted.
type v1CancelAtEOFReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *v1CancelAtEOFReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}
