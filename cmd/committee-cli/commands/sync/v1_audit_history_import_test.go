// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport/v1auditimporttest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/nats/natstest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
)

func TestV1AuditHistoryImportSubcommand(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	eventsPath, events := v1auditimporttest.WriteEvents(t, dir)
	statePath := filepath.Join(dir, "state.ndjson")
	tokenPath := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("test-token\n"), 0600))

	client := natstest.NewClient(t)
	js, err := client.JetStream()
	require.NoError(t, err)
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "index-events", Subjects: []string{"lfx.index.>"}})
	require.NoError(t, err)

	run := func(args ...string) (*service.V1AuditDispatchReport, error) {
		var out, logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		err := (&v1AuditHistoryImportSubcommand{output: &out, logger: logger}).Run(ctx, commands.RunContext{Args: args, JetStream: client.JetStream})
		require.Zero(t, out.Len(), "the summary is a log line, not separate output")
		if logs.Len() == 0 {
			return nil, err
		}
		// One log line, whose fields are the report's.
		require.Equal(t, 1, bytes.Count(logs.Bytes(), []byte("\n")))
		require.Contains(t, logs.String(), `"msg":"v1-audit-history-import complete"`)
		require.Contains(t, logs.String(), `"failed":false`)
		var report service.V1AuditDispatchReport
		require.NoError(t, json.Unmarshal(logs.Bytes(), &report))
		return &report, err
	}
	base := []string{"--events", eventsPath, "--state", statePath}

	t.Run("preview sends and writes nothing", func(t *testing.T) {
		report, err := run(base...)
		require.NoError(t, err)
		require.Equal(t, len(events), report.WouldSend)
		require.NoFileExists(t, statePath)
		info, err := stream.Info(ctx)
		require.NoError(t, err)
		require.Zero(t, info.State.Msgs)
	})

	t.Run("apply publishes once with the de-duplication ID and journals each ack", func(t *testing.T) {
		// A capped batch still reports the file's total.
		report, err := run(append(base, "--apply", "--authorization-file", tokenPath, "--sleep", "0s", "--max-events", "1")...)
		require.NoError(t, err)
		require.Equal(t, 1, report.Sent)
		require.True(t, report.Limited)
		require.Equal(t, len(events), report.Events)

		report, err = run(append(base, "--apply", "--authorization-file", tokenPath, "--sleep", "0s")...)
		require.NoError(t, err)
		require.Equal(t, len(events)-1, report.Sent)

		for i, event := range events {
			msg, err := stream.GetMsg(ctx, uint64(i+1))
			require.NoError(t, err)
			require.Equal(t, "lfx.index."+event.ObjectType, msg.Subject)
			require.Equal(t, service.V1AuditImportPublisher+":"+v1auditimporttest.EventID(t, event), msg.Header.Get(jetstream.MsgIDHeader))
			var envelope struct {
				Headers map[string]string `json:"headers"`
			}
			require.NoError(t, json.Unmarshal(msg.Data, &envelope))
			require.Equal(t, "Bearer test-token", envelope.Headers["authorization"])
		}
		data, err := os.ReadFile(statePath)
		require.NoError(t, err)
		require.NotContains(t, string(data), "test-token", "the journal never holds the credential")

		// A second run inside the republish window sends nothing.
		report, err = run(append(base, "--apply", "--authorization-file", tokenPath)...)
		require.NoError(t, err)
		require.Equal(t, len(events), report.AwaitingHistory)
		info, err := stream.Info(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(len(events)), info.State.Msgs)
	})

	t.Run("a run that fails is summarized as failed", func(t *testing.T) {
		// A broker with no index-events stream refuses every publish.
		empty := natstest.NewClient(t)
		state := filepath.Join(t.TempDir(), "state.ndjson")
		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		err := (&v1AuditHistoryImportSubcommand{logger: logger}).Run(ctx, commands.RunContext{JetStream: empty.JetStream,
			Args: []string{"--events", eventsPath, "--state", state, "--apply", "--authorization-file", tokenPath, "--sleep", "0s"}})
		require.ErrorContains(t, err, "not acknowledged")
		require.Contains(t, logs.String(), `"msg":"v1-audit-history-import failed"`)
		require.Contains(t, logs.String(), `"level":"ERROR"`)
		var summary struct {
			Failed bool `json:"failed"`
			Sent   int  `json:"sent"`
		}
		require.NoError(t, json.Unmarshal(logs.Bytes(), &summary))
		require.True(t, summary.Failed)
		require.Zero(t, summary.Sent)
	})

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"apply without a token":   {append(base, "--apply"), "--apply requires --authorization-file"},
		"token without apply":     {append(base, "--authorization-file", tokenPath), "only used with --apply"},
		"republish under minimum": {append(base, "--republish-after", "4m59s"), "--republish-after"},
		"negative sleep":          {append(base, "--sleep", "-1s"), "--sleep"},
		"no state":                {[]string{"--events", eventsPath}, "--events and --state"},
		"unknown flag":            {[]string{"--private-marker"}, "invalid audit history import arguments"},
		"zero max events":         {append(base, "--max-events", "0"), "--max-events"},
		"too many max events":     {append(base, "--max-events", "10001"), "--max-events"},
		"extra argument":          {append(base, "private-marker"), "positional"},
		"missing events file":     {[]string{"--events", filepath.Join(dir, "private-marker"), "--state", statePath}, "events file"},
		"world-readable token":    {append(base, "--apply", "--authorization-file", eventsPath+".shared"), "authorization file"},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "world-readable token" {
				require.NoError(t, os.WriteFile(eventsPath+".shared", []byte("private-marker"), 0600))
				require.NoError(t, os.Chmod(eventsPath+".shared", 0o640))
			}
			_, err := run(tc.args...)
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "private-marker")
		})
	}
}
