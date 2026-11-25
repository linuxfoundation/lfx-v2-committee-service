// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	indexerconstants "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// v1AuditMaxImportLine bounds one line of the frozen events file: a frozen
// event plus its object type and JSON framing.
const v1AuditMaxImportLine = indexertypes.MaxAuditEnvelopeBytes + 4096

// V1AuditImportStatus is an event's latest disposition in the state journal.
type V1AuditImportStatus string

const (
	// V1AuditImportPublished means the broker acknowledged the event (PubAck).
	// That proves broker storage only, not that history was written.
	V1AuditImportPublished V1AuditImportStatus = "published"
	// V1AuditImportPersisted means the history record exists and matches.
	V1AuditImportPersisted V1AuditImportStatus = "persisted"
	// V1AuditImportMissing means a published event has no history record yet.
	V1AuditImportMissing V1AuditImportStatus = "missing"
	// V1AuditImportErased means the history slot is an erasure fence. It is a
	// final disposition: the event is never re-sent or re-identified.
	V1AuditImportErased V1AuditImportStatus = "erased"
	// V1AuditImportConflict means the history record differs from the event.
	V1AuditImportConflict V1AuditImportStatus = "conflict"
)

// V1AuditImportRecord is one line of the append-only state journal. It holds
// identifiers and broker positions only, never event content.
type V1AuditImportRecord struct {
	EventID    string              `json:"event_id"`
	ObjectType string              `json:"object_type"`
	Status     V1AuditImportStatus `json:"status"`
	At         time.Time           `json:"at"`
	Stream     string              `json:"stream,omitempty"`
	Sequence   uint64              `json:"sequence,omitempty"`
	Duplicate  bool                `json:"duplicate,omitempty"`
}

// v1AuditImportJournalHeader is the first journal line. It binds the journal to
// one frozen events file, so a state can never be applied to different events.
type v1AuditImportJournalHeader struct {
	Version      int    `json:"version"`
	Publisher    string `json:"publisher"`
	EventsSHA256 string `json:"events_sha256"`
}

// V1AuditImportState is the replayed journal: each event's latest status and
// when it was last published.
type V1AuditImportState struct {
	EventsSHA256  string
	latest        map[string]V1AuditImportRecord
	lastPublished map[string]time.Time
}

// NewV1AuditImportState starts an empty state bound to an events file.
func NewV1AuditImportState(eventsSHA256 string) *V1AuditImportState {
	return &V1AuditImportState{EventsSHA256: eventsSHA256, latest: map[string]V1AuditImportRecord{}, lastPublished: map[string]time.Time{}}
}

// V1AuditImportJournalHeader returns the header line for a new journal.
func V1AuditImportJournalHeader(eventsSHA256 string) ([]byte, error) {
	line, err := json.Marshal(v1AuditImportJournalHeader{Version: 1, Publisher: V1AuditImportPublisher, EventsSHA256: eventsSHA256})
	if err != nil {
		return nil, errs.NewUnexpected("cannot encode import journal header", err)
	}
	return append(line, '\n'), nil
}

// ReadV1AuditImportState replays a journal. A truncated final line (an
// interrupted append) is ignored; any other malformed line is rejected.
func ReadV1AuditImportState(r io.Reader) (*V1AuditImportState, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, errs.NewValidation("cannot read import state journal")
	}
	// An unterminated final line is an interrupted append: it was never
	// committed, so it is not replayed even if it is complete JSON. The journal's
	// opener cuts it from disk, and the state must match what is kept. A header
	// with no newline therefore leaves no header and is rejected.
	data = data[:bytes.LastIndexByte(data, '\n')+1]
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) == 0 || len(bytes.TrimSpace(lines[0])) == 0 {
		return nil, errs.NewValidation("import state journal has no header")
	}
	var header v1AuditImportJournalHeader
	if decodeV1AuditJSON(lines[0], &header) != nil || header.Version != 1 || header.Publisher != V1AuditImportPublisher || len(header.EventsSHA256) != 64 {
		return nil, errs.NewValidation("invalid import state journal header")
	}
	state := NewV1AuditImportState(header.EventsSHA256)
	for i, line := range lines[1:] {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record V1AuditImportRecord
		if decodeV1AuditJSON(line, &record) != nil || record.EventID == "" || record.At.IsZero() || !v1AuditImportStatusValid(record.Status) ||
			// A publish is recorded only with the indexer stream's acknowledgement.
			(record.Status == V1AuditImportPublished && (record.Stream != indexerconstants.StreamNameIndexEvents || record.Sequence == 0)) {
			return nil, errs.NewValidation(fmt.Sprintf("invalid import state journal record on line %d", i+2))
		}
		if !state.allows(record.EventID, record.Status) {
			return nil, errs.NewValidation(fmt.Sprintf("import state journal record on line %d does not follow the event's earlier records", i+2))
		}
		state.Apply(record)
	}
	return state, nil
}

