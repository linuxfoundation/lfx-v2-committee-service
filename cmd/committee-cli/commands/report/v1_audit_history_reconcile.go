// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const (
	// v1AuditHistoryIndex is the indexer's history index; a document's ID is
	// <publisher>:<event_id>.
	v1AuditHistoryIndex = "resource-history-v1"
	// v1AuditHistoryMaxResponse bounds one history document response: the
	// event's snapshots plus the indexer's metadata.
	v1AuditHistoryMaxResponse = 2 << 20
	v1AuditHistoryReadTimeout = 30 * time.Second
)

// v1AuditHistoryReconcileSubcommand checks published import events against the
// stored history and records each outcome in the state journal. It writes only
// the local journal.
type v1AuditHistoryReconcileSubcommand struct {
	output io.Writer
	client *http.Client
}

func (*v1AuditHistoryReconcileSubcommand) Name() string { return "v1-audit-history-reconcile" }

func (*v1AuditHistoryReconcileSubcommand) Help() string {
	return "check sent v1 audit history import events against stored history (reads OpenSearch; writes only the state journal)"
}

// RunsOffline is true: reconciliation reads OpenSearch, never NATS.
func (*v1AuditHistoryReconcileSubcommand) RunsOffline(_ []string) bool { return true }

func (s *v1AuditHistoryReconcileSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	out := s.output
	if out == nil {
		out = os.Stdout
	}
	fs := flag.NewFlagSet(s.Name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	eventsPath := fs.String("events", "", "frozen import events file the import sent")
	statePath := fs.String("state", "", "the import's state journal")
	defaultURL := os.Getenv("OPENSEARCH_URL")
	if defaultURL == "" {
		defaultURL = "http://localhost:9200"
	}
	openSearchURL := fs.String("opensearch-url", defaultURL, "OpenSearch base URL, without credentials (default: $OPENSEARCH_URL, else http://localhost:9200)")
	// --help prints a flag's default value verbatim: never print the environment's URL.
	fs.Lookup("opensearch-url").DefValue = ""
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprintln(out, "usage: committee-cli report v1-audit-history-reconcile --events <file> --state <file> [--opensearch-url <url>]")
			fs.SetOutput(out)
			fs.PrintDefaults()
			return nil
		}
		return errs.NewValidation("invalid audit history reconcile arguments")
	}
	if *eventsPath == "" || *statePath == "" || fs.NArg() != 0 {
		return errs.NewValidation("audit history reconcile requires --events and --state")
	}
	base, err := url.Parse(strings.TrimRight(*openSearchURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" {
		return errs.NewValidation("--opensearch-url must be an http(s) URL without credentials or query")
	}
	if _, err := os.Stat(*statePath); err != nil {
		return errs.NewValidation("import state journal does not exist; nothing has been sent")
	}

	events, err := v1auditimport.OpenEvents(ctx, *eventsPath)
	if err != nil {
		return err
	}
	defer events.Close()
	journal, err := v1auditimport.OpenJournal(*statePath, events, true)
	if err != nil {
		return err
	}
	defer journal.Close()

	client := s.client
	if client == nil {
		client = &http.Client{Timeout: v1AuditHistoryReadTimeout}
	}
	reader := openSearchHistoryReader{client: client, base: base}
	var report *service.V1AuditReconcileReport
	err = events.Scan(func(r io.Reader) error {
		var err error
		report, err = service.ReconcileV1AuditImport(ctx, r, journal.State, reader, journal.Record, nil)
		return err
	})
	if report != nil {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if encoder.Encode(report) != nil && err == nil {
			err = errs.NewUnexpected("cannot write audit history reconcile summary")
		}
	}
	return err
}

// openSearchHistoryReader reads one history document with a realtime GET, so a
// just-written record is seen before an index refresh.
type openSearchHistoryReader struct {
	client *http.Client
	base   *url.URL
}

func (r openSearchHistoryReader) GetHistory(ctx context.Context, id string) (*service.V1AuditHistoryRecord, error) {
	target := r.base.JoinPath(v1AuditHistoryIndex, "_doc", id)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		// Transport errors echo the URL, which holds the event ID.
		return nil, errs.NewUnexpected("history read transport failure")
	}
	defer func() { _ = response.Body.Close() }()
	var document struct {
		Found  *bool                         `json:"found"`
		Source *service.V1AuditHistoryRecord `json:"_source"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, v1AuditHistoryMaxResponse))
	if decoder.Decode(&document) != nil || document.Found == nil {
		// An error body (such as a missing index) has no "found".
		return nil, errs.NewUnexpected(fmt.Sprintf("unexpected history read response (status %d)", response.StatusCode))
	}
	switch {
	case response.StatusCode == http.StatusNotFound && !*document.Found:
		return nil, nil
	case response.StatusCode == http.StatusOK && *document.Found && document.Source != nil:
		return document.Source, nil
	}
	return nil, errs.NewUnexpected(fmt.Sprintf("unexpected history read response (status %d)", response.StatusCode))
}

var _ commands.OfflineSubcommand = (*v1AuditHistoryReconcileSubcommand)(nil)
