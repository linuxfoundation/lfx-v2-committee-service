// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func TestV1AuditFields(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"false", `{"public_enabled":false}`, true},
		{"null", `{"public_enabled":null}`, true},
		{"empty string", `{"public_name":""}`, true},
		{"unknown excluded", `{"chat_webhook_url":"private-marker"}`, true},
		{"wrong boolean", `{"public_enabled":"false"}`, false},
		{"wrong string", `{"public_name":1}`, false},
		{"URL credentials", `{"committee_website__c":"https://first-last:` + `private-marker@example.com"}`, false},
		{"URL query", `{"chat_channel":"https://example.com/chat?secret=private-marker"}`, false},
		{"bare URL query", `{"committee_website__c":"example.com?secret=private-marker"}`, false},
		{"URL fragment", `{"chat_channel":"https://example.com/#private-marker"}`, false},
		{"URL empty query", `{"committee_website__c":"https://example.com/?"}`, false},
		{"opaque URL credentials", `{"committee_website__c":"https:first-last:` + `private-marker@example.com"}`, false},
		{"unsupported scheme", `{"committee_website__c":"ftp://example.com/private-marker"}`, false},
		// Webhook hosts are split so secret scanners do not treat fixtures as secrets.
		{"Slack webhook", `{"chat_channel":"https://hooks.slack` + `.com/services/private-marker"}`, false},
		{"bare Slack webhook", `{"chat_channel":"Hooks.Slack` + `.com./services/private-marker"}`, false},
		{"Discord webhook", `{"chat_channel":"https://discord` + `.com/api/webhooks/private-marker"}`, false},
		{"Teams webhook", `{"chat_channel":"https://example.webhook.office` + `.com/private-marker"}`, false},
		{"legacy Outlook webhook", `{"chat_channel":"https://outlook.office` + `.com/webhook/private-marker/IncomingWebhook/private-marker/private-marker"}`, false},
		{"legacy Office 365 webhook", `{"committee_website__c":"https://Outlook.Office365` + `.com/webhookb2/private-marker"}`, false},
		{"Outlook link", `{"committee_website__c":"https://outlook.office.com/calendar/view"}`, true},
		{"self-hosted hooks path", `{"chat_channel":"https://chat.example.com/hooks/private-marker"}`, false},
		{"self-hosted hooks path with token", `{"chat_channel":"https://chat.example.com/Hooks/private-marker/private-marker"}`, false},
		{"bare webhooks path", `{"committee_website__c":"example.com/api/v10/webhooks/private-marker"}`, false},
		{"hooks only as a word", `{"committee_website__c":"https://example.com/git-hooks-guide"}`, true},
		{"plain channel name", `{"chat_channel":"#general"}`, true},
		{"channel name with query", `{"chat_channel":"#general?secret=private-marker"}`, false},
		{"channel name outside chat_channel", `{"committee_website__c":"#private-marker"}`, false},
		{"chat link", `{"chat_channel":"https://example.com/chat/general"}`, true},
		{"Discord channel link", `{"chat_channel":"https://discord.com/channels/1/2"}`, true},
		{"bare website", `{"committee_website__c":"example.com/committee"}`, true},
		{"all kinds", `{"public_name":"Committee example","committee_website__c":"https://example.com/","enable_voting__c":true,"chat_channel":null}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var source map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(tc.raw), &source))
			fields, err := model.V1AuditFields(model.ResourceCommittee, source)
			if tc.valid {
				require.NoError(t, err)
				delete(source, "chat_webhook_url")
				require.NotContains(t, fields, "chat_webhook_url")
				require.Len(t, fields, len(source), "every allowed source field must be captured")
				for key, value := range source {
					require.Contains(t, fields, key)
					require.JSONEq(t, string(value), string(fields[key]))
				}
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-marker")
			}
		})
	}
}

func TestValidateV1AuditSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*model.V1AuditSnapshot)
		valid  bool
	}{
		{"closed", func(*model.V1AuditSnapshot) {}, true},
		{"unknown field omitted", func(s *model.V1AuditSnapshot) { s.Fields = map[string]json.RawMessage{} }, true},
		{"null fields object", func(s *model.V1AuditSnapshot) { s.Fields = nil }, false},
		{"unregistered field", func(s *model.V1AuditSnapshot) { s.Fields["private_key"] = json.RawMessage(`"private-marker"`) }, false},
		{"invalid UUID", func(s *model.V1AuditSnapshot) { s.ProjectUID = "project-1" }, false},
		{"mismatched scope", func(s *model.V1AuditSnapshot) { s.CommitteeUID = s.ProjectUID }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := model.V1AuditSnapshot{
				UID: "00000000-0000-4000-8000-000000000001", CommitteeUID: "00000000-0000-4000-8000-000000000001",
				ProjectUID: "00000000-0000-4000-8000-000000000002", Fields: map[string]json.RawMessage{"public_enabled": json.RawMessage(`false`)},
			}
			tc.modify(&snapshot)
			err := model.ValidateV1AuditSnapshot(model.ResourceCommittee, snapshot)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-marker")
			}
		})
	}
}
