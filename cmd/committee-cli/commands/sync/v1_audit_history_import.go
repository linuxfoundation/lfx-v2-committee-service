// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	indexerconstants "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const (
	// v1AuditHistoryImportPublishTimeout bounds one publish and its PubAck.
	v1AuditHistoryImportPublishTimeout = 10 * time.Second
	// v1AuditHistoryImportMinRepublish keeps a re-send past the index-events
	// stream's duplicate window (the NATS default of 2 minutes), so the broker
	// delivers it again rather than dropping it as a duplicate.
	v1AuditHistoryImportMinRepublish = 5 * time.Minute
)

// v1AuditHistoryImportSubcommand sends frozen v1 audit history import events to
// the indexer and records each broker acknowledgement in the state journal.
type v1AuditHistoryImportSubcommand struct {
	output io.Writer    // help text
	logger *slog.Logger // the run summary; slog.Default() when nil
}

func (*v1AuditHistoryImportSubcommand) Name() string { return "v1-audit-history-import" }

func (*v1AuditHistoryImportSubcommand) Help() string {
	return "send frozen v1 audit history import events to the indexer (preview unless --apply)"
}

func (s *v1AuditHistoryImportSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	out := s.output
	if out == nil {
		out = os.Stdout
	}
	fs := flag.NewFlagSet(s.Name(), flag.ContinueOnError)
	// Parser errors can contain private paths; return fixed diagnostics.
	fs.SetOutput(io.Discard)
	eventsPath := fs.String("events", "", "frozen import events file written by report v1-audit-history --plan-output")
	statePath := fs.String("state", "", "the import's state journal (created by the first --apply run; mode 0600)")
	authorizationPath := fs.String("authorization-file", "", "file (mode 0600) holding the import client's bearer token; required with --apply")
	apply := fs.Bool("apply", false, "publish; without it the run only reports what it would send")
	maxEvents := fs.Int("max-events", 100, "maximum events published in this run (1..10000)")
	republishAfter := fs.Duration("republish-after", 15*time.Minute, "wait before re-sending a published event that has no history yet (at least 5m)")
	sleep := fs.Duration("sleep", 200*time.Millisecond, "pause between publishes")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprintln(out, "usage: committee-cli sync v1-audit-history-import --events <file> --state <file> [--apply --authorization-file <file>] [flags]")
			fs.SetOutput(out)
			fs.PrintDefaults()
			return nil
		}
		return errs.NewValidation("invalid audit history import arguments")
	}
	// Each message names the flag to change, never a path or value.
	switch {
	case fs.NArg() != 0:
		return errs.NewValidation("audit history import takes no positional arguments")
	case *eventsPath == "" || *statePath == "":
		return errs.NewValidation("audit history import requires --events and --state")
	case *apply && *authorizationPath == "":
		return errs.NewValidation("--apply requires --authorization-file")
	case !*apply && *authorizationPath != "":
		return errs.NewValidation("--authorization-file is only used with --apply")
	case *maxEvents < 1 || *maxEvents > 10000:
		return errs.NewValidation("--max-events must be between 1 and 10000")
	case *republishAfter < v1AuditHistoryImportMinRepublish:
		return errs.NewValidation("--republish-after must be at least 5m")
	case *sleep < 0:
		return errs.NewValidation("--sleep must not be negative")
	}

	options := service.V1AuditDispatchOptions{Apply: *apply, MaxEvents: *maxEvents, RepublishAfter: *republishAfter, Sleep: *sleep}
	var publisher service.V1AuditImportPublisherPort
	if *apply {
		authorization, err := v1auditimport.ReadAuthorization(*authorizationPath)
		if err != nil {
			return err
		}
		options.Authorization = authorization
		if rc.JetStream == nil {
			return errs.NewUnexpected("JetStream is not available")
		}
		js, err := rc.JetStream()
		if err != nil {
			return errs.NewUnexpected("cannot open JetStream", err)
		}
		publisher = jetStreamImportPublisher{js: js}
	}

	events, err := v1auditimport.OpenEvents(ctx, *eventsPath)
	if err != nil {
		return err
	}
	defer events.Close()
	journal, err := v1auditimport.OpenJournal(*statePath, events, *apply)
	if err != nil {
		return err
	}
	defer journal.Close()

	var record func(service.V1AuditImportRecord) error
	if *apply {
		record = journal.Record
	}
	report, err := service.DispatchV1AuditImport(ctx, events.Scan, journal.State, options, publisher, record)
	if report != nil {
		// A run stopped by --max-events scans only part of the file; report the
		// file's total, as reconciliation does.
		report.Events = events.Count
		// Like the other sync commands, the summary is one structured log line, so
		// stdout holds only JSON log lines.
		logger := s.logger
		if logger == nil {
			logger = slog.Default()
		}
		level, outcome := slog.LevelInfo, " complete"
		if err != nil {
			// Partial counts of a run that stopped on an error.
			level, outcome = slog.LevelError, " failed"
		}
		logger.Log(ctx, level, s.Name()+outcome, "failed", err != nil,
			"apply", report.Apply, "events", report.Events, "sent", report.Sent, "would_send", report.WouldSend,
			"retried", report.Retried, "broker_duplicate", report.BrokerDuplicate, "awaiting_history", report.AwaitingHistory,
			"persisted", report.Persisted, "erased", report.Erased, "conflicts", report.Conflicts, "limited", report.Limited)
	}
	return err
}

// jetStreamImportPublisher publishes with the broker de-duplication message ID
// and waits for the PubAck.
type jetStreamImportPublisher struct {
	js jetstream.JetStream
}

func (p jetStreamImportPublisher) PublishImport(ctx context.Context, subject string, payload []byte, msgID string) (service.V1AuditPubAck, error) {
	ctx, cancel := context.WithTimeout(ctx, v1AuditHistoryImportPublishTimeout)
	defer cancel()
	// Expecting the indexer's stream makes the server refuse the publish if
	// another stream captures the subject.
	ack, err := p.js.Publish(ctx, subject, payload, jetstream.WithMsgID(msgID),
		jetstream.WithExpectStream(indexerconstants.StreamNameIndexEvents))
	if err != nil {
		return service.V1AuditPubAck{}, err
	}
	return service.V1AuditPubAck{Stream: ack.Stream, Sequence: ack.Sequence, Duplicate: ack.Duplicate}, nil
}