func v1AuditImportStatusValid(status V1AuditImportStatus) bool {
	switch status {
	case V1AuditImportPublished, V1AuditImportPersisted, V1AuditImportMissing, V1AuditImportErased, V1AuditImportConflict:
		return true
	}
	return false
}

// allows reports whether a record can follow the event's earlier records, as
// dispatch and reconciliation write them: a reconciliation outcome needs an
// earlier publish, and nothing follows a final status.
func (s *V1AuditImportState) allows(eventID string, next V1AuditImportStatus) bool {
	switch s.Status(eventID) {
	case V1AuditImportPersisted, V1AuditImportErased, V1AuditImportConflict:
		return false
	}
	_, published := s.lastPublished[eventID]
	return next == V1AuditImportPublished || published
}

// Apply records an event's new status.
func (s *V1AuditImportState) Apply(record V1AuditImportRecord) {
	s.latest[record.EventID] = record
	if record.Status == V1AuditImportPublished {
		s.lastPublished[record.EventID] = record.At
	}
}

// Status returns an event's latest status, or "" if it was never recorded.
func (s *V1AuditImportState) Status(eventID string) V1AuditImportStatus {
	return s.latest[eventID].Status
}

// V1AuditImportItem is one restored, validated event from the frozen file.
type V1AuditImportItem struct {
	Line       int
	ObjectType string
	EventID    string
	Message    *indexertypes.MessageV2
}

// ScanV1AuditImportEvents streams a frozen events file (lines of
// V1AuditImportEvent), restoring and re-validating each event before calling fn.
// Errors identify lines, never event content.
func ScanV1AuditImportEvents(ctx context.Context, r io.Reader, fn func(V1AuditImportItem) error) error {
	reader := bufio.NewReaderSize(r, 64<<10)
	line := 0
	for {
		if err := ctx.Err(); err != nil {
			return errs.NewUnexpected("import canceled", err)
		}
		data, err := readBoundedLine(reader, v1AuditMaxImportLine)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return v1AuditReadError(ctx, fmt.Sprintf("cannot read import events (line %d)", line+1))
		}
		line++
		if len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		var event V1AuditImportEvent
		if decodeV1AuditJSON(data, &event) != nil {
			return errs.NewValidation(fmt.Sprintf("invalid import event on line %d", line))
		}
		message, err := RestoreV1AuditImportEvent(event)
		if err != nil {
			return errs.NewValidation(fmt.Sprintf("invalid import event on line %d", line))
		}
		envelope, err := v1AuditEnvelopeOf(message)
		// The SDK runs the content validator only when content is present, so a
		// valid envelope of another origin could restore: accept only this
		// publisher's historical imports, with their content.
		if err != nil || envelope.Audit.Origin != indexertypes.AuditOriginHistoricalImport ||
			envelope.Audit.Publisher != V1AuditImportPublisher || envelope.Audit.Content == nil ||
			// Imports carry no actor evidence; a JWT here would be sent to the stream.
			envelope.Audit.Attribution.ActorJWT != "" || envelope.Audit.Attribution.EffectiveActorJWT != "" {
			return errs.NewValidation(fmt.Sprintf("invalid import event on line %d", line))
		}
		if err := fn(V1AuditImportItem{Line: line, ObjectType: event.ObjectType, EventID: envelope.Audit.EventID, Message: message}); err != nil {
			return err
		}
	}
}

