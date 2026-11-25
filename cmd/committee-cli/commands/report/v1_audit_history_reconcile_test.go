// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport/v1auditimporttest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
)

// urlWithCredentials builds a URL carrying a fake credential at run time, so no
// credential-shaped literal is in the source.
func urlWithCredentials(scheme, host string) string {
	return (&url.URL{Scheme: scheme, Host: host, User: url.UserPassword("user", "private-marker")}).String()
}

func TestV1AuditHistoryReconcileSubcommand(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	eventsPath, events := v1auditimporttest.WriteEvents(t, dir)
	require.GreaterOrEqual(t, len(events), 2)
	statePath := filepath.Join(dir, "state.ndjson")

	// Journal every event as published, as an --apply run would.
	opened, err := v1auditimport.OpenEvents(ctx, eventsPath)
	require.NoError(t, err)
	journal, err := v1auditimport.OpenJournal(statePath, opened, true)
	require.NoError(t, err)
	ids := make([]string, len(events))
	for i, event := range events {
		ids[i] = v1auditimporttest.EventID(t, event)
		require.NoError(t, journal.Record(service.V1AuditImportRecord{EventID: ids[i], ObjectType: event.ObjectType,
			Status: service.V1AuditImportPublished, At: time.Now().UTC(), Stream: "index-events", Sequence: uint64(i + 1)}))
	}
	journal.Close()
	opened.Close()

	// The first event is stored; the rest are not yet.
	stored, err := service.ExpectedV1AuditHistoryRecord(events[0])
	require.NoError(t, err)
	source, err := json.Marshal(stored)
	require.NoError(t, err)
	var mu sync.Mutex
	var paths []string
	indexMissing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		missing := indexMissing
		mu.Unlock()
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case missing:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"index_not_found_exception","reason":"private-marker"},"status":404}`))
		case strings.HasSuffix(r.URL.Path, "/"+service.V1AuditImportPublisher+":"+ids[0]):
			_, _ = w.Write([]byte(`{"_index":"resource-history-v1","found":true,"_source":` + string(source) + `}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"_index":"resource-history-v1","found":false}`))
		}
	}))
	defer server.Close()

	run := func(args ...string) (*service.V1AuditReconcileReport, error) {
		var out bytes.Buffer
		err := (&v1AuditHistoryReconcileSubcommand{output: &out, client: server.Client()}).Run(ctx, commands.RunContext{Args: args})
		if out.Len() == 0 {
			return nil, err
		}
		var report service.V1AuditReconcileReport
		require.NoError(t, json.Unmarshal(out.Bytes(), &report))
		return &report, err
	}
	base := []string{"--events", eventsPath, "--state", statePath, "--opensearch-url", server.URL + "/"}

	report, err := run(base...)
	require.NoError(t, err)
	require.Equal(t, len(events), report.Checked)
	require.Equal(t, 1, report.Persisted)
	require.Equal(t, len(events)-1, report.Missing)
	require.False(t, report.Complete)
	require.Equal(t, "/resource-history-v1/_doc/"+service.V1AuditImportPublisher+":"+ids[0], paths[0])

	// The persisted event is settled and not read again.
	paths = nil
	report, err = run(base...)
	require.NoError(t, err)
	require.Equal(t, len(events)-1, report.Checked)
	require.Len(t, paths, len(events)-1)

	// A missing index is an error, not "missing history"; its body is not echoed.
	indexMissing = true
	_, err = run(base...)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-marker")

	for name, args := range map[string][]string{
		"credentials in the URL": {"--events", eventsPath, "--state", statePath, "--opensearch-url", urlWithCredentials("http", "localhost:9200")},
		"no URL":                 {"--events", eventsPath, "--state", statePath, "--opensearch-url", ""},
		"no journal":             {"--events", eventsPath, "--state", filepath.Join(dir, "private-marker"), "--opensearch-url", server.URL},
		"unknown flag":           {"--private-marker"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := run(args...)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-marker")
		})
	}
	t.Run("help never prints the environment's URL", func(t *testing.T) {
		t.Setenv("OPENSEARCH_URL", urlWithCredentials("https", "opensearch.example.com:9200"))
		var out bytes.Buffer
		require.NoError(t, (&v1AuditHistoryReconcileSubcommand{output: &out}).Run(ctx, commands.RunContext{Args: []string{"--help"}}))
		require.Contains(t, out.String(), "--opensearch-url")
		require.NotContains(t, out.String(), "private-marker")
	})

	_, err = os.Stat(statePath + ".lock")
	require.True(t, os.IsNotExist(err), "every run releases the journal lock")
}
