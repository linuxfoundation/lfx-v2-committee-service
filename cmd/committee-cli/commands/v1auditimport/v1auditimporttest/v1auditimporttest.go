// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package v1auditimporttest writes frozen v1 audit history import events files
// for the tests of the import commands.
package v1auditimporttest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
)

const rows = `{"id":"00000000-0000-4000-8000-000000000010","record_id":"committee-1","record_type":"COMMITTEE","action":"INSERT","created_at":"2025-03-17T21:27:04.533187Z","created_by":"actor-1","created_by_username":"first-last","new_record":{"sfid":"committee-1","project_name__c":"project-1","mailing_list__c":"Committee example"}}
{"id":"00000000-0000-4000-8000-000000000011","record_id":"committee-1","record_type":"COMMITTEE","action":"UPDATE","created_at":"2025-03-18T21:27:04.533187Z","created_by":"actor-1","created_by_username":"first-last","old_record":{"sfid":"committee-1","project_name__c":"project-1","mailing_list__c":"Committee example"},"new_record":{"sfid":"committee-1","project_name__c":"project-1","mailing_list__c":"Committee example 2"}}`

const mappings = `{"version":1,"source":"fixture-v1","projects":{"project-1":"00000000-0000-4000-8000-000000000002"},"committees":{"committee-1":"00000000-0000-4000-8000-000000000001"}}`

// WriteEvents writes a frozen events file (mode 0600) in dir and returns its
// path and the events in file order.
func WriteEvents(t *testing.T, dir string) (string, []service.V1AuditImportEvent) {
	t.Helper()
	manifest, err := service.DecodeV1AuditMappings(strings.NewReader(mappings))
	require.NoError(t, err)
	var file bytes.Buffer
	var events []service.V1AuditImportEvent
	report, err := service.PlanV1AuditHistory(context.Background(), strings.NewReader(rows),
		service.V1AuditOptions{Source: "fixture-v1", Mappings: manifest, MaxRecords: 100}, func(event model.V1AuditPlanEvent) error {
			frozen := service.V1AuditImportEvent{ObjectType: string(event.ResourceType), Event: event.Frozen}
			line, err := json.Marshal(frozen)
			if err != nil {
				return err
			}
			events = append(events, frozen)
			file.Write(append(line, '\n'))
			return nil
		})
	require.NoError(t, err)
	require.Zero(t, report.Invalid)
	require.NotEmpty(t, events)
	path := filepath.Join(dir, "events.ndjson")
	require.NoError(t, os.WriteFile(path, file.Bytes(), 0600))
	return path, events
}

// EventID returns a frozen event's audit event ID.
func EventID(t *testing.T, event service.V1AuditImportEvent) string {
	t.Helper()
	var envelope struct {
		Audit struct {
			EventID string `json:"event_id"`
		} `json:"audit"`
	}
	message, err := service.RestoreV1AuditImportEvent(event)
	require.NoError(t, err)
	raw, err := message.MarshalJSON()
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.NotEmpty(t, envelope.Audit.EventID)
	return envelope.Audit.EventID
}