// readBoundedLine returns the next line without its line ending, or io.EOF.
func readBoundedLine(reader *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > max+2 {
			return nil, errV1AuditLineTooLong
		}
		line = append(line, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(line) == 0 {
			return nil, io.EOF
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		return bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r")), nil
	}
}

func v1AuditEnvelopeOf(message *indexertypes.MessageV2) (*indexertypes.IndexerMessageEnvelope, error) {
	raw, err := message.MarshalJSON()
	if err != nil {
		return nil, err
	}
	// Decode numbers as json.Number, as the indexer does, so the fingerprint
	// sees the same values.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var envelope indexertypes.IndexerMessageEnvelope
	if err := decoder.Decode(&envelope); err != nil || envelope.Audit == nil || envelope.IndexingConfig == nil {
		return nil, errors.New("frozen import event has no audit metadata")
	}
	return &envelope, nil
}

// V1AuditImportFile summarizes a validated frozen events file.
type V1AuditImportFile struct {
	SHA256 string
	Events int
}

// InspectV1AuditImportEvents validates a whole frozen events file before
// anything is sent: every event must restore, and event IDs must be unique.
func InspectV1AuditImportEvents(ctx context.Context, r io.Reader) (*V1AuditImportFile, error) {
	hash := sha256.New()
	seen := map[string]bool{}
	err := ScanV1AuditImportEvents(ctx, io.TeeReader(r, hash), func(item V1AuditImportItem) error {
		if seen[item.EventID] {
			return errs.NewValidation(fmt.Sprintf("duplicate import event id on line %d", item.Line))
		}
		seen[item.EventID] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(seen) == 0 {
		return nil, errs.NewValidation("import events file has no events")
	}
	return &V1AuditImportFile{SHA256: hex.EncodeToString(hash.Sum(nil)), Events: len(seen)}, nil
}

// V1AuditPubAck is the broker's acknowledgement of one published event.
type V1AuditPubAck struct {
	Stream    string
	Sequence  uint64
	Duplicate bool
}

// V1AuditImportPublisherPort publishes one envelope to the indexing stream with
// a broker de-duplication message ID. Only an acknowledgement from the indexer's
// stream (indexerconstants.StreamNameIndexEvents) counts as published.
type V1AuditImportPublisherPort interface {
	PublishImport(ctx context.Context, subject string, payload []byte, msgID string) (V1AuditPubAck, error)
}

// V1AuditDispatchOptions bounds one dispatch run.
type V1AuditDispatchOptions struct {
	// Apply publishes; without it the run only reports what it would send.
	Apply bool
	// Authorization is the dispatch credential header value ("Bearer <jwt>").
	Authorization string
	// MaxEvents bounds publishes per run.
	MaxEvents int
	// RepublishAfter is how long a published event that is not yet persisted
	// waits before it may be sent again. It must exceed the broker's duplicate
	// window and the indexer's processing time.
	RepublishAfter time.Duration
	// Sleep pauses between publishes.
	Sleep time.Duration
	// Now returns the current time (tests).
	Now func() time.Time
}

// V1AuditDispatchReport counts a dispatch run. It holds counts only.
type V1AuditDispatchReport struct {
	Apply           bool `json:"apply"`
	Events          int  `json:"events"`
	Sent            int  `json:"sent"`
	WouldSend       int  `json:"would_send"`
	BrokerDuplicate int  `json:"broker_duplicate"`
	AwaitingHistory int  `json:"awaiting_history"`
	Persisted       int  `json:"persisted"`
	Erased          int  `json:"erased"`
	Conflicts       int  `json:"conflicts"`
	// Retried counts the sends (or would-sends) that retried an event already published.
	Retried int  `json:"retried"`
	Limited bool `json:"limited"`
}

// DispatchV1AuditImport sends each event that is not yet settled. A settled
// event (persisted, erased or conflicting) is never sent again. A published
// event is re-sent only after RepublishAfter, so a missing history record is
// retried with the same bytes and identity. Never-sent events get the
// MaxEvents budget first, in file order; retries use what is left, so events
// that stay missing cannot hold back the rest of the import. scan passes the
// frozen events to its callback and is called once per pass. Any publish
// failure stops the run: its outcome is unknown and the event is retried next
// run.
func DispatchV1AuditImport(ctx context.Context, scan func(func(io.Reader) error) error, state *V1AuditImportState, options V1AuditDispatchOptions,
	publisher V1AuditImportPublisherPort, record func(V1AuditImportRecord) error) (*V1AuditDispatchReport, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if scan == nil || options.MaxEvents < 1 || options.RepublishAfter <= 0 || (options.Apply && (publisher == nil || options.Authorization == "" || record == nil)) {
		return nil, errs.NewValidation("invalid import dispatch options")
	}
	report := &V1AuditDispatchReport{Apply: options.Apply}
	errLimit := errors.New("limit")
	retriesDue := 0
	// One cutoff for the whole run decides which earlier publishes are due, and
	// events first sent in this run are never retried in it, however long the
	// run takes.
	cutoff := options.Now()
	sentThisRun := map[string]bool{}

	send := func(item V1AuditImportItem) error {
		if report.Sent+report.WouldSend >= options.MaxEvents {
			report.Limited = true
			return errLimit
		}
		if !options.Apply {
			report.WouldSend++
			return nil
		}
		envelope, err := item.Message.Envelope(options.Authorization, ValidateV1AuditImportContent)
		if err != nil {
			return errs.NewValidation(fmt.Sprintf("cannot prepare import event on line %d", item.Line))
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			return errs.NewUnexpected(fmt.Sprintf("cannot encode import event on line %d", item.Line))
		}
		ack, err := publisher.PublishImport(ctx, indexerconstants.IndexPrefix+item.ObjectType, payload, V1AuditImportPublisher+":"+item.EventID)
		if err != nil || ack.Stream != indexerconstants.StreamNameIndexEvents || ack.Sequence == 0 {
			// Do not wrap the broker error: it is not needed to retry and is not ours to log.
			return errs.NewUnexpected(fmt.Sprintf("publish of import event on line %d was not acknowledged; rerun to retry it", item.Line))
		}
		next := V1AuditImportRecord{EventID: item.EventID, ObjectType: item.ObjectType, Status: V1AuditImportPublished,
			At: options.Now().UTC(), Stream: ack.Stream, Sequence: ack.Sequence, Duplicate: ack.Duplicate}
		if err := record(next); err != nil {
			return errs.NewUnexpected("cannot write import state journal")
		}
		state.Apply(next)
		sentThisRun[item.EventID] = true
		report.Sent++
		if ack.Duplicate {
			report.BrokerDuplicate++
		}
		if options.Sleep > 0 {
			select {
			case <-ctx.Done():
				return errs.NewUnexpected("import canceled", ctx.Err())
			case <-time.After(options.Sleep):
			}
		}
		return nil
	}
	pass := func(fn func(V1AuditImportItem) error) error {
		err := scan(func(r io.Reader) error { return ScanV1AuditImportEvents(ctx, r, fn) })
		if errors.Is(err, errLimit) {
			return nil
		}
		return err
	}

	// First pass: count every event and send those never sent.
	err := pass(func(item V1AuditImportItem) error {
		report.Events++
		switch state.Status(item.EventID) {
		case V1AuditImportPersisted:
			report.Persisted++
			return nil
		case V1AuditImportErased:
			report.Erased++
			return nil
		case V1AuditImportConflict:
			report.Conflicts++
			return nil
		}
		published, sent := state.lastPublished[item.EventID]
		switch {
		case !sent:
			return send(item)
		case cutoff.Sub(published) < options.RepublishAfter:
			report.AwaitingHistory++
		default:
			retriesDue++
		}
		return nil
	})
	if err != nil || retriesDue == 0 {
		return report, err
	}
	if report.Limited || report.Sent+report.WouldSend >= options.MaxEvents {
		// The budget went to never-sent events; the retries wait for a later run.
		report.Limited = true
		return report, nil
	}

	// Second pass: retry events published earlier whose wait has passed, with
	// the budget that is left. Events sent in the first pass are not retried.
	err = pass(func(item V1AuditImportItem) error {
		if !v1AuditImportUnsettled(state.Status(item.EventID)) {
			return nil
		}
		published, sent := state.lastPublished[item.EventID]
		if !sent || sentThisRun[item.EventID] || cutoff.Sub(published) < options.RepublishAfter {
			return nil
		}
		before := report.Sent + report.WouldSend
		if err := send(item); err != nil {
			return err
		}
		if report.Sent+report.WouldSend > before {
			report.Retried++
		}
		return nil
	})
	return report, err
}

// v1AuditImportUnsettled reports whether an event with this status may still be sent.
func v1AuditImportUnsettled(status V1AuditImportStatus) bool {
	return status == "" || status == V1AuditImportPublished || status == V1AuditImportMissing
}

// V1AuditHistoryRecord is the stored history document an import is checked
// against. A missing document is nil.
type V1AuditHistoryRecord struct {
	Publisher            string                     `json:"publisher"`
	EventID              string                     `json:"event_id"`
	ObjectType           string                     `json:"object_type"`
	ObjectID             string                     `json:"object_id"`
	Action               string                     `json:"action"`
	ResourceRevision     uint64                     `json:"resource_revision"`
	OccurredAt           time.Time                  `json:"occurred_at"`
	HistoryCheckObject   string                     `json:"history_check_object"`
	HistoryCheckRelation string                     `json:"history_check_relation"`
	Scope                indexertypes.AuditScope    `json:"scope"`
	ActorRef             string                     `json:"actor_ref"`
	EffectiveActorRef    string                     `json:"effective_actor_ref"`
	ActorVerified        bool                       `json:"actor_verified"`
	EffectiveVerified    bool                       `json:"effective_actor_verified"`
	Erased               bool                       `json:"erased"`
	Fingerprint          string                     `json:"fingerprint"`
	Audit                *v1AuditHistoryRecordAudit `json:"audit"`
}

// v1AuditHistoryRecordAudit is the stored copy of the event's audit metadata
// (the indexer's HistoryAudit), compared with the event as well as the
// top-level fields.
type v1AuditHistoryRecordAudit struct {
	EventID          string                         `json:"event_id"`
	Publisher        string                         `json:"publisher"`
	OccurredAt       time.Time                      `json:"occurred_at"`
	ResourceRevision uint64                         `json:"resource_revision"`
	Origin           indexertypes.AuditOrigin       `json:"origin"`
	Attribution      v1AuditHistoryRecordAttributes `json:"attribution"`
	Scope            indexertypes.AuditScope        `json:"scope"`
	Content          *indexertypes.AuditContent     `json:"content"`
}

type v1AuditHistoryRecordAttributes struct {
	ActorRef               string                           `json:"actor_ref"`
	EffectiveActorRef      string                           `json:"effective_actor_ref"`
	ActorVerified          bool                             `json:"actor_verified"`
	EffectiveActorVerified bool                             `json:"effective_actor_verified"`
	ActorIdentity          *indexertypes.AuditActorIdentity `json:"actor_identity"`
	EffectiveActorIdentity *indexertypes.AuditActorIdentity `json:"effective_actor_identity"`
}

// v1AuditLegacyIdentityClear reports whether a stored identity honours the
// indexer contract for its actor: a legacy actor's identity, if present, has
// both username and email null. Other actors' identities are resolved by the
// indexer from its directory and are not compared with the event.
func v1AuditLegacyIdentityClear(ref string, identity *indexertypes.AuditActorIdentity) bool {
	if !strings.HasPrefix(ref, "legacy:") || identity == nil {
		return true
	}
	return identity.Username == nil && identity.Email == nil
}

// V1AuditHistoryReaderPort reads one history document by its ID
// (<publisher>:<event_id>), returning nil when it does not exist.
type V1AuditHistoryReaderPort interface {
	GetHistory(ctx context.Context, id string) (*V1AuditHistoryRecord, error)
}

// V1AuditReconcileReport counts a reconciliation run. It lists event IDs (source
// hashes, never content) only for erased and conflicting events.
type V1AuditReconcileReport struct {
	Events         int      `json:"events"`
	Checked        int      `json:"checked"`
	NeverPublished int      `json:"never_published"`
	Persisted      int      `json:"persisted"`
	Missing        int      `json:"missing"`
	Erased         int      `json:"erased"`
	Conflicts      int      `json:"conflicts"`
	Complete       bool     `json:"complete"`
	ErasedEvents   []string `json:"erased_events"`
	ConflictEvents []string `json:"conflict_events"`
}

// ReconcileV1AuditImport checks every published or missing event against stored
// history and records persisted, missing, erased or conflict. Settled events are
// not re-read. Complete is true only when every event is persisted or erased.
func ReconcileV1AuditImport(ctx context.Context, events io.Reader, state *V1AuditImportState, reader V1AuditHistoryReaderPort,
	record func(V1AuditImportRecord) error, now func() time.Time) (*V1AuditReconcileReport, error) {
	if reader == nil || record == nil {
		return nil, errs.NewValidation("invalid import reconciliation options")
	}
	if now == nil {
		now = time.Now
	}
	report := &V1AuditReconcileReport{ErasedEvents: []string{}, ConflictEvents: []string{}}
	err := ScanV1AuditImportEvents(ctx, events, func(item V1AuditImportItem) error {
		report.Events++
		status := state.Status(item.EventID)
		switch status {
		case "":
			report.NeverPublished++
			return nil
		case V1AuditImportPublished, V1AuditImportMissing:
		default:
			report.count(status, item.EventID)
			return nil
		}
		report.Checked++
		stored, err := reader.GetHistory(ctx, V1AuditImportPublisher+":"+item.EventID)
		if err != nil {
			return v1AuditReadError(ctx, fmt.Sprintf("cannot read history for import event on line %d", item.Line))
		}
		next := V1AuditImportMissing
		switch {
		case stored == nil:
		case stored.Erased:
			next = V1AuditImportErased
		case v1AuditHistoryMatches(item, stored):
			next = V1AuditImportPersisted
		default:
			next = V1AuditImportConflict
		}
		report.count(next, item.EventID)
		if next == status {
			return nil
		}
		update := V1AuditImportRecord{EventID: item.EventID, ObjectType: item.ObjectType, Status: next, At: now().UTC()}
		if err := record(update); err != nil {
			return errs.NewUnexpected("cannot write import state journal")
		}
		state.Apply(update)
		return nil
	})
	if err != nil {
		return report, err
	}
	report.Complete = report.Persisted+report.Erased == report.Events
	return report, nil
}

func (r *V1AuditReconcileReport) count(status V1AuditImportStatus, eventID string) {
	switch status {
	case V1AuditImportPersisted:
		r.Persisted++
	case V1AuditImportMissing, V1AuditImportPublished:
		r.Missing++
	case V1AuditImportErased:
		r.Erased++
		r.ErasedEvents = append(r.ErasedEvents, eventID)
	case V1AuditImportConflict:
		r.Conflicts++
		r.ConflictEvents = append(r.ConflictEvents, eventID)
	}
}

// ExpectedV1AuditHistoryRecord returns the history record the indexer stores
// for a frozen import event: the values reconciliation compares.
func ExpectedV1AuditHistoryRecord(event V1AuditImportEvent) (*V1AuditHistoryRecord, error) {
	message, err := RestoreV1AuditImportEvent(event)
	if err != nil {
		return nil, err
	}
	return v1AuditExpectedRecord(V1AuditImportItem{ObjectType: event.ObjectType, Message: message})
}

func v1AuditExpectedRecord(item V1AuditImportItem) (*V1AuditHistoryRecord, error) {
	envelope, err := v1AuditEnvelopeOf(item.Message)
	if err != nil {
		return nil, err
	}
	if envelope.Audit.Content == nil {
		return nil, errors.New("frozen import event has no content")
	}
	fingerprint, err := v1AuditImportFingerprint(envelope, item.ObjectType)
	if err != nil {
		return nil, err
	}
	audit := envelope.Audit
	return &V1AuditHistoryRecord{
		Publisher: audit.Publisher, EventID: audit.EventID, ObjectType: item.ObjectType, ObjectID: envelope.IndexingConfig.ObjectID,
		Action: string(envelope.Action), ResourceRevision: audit.ResourceRevision, OccurredAt: audit.OccurredAt.UTC(),
		HistoryCheckObject: envelope.IndexingConfig.HistoryCheckObject, HistoryCheckRelation: envelope.IndexingConfig.HistoryCheckRelation,
		Scope: audit.Scope, ActorRef: audit.Attribution.ActorRef, EffectiveActorRef: audit.Attribution.EffectiveActorRef,
		Fingerprint: fingerprint,
		Audit: &v1AuditHistoryRecordAudit{
			EventID: audit.EventID, Publisher: audit.Publisher, OccurredAt: audit.OccurredAt.UTC(),
			ResourceRevision: audit.ResourceRevision, Origin: audit.Origin, Scope: audit.Scope, Content: audit.Content,
			Attribution: v1AuditHistoryRecordAttributes{ActorRef: audit.Attribution.ActorRef, EffectiveActorRef: audit.Attribution.EffectiveActorRef},
		},
	}, nil
}

// v1AuditHistoryMatches compares the stored fingerprint, which covers every
// immutable part of the event including data and tags, and the stored values
// it is built from, including the snapshots.
func v1AuditHistoryMatches(item V1AuditImportItem, stored *V1AuditHistoryRecord) bool {
	want, err := v1AuditExpectedRecord(item)
	if err != nil || stored.Audit == nil || stored.Audit.Content == nil {
		return false
	}
	return stored.Fingerprint == want.Fingerprint &&
		stored.Publisher == want.Publisher && stored.EventID == want.EventID &&
		stored.ObjectType == want.ObjectType && stored.ObjectID == want.ObjectID &&
		stored.Action == want.Action && stored.ResourceRevision == want.ResourceRevision &&
		stored.OccurredAt.Equal(want.OccurredAt) &&
		stored.HistoryCheckObject == want.HistoryCheckObject && stored.HistoryCheckRelation == want.HistoryCheckRelation &&
		reflect.DeepEqual(nonNil(stored.Scope.Before), nonNil(want.Scope.Before)) &&
		reflect.DeepEqual(nonNil(stored.Scope.After), nonNil(want.Scope.After)) &&
		stored.ActorRef == want.ActorRef && stored.EffectiveActorRef == want.EffectiveActorRef &&
		// Imports carry no actor JWT, so the indexer never marks their actors verified.
		!stored.ActorVerified && !stored.EffectiveVerified &&
		!stored.Audit.Attribution.ActorVerified && !stored.Audit.Attribution.EffectiveActorVerified &&
		v1AuditLegacyIdentityClear(want.ActorRef, stored.Audit.Attribution.ActorIdentity) &&
		v1AuditLegacyIdentityClear(want.EffectiveActorRef, stored.Audit.Attribution.EffectiveActorIdentity) &&
		stored.Audit.EventID == want.EventID && stored.Audit.Publisher == want.Publisher &&
		stored.Audit.OccurredAt.Equal(want.OccurredAt) && stored.Audit.ResourceRevision == want.ResourceRevision &&
		stored.Audit.Attribution.ActorRef == want.ActorRef && stored.Audit.Attribution.EffectiveActorRef == want.EffectiveActorRef &&
		reflect.DeepEqual(nonNil(stored.Audit.Scope.Before), nonNil(want.Scope.Before)) &&
		reflect.DeepEqual(nonNil(stored.Audit.Scope.After), nonNil(want.Scope.After)) &&
		stored.Audit.Origin == indexertypes.AuditOriginHistoricalImport &&
		stored.Audit.Content.Schema == want.Audit.Content.Schema &&
		stored.Audit.Content.Representation == want.Audit.Content.Representation &&
		jsonEqual(stored.Audit.Content.Before, want.Audit.Content.Before) &&
		jsonEqual(stored.Audit.Content.After, want.Audit.Content.After)
}

// v1AuditImportFingerprint computes the indexer's fingerprint of an event's
// immutable content (contracts.AuditFingerprint at indexer dd650b2): headers and
// actor JWTs are excluded, and the JSON is normalized with numbers kept exact.
func v1AuditImportFingerprint(envelope *indexertypes.IndexerMessageEnvelope, objectType string) (string, error) {
	audit := *envelope.Audit
	audit.OccurredAt = audit.OccurredAt.UTC()
	audit.Attribution.ActorJWT, audit.Attribution.EffectiveActorJWT = "", ""
	immutable := struct {
		Action         indexerconstants.MessageAction `json:"action"`
		ObjectType     string                         `json:"object_type"`
		Data           any                            `json:"data"`
		Tags           []string                       `json:"tags"`
		IndexingConfig *indexertypes.IndexingConfig   `json:"indexing_config"`
		Audit          *indexertypes.AuditEvent       `json:"audit"`
	}{envelope.Action, objectType, envelope.Data, envelope.Tags, envelope.IndexingConfig, &audit}
	encoded, err := json.Marshal(immutable)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return "", err
	}
	if encoded, err = json.Marshal(normalized); err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// jsonEqual compares two JSON values semantically; an absent side equals an
// absent side only.
func jsonEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}
