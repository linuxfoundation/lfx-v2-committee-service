// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// v1ImportEventsFile builds a frozen events file from every fixture lifecycle.
func v1ImportEventsFile(t *testing.T) []byte {
	t.Helper()
	var file bytes.Buffer
	for _, member := range []bool{false, true} {
		for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
			row, mappings := v1HistoryFixture(member, action)
			row["id"] = map[string]string{"INSERT": "00000000-0000-4000-8000-000000000101", "UPDATE": "00000000-0000-4000-8000-000000000102", "DELETE": "00000000-0000-4000-8000-000000000103"}[action]
			if member {
				row["id"] = strings.Replace(row["id"].(string), "-000000000", "-100000000", 1)
			}
			var events []V1AuditImportEvent
			_, err := PlanV1AuditHistory(context.Background(), strings.NewReader(v1HistoryJSON(t, row)), V1AuditOptions{Source: mappings.Source, Mappings: mappings}, func(event model.V1AuditPlanEvent) error {
				events = append(events, V1AuditImportEvent{ObjectType: string(event.ResourceType), Event: event.Frozen})
				return nil
			})
			require.NoError(t, err)
			for _, event := range events {
				line, err := json.Marshal(event)
				require.NoError(t, err)
				file.Write(append(line, '\n'))
			}
		}
	}
	return file.Bytes()
}

type fakeImportPublisher struct {
	calls     []fakePublish
	err       error
	ack       *V1AuditPubAck
	duplicate bool
}

type fakePublish struct {
	subject, msgID string
	envelope       indexertypes.IndexerMessageEnvelope
}

func (p *fakeImportPublisher) PublishImport(_ context.Context, subject string, payload []byte, msgID string) (V1AuditPubAck, error) {
	if p.err != nil {
		return V1AuditPubAck{}, p.err
	}
	var envelope indexertypes.IndexerMessageEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return V1AuditPubAck{}, err
	}
	p.calls = append(p.calls, fakePublish{subject: subject, msgID: msgID, envelope: envelope})
	if p.ack != nil {
		return *p.ack, nil
	}
	return V1AuditPubAck{Stream: "index-events", Sequence: uint64(len(p.calls)), Duplicate: p.duplicate}, nil
}

// fakeHistory serves history records; stored builds the record the indexer
// would store for an event.
type fakeHistory struct {
	records map[string]*V1AuditHistoryRecord
	err     error
}

func (h *fakeHistory) GetHistory(_ context.Context, id string) (*V1AuditHistoryRecord, error) {
	if h.err != nil {
		return nil, h.err
	}
	return h.records[id], nil
}

func storedHistory(t *testing.T, item V1AuditImportItem) *V1AuditHistoryRecord {
	t.Helper()
	record, err := v1AuditExpectedRecord(item)
	require.NoError(t, err)
	return record
}

// scanBytes passes a frozen events file to each dispatch pass.
func scanBytes(file []byte) func(func(io.Reader) error) error {
	return func(fn func(io.Reader) error) error { return fn(bytes.NewReader(file)) }
}

func scanItems(t *testing.T, file []byte) []V1AuditImportItem {
	t.Helper()
	var items []V1AuditImportItem
	require.NoError(t, ScanV1AuditImportEvents(context.Background(), bytes.NewReader(file), func(item V1AuditImportItem) error {
		items = append(items, item)
		return nil
	}))
	return items
}

func TestInspectV1AuditImportEvents(t *testing.T) {
	file := v1ImportEventsFile(t)
	inspected, err := InspectV1AuditImportEvents(context.Background(), bytes.NewReader(file))
	require.NoError(t, err)
	require.Equal(t, 9, inspected.Events)
	require.Len(t, inspected.SHA256, 64)
	again, err := InspectV1AuditImportEvents(context.Background(), bytes.NewReader(file))
	require.NoError(t, err)
	require.Equal(t, inspected.SHA256, again.SHA256)

	first := bytes.SplitN(file, []byte("\n"), 2)[0]
	var event V1AuditImportEvent
	require.NoError(t, json.Unmarshal(first, &event))
	withHeaders := strings.Replace(string(event.Event), `"headers":null`, `"headers":{"authorization":"Bearer private-marker"}`, 1)
	require.NotEqual(t, string(event.Event), withHeaders)
	tampered, err := json.Marshal(V1AuditImportEvent{ObjectType: event.ObjectType, Event: json.RawMessage(withHeaders)})
	require.NoError(t, err)
	wrongType, err := json.Marshal(V1AuditImportEvent{ObjectType: "committee_invite", Event: event.Event})
	require.NoError(t, err)

	for name, bad := range map[string]string{
		"empty file":         "",
		"duplicate event id": string(first) + "\n" + string(first) + "\n",
		"malformed line":     string(first) + "\n{\n",
		"carries headers":    string(tampered) + "\n",
		"unsupported type":   string(wrongType) + "\n",
		"unknown key":        strings.Replace(string(first), `{"object_type"`, `{"private_marker":1,"object_type"`, 1) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := InspectV1AuditImportEvents(context.Background(), strings.NewReader(bad))
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-marker")
		})
	}
}

// relabel rewrites a frozen event's audit origin, optionally dropping its content.
func relabel(t *testing.T, item V1AuditImportItem, origin string, dropContent bool) string {
	t.Helper()
	raw, err := item.Message.MarshalJSON()
	require.NoError(t, err)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(raw, &envelope))
	audit := envelope["audit"].(map[string]any)
	audit["origin"] = origin
	if dropContent {
		delete(audit, "content")
	}
	event, err := json.Marshal(envelope)
	require.NoError(t, err)
	// The SDK restores it: only the scanner's own check rejects it.
	_, err = RestoreV1AuditImportEvent(V1AuditImportEvent{ObjectType: item.ObjectType, Event: event})
	require.NoError(t, err)
	line, err := json.Marshal(V1AuditImportEvent{ObjectType: item.ObjectType, Event: event})
	require.NoError(t, err)
	return string(line) + "\n"
}

// withJWT adds actor evidence to a frozen event with a user actor.
func withJWT(t *testing.T, item V1AuditImportItem, field string) string {
	t.Helper()
	raw, err := item.Message.MarshalJSON()
	require.NoError(t, err)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(raw, &envelope))
	attribution := envelope["audit"].(map[string]any)["attribution"].(map[string]any)
	require.True(t, strings.HasPrefix(attribution["actor_ref"].(string), "user:"))
	attribution[field] = "eyJ.private-marker.sig"
	event, err := json.Marshal(envelope)
	require.NoError(t, err)
	_, err = RestoreV1AuditImportEvent(V1AuditImportEvent{ObjectType: item.ObjectType, Event: event})
	require.NoError(t, err, "the SDK restores it: only the scanner's own check rejects it")
	line, err := json.Marshal(V1AuditImportEvent{ObjectType: item.ObjectType, Event: event})
	require.NoError(t, err)
	return string(line) + "\n"
}

func TestScanV1AuditImportEvents_ImportsOnly(t *testing.T) {
	item := scanItems(t, v1ImportEventsFile(t))[0]
	for name, line := range map[string]string{
		"backfill without content": relabel(t, item, "backfill", true),
		"mutation with content":    relabel(t, item, "mutation", false),
		"actor JWT":                withJWT(t, item, "actor_jwt"),
		"effective actor JWT":      withJWT(t, item, "effective_actor_jwt"),
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			err := ScanV1AuditImportEvents(context.Background(), strings.NewReader(line), func(V1AuditImportItem) error { called = true; return nil })
			require.ErrorContains(t, err, "invalid import event on line 1")
			require.NotContains(t, err.Error(), "private-marker")
			require.False(t, called, "a non-import event is never passed on")
		})
	}
}

func TestReadV1AuditImportState(t *testing.T) {
	header, err := V1AuditImportJournalHeader(strings.Repeat("a", 64))
	require.NoError(t, err)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	record := func(id string, status V1AuditImportStatus) string {
		r := V1AuditImportRecord{EventID: id, ObjectType: "committee", Status: status, At: at}
		if status == V1AuditImportPublished {
			r.Stream, r.Sequence = "index-events", 1
		}
		line, err := json.Marshal(r)
		require.NoError(t, err)
		return string(line) + "\n"
	}
	journal := string(header) + record("e1", V1AuditImportPublished) + record("e1", V1AuditImportPersisted) + record("e2", V1AuditImportPublished)

	state, err := ReadV1AuditImportState(strings.NewReader(journal))
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("a", 64), state.EventsSHA256)
	require.Equal(t, V1AuditImportPersisted, state.Status("e1"))
	require.Equal(t, V1AuditImportPublished, state.Status("e2"))
	require.Equal(t, at, state.lastPublished["e2"])

	// Every sequence dispatch and reconciliation write replays.
	state, err = ReadV1AuditImportState(strings.NewReader(string(header) + record("e4", V1AuditImportPublished) +
		record("e4", V1AuditImportMissing) + record("e4", V1AuditImportPublished) + record("e4", V1AuditImportMissing) + record("e4", V1AuditImportErased)))
	require.NoError(t, err)
	require.Equal(t, V1AuditImportErased, state.Status("e4"))

	// An interrupted final append is ignored, even when it is complete JSON
	// missing only its newline: it was never committed.
	state, err = ReadV1AuditImportState(strings.NewReader(journal + `{"event_id":"e3","sta`))
	require.NoError(t, err)
	require.Empty(t, state.Status("e3"))
	state, err = ReadV1AuditImportState(strings.NewReader(journal + strings.TrimSuffix(record("e2", V1AuditImportPersisted), "\n")))
	require.NoError(t, err)
	require.Equal(t, V1AuditImportPublished, state.Status("e2"))

	for name, bad := range map[string]string{
		"no header":               record("e1", V1AuditImportPublished),
		"unterminated header":     strings.TrimSuffix(string(header), "\n"),
		"bad header":              `{"version":2}` + "\n",
		"corrupt middle":          string(header) + "{\n" + record("e1", V1AuditImportPublished),
		"unknown status":          string(header) + strings.Replace(record("e1", V1AuditImportPublished), "published", "private-marker", 1),
		"settled without publish": string(header) + record("e1", V1AuditImportPersisted),
		"publish without ack":     string(header) + strings.Replace(record("e1", V1AuditImportPublished), `"sequence":1`, `"sequence":0`, 1),
		"publish on other stream": string(header) + strings.Replace(record("e1", V1AuditImportPublished), `"index-events"`, `"other-stream"`, 1),
		"missing without publish": string(header) + record("e1", V1AuditImportMissing),
		"record after final":      string(header) + record("e1", V1AuditImportPublished) + record("e1", V1AuditImportConflict) + record("e1", V1AuditImportPublished),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadV1AuditImportState(strings.NewReader(bad))
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-marker")
		})
	}
}

func TestDispatchV1AuditImport(t *testing.T) {
	file := v1ImportEventsFile(t)
	items := scanItems(t, file)
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	options := func(now time.Time) V1AuditDispatchOptions {
		return V1AuditDispatchOptions{Apply: true, Authorization: "Bearer test-token", MaxEvents: 100, RepublishAfter: 15 * time.Minute,
			Now: func() time.Time { return now }}
	}

	t.Run("dry run sends nothing", func(t *testing.T) {
		publisher := &fakeImportPublisher{}
		opts := options(start)
		opts.Apply = false
		report, err := DispatchV1AuditImport(context.Background(), scanBytes(file), NewV1AuditImportState("x"), opts, publisher, nil)
		require.NoError(t, err)
		require.Equal(t, 9, report.WouldSend)
		require.Zero(t, report.Sent)
		require.Empty(t, publisher.calls)
	})

	t.Run("apply publishes each event once, then waits for history", func(t *testing.T) {
		publisher := &fakeImportPublisher{}
		state := NewV1AuditImportState("x")
		var journal []V1AuditImportRecord
		record := func(r V1AuditImportRecord) error { journal = append(journal, r); return nil }

		report, err := DispatchV1AuditImport(context.Background(), scanBytes(file), state, options(start), publisher, record)
		require.NoError(t, err)
		require.Equal(t, 9, report.Sent)
		require.Len(t, publisher.calls, 9)
		require.Len(t, journal, 9)
		for i, call := range publisher.calls {
			require.Equal(t, "lfx.index."+items[i].ObjectType, call.subject)
			require.Equal(t, V1AuditImportPublisher+":"+items[i].EventID, call.msgID)
			require.Equal(t, "Bearer test-token", call.envelope.Headers["authorization"], "the dispatch credential is attached on send")
			require.Equal(t, indexertypes.AuditOriginHistoricalImport, call.envelope.Audit.Origin)
			require.Equal(t, V1AuditImportPublished, journal[i].Status)
			require.NotZero(t, journal[i].Sequence)
		}

		// Within the republish window nothing is sent again.
		report, err = DispatchV1AuditImport(context.Background(), scanBytes(file), state, options(start.Add(time.Minute)), publisher, record)
		require.NoError(t, err)
		require.Equal(t, 9, report.AwaitingHistory)
		require.Len(t, publisher.calls, 9)

		// Settled events are never sent again; unsettled ones are re-sent after the window.
		state.Apply(V1AuditImportRecord{EventID: items[0].EventID, Status: V1AuditImportPersisted, At: start})
		state.Apply(V1AuditImportRecord{EventID: items[1].EventID, Status: V1AuditImportErased, At: start})
		state.Apply(V1AuditImportRecord{EventID: items[2].EventID, Status: V1AuditImportConflict, At: start})
		state.Apply(V1AuditImportRecord{EventID: items[3].EventID, Status: V1AuditImportMissing, At: start})
		report, err = DispatchV1AuditImport(context.Background(), scanBytes(file), state, options(start.Add(time.Hour)), publisher, record)
		require.NoError(t, err)
		require.Equal(t, 1, report.Persisted)
		require.Equal(t, 1, report.Erased)
		require.Equal(t, 1, report.Conflicts)
		require.Equal(t, 6, report.Sent)
		require.Equal(t, items[3].EventID, strings.TrimPrefix(publisher.calls[9].msgID, V1AuditImportPublisher+":"), "a missing event is re-sent with the same identity")
	})

	t.Run("never-sent events get the budget before retries", func(t *testing.T) {
		publisher := &fakeImportPublisher{}
		state := NewV1AuditImportState("x")
		// The first event was sent long ago and its history is still missing.
		state.Apply(V1AuditImportRecord{EventID: items[0].EventID, Status: V1AuditImportPublished, At: start.Add(-time.Hour)})
		state.Apply(V1AuditImportRecord{EventID: items[0].EventID, Status: V1AuditImportMissing, At: start.Add(-time.Hour)})
		opts := options(start)
		opts.MaxEvents = 1
		record := func(V1AuditImportRecord) error { return nil }

		// Each one-event run sends the next never-sent event, not the stuck retry.
		for i := 1; i < len(items); i++ {
			report, err := DispatchV1AuditImport(context.Background(), scanBytes(file), state, opts, publisher, record)
			require.NoError(t, err)
			require.Equal(t, 1, report.Sent)
			require.Zero(t, report.Retried)
			require.Equal(t, V1AuditImportPublisher+":"+items[i].EventID, publisher.calls[len(publisher.calls)-1].msgID)
			require.True(t, report.Limited, "the due retry is still waiting for budget")
		}
		// With nothing left unsent, the budget goes to the retry.
		report, err := DispatchV1AuditImport(context.Background(), scanBytes(file), state, opts, publisher, record)
		require.NoError(t, err)
		require.Equal(t, 1, report.Retried)
		require.Equal(t, V1AuditImportPublisher+":"+items[0].EventID, publisher.calls[len(publisher.calls)-1].msgID)
		require.False(t, report.Limited)

		// A preview counts retries the same way.
		preview := options(start.Add(time.Hour))
		preview.Apply = false
		report, err = DispatchV1AuditImport(context.Background(), scanBytes(file), state, preview, nil, nil)
		require.NoError(t, err)
		require.Equal(t, len(items), report.WouldSend)
		require.Equal(t, len(items), report.Retried)
	})

	t.Run("events sent in this run are never retried in it", func(t *testing.T) {
		// A slow run: each call to Now is an hour later, so the first pass's sends
		// would be "due" again by the second pass without a single cutoff.
		publisher := &fakeImportPublisher{}
		state := NewV1AuditImportState("x")
		state.Apply(V1AuditImportRecord{EventID: items[len(items)-1].EventID, Status: V1AuditImportPublished, At: start.Add(-time.Hour)})
		clock := start
		opts := options(start)
		opts.Now = func() time.Time { clock = clock.Add(time.Hour); return clock }
		opts.MaxEvents = len(items)
		report, err := DispatchV1AuditImport(context.Background(), scanBytes(file), state, opts, publisher, func(V1AuditImportRecord) error { return nil })
		require.NoError(t, err)
		require.Equal(t, len(items), report.Sent)
		require.Equal(t, 1, report.Retried)
		require.Equal(t, V1AuditImportPublisher+":"+items[len(items)-1].EventID, publisher.calls[len(publisher.calls)-1].msgID,
			"the older retry gets the last slot")
	})

	t.Run("max events limits a run", func(t *testing.T) {
		publisher := &fakeImportPublisher{}
		opts := options(start)
		opts.MaxEvents = 4
		report, err := DispatchV1AuditImport(context.Background(), scanBytes(file), NewV1AuditImportState("x"), opts, publisher, func(V1AuditImportRecord) error { return nil })
		require.NoError(t, err)
		require.Equal(t, 4, report.Sent)
		require.True(t, report.Limited)
	})

	t.Run("broker duplicates are counted", func(t *testing.T) {
		publisher := &fakeImportPublisher{duplicate: true}
		report, err := DispatchV1AuditImport(context.Background(), scanBytes(file), NewV1AuditImportState("x"), options(start), publisher, func(V1AuditImportRecord) error { return nil })
		require.NoError(t, err)
		require.Equal(t, 9, report.BrokerDuplicate)
	})

	for name, publisher := range map[string]*fakeImportPublisher{
		"publish error":       {err: errors.New("private-marker")},
		"missing acknowledge": {ack: &V1AuditPubAck{}},
		"other stream":        {ack: &V1AuditPubAck{Stream: "other-stream", Sequence: 1}},
	} {
		t.Run(name+" stops the run without recording", func(t *testing.T) {
			var journal []V1AuditImportRecord
			_, err := DispatchV1AuditImport(context.Background(), scanBytes(file), NewV1AuditImportState("x"), options(start), publisher,
				func(r V1AuditImportRecord) error { journal = append(journal, r); return nil })
			require.ErrorContains(t, err, "rerun to retry")
			require.NotContains(t, err.Error(), "private-marker")
			require.Empty(t, journal)
		})
	}

	t.Run("journal failure stops the run", func(t *testing.T) {
		publisher := &fakeImportPublisher{}
		_, err := DispatchV1AuditImport(context.Background(), scanBytes(file), NewV1AuditImportState("x"), options(start), publisher,
			func(V1AuditImportRecord) error { return errors.New("disk full") })
		require.ErrorContains(t, err, "journal")
		require.Len(t, publisher.calls, 1)
	})

	t.Run("invalid options", func(t *testing.T) {
		for _, opts := range []V1AuditDispatchOptions{
			{Apply: true, MaxEvents: 1, RepublishAfter: time.Minute},
			{Authorization: "Bearer x", RepublishAfter: time.Minute},
			{Authorization: "Bearer x", MaxEvents: 1},
		} {
			_, err := DispatchV1AuditImport(context.Background(), scanBytes(file), NewV1AuditImportState("x"), opts, &fakeImportPublisher{}, func(V1AuditImportRecord) error { return nil })
			require.Error(t, err)
		}
	})
}

// TestV1AuditImportFingerprint_MatchesIndexer pins the copied fingerprint to the
// value the indexer's own contracts.AuditFingerprint (indexer dd650b2) computes
// for the first fixture event, decoded from its wire form. A mismatch would mark
// every stored event as a conflict.
func TestV1AuditImportFingerprint_MatchesIndexer(t *testing.T) {
	item := scanItems(t, v1ImportEventsFile(t))[0]
	envelope, err := v1AuditEnvelopeOf(item.Message)
	require.NoError(t, err)
	fingerprint, err := v1AuditImportFingerprint(envelope, item.ObjectType)
	require.NoError(t, err)
	require.Equal(t, "73e0594cfa4ac16608e2c0541f69d55914773861625f5349662dbe35d7de8d2c", fingerprint)
}

func TestV1AuditLegacyIdentityClear(t *testing.T) {
	value := "first.last@example.com"
	for _, tc := range []struct {
		ref      string
		identity *indexertypes.AuditActorIdentity
		clear    bool
	}{
		{"legacy:fixture-v1/actor-1", nil, true},
		{"legacy:fixture-v1/actor-1", &indexertypes.AuditActorIdentity{}, true},
		{"legacy:fixture-v1/actor-1", &indexertypes.AuditActorIdentity{Email: &value}, false},
		{"legacy:fixture-v1/actor-1", &indexertypes.AuditActorIdentity{Username: &value}, false},
		// A user actor's identity is resolved by the indexer from its directory.
		{"user:first-last", &indexertypes.AuditActorIdentity{Email: &value}, true},
	} {
		require.Equal(t, tc.clear, v1AuditLegacyIdentityClear(tc.ref, tc.identity), "%s %+v", tc.ref, tc.identity)
	}
}

func TestReconcileV1AuditImport(t *testing.T) {
	file := v1ImportEventsFile(t)
	items := scanItems(t, file)
	now := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	state := NewV1AuditImportState("x")
	for _, item := range items[:5] {
		state.Apply(V1AuditImportRecord{EventID: item.EventID, Status: V1AuditImportPublished, At: now()})
	}
	conflicting := storedHistory(t, items[2])
	conflicting.Fingerprint = strings.Repeat("0", 64) // e.g. different data or tags
	changedSnapshot := storedHistory(t, items[3])
	content := *changedSnapshot.Audit.Content
	content.After = json.RawMessage(strings.Replace(string(content.After), `"uid"`, `"uid_changed"`, 1))
	require.NotEqual(t, string(changedSnapshot.Audit.Content.After), string(content.After))
	// Only the snapshot differs: the metadata still matches, so the snapshot
	// comparison is what rejects it.
	changedSnapshot.Audit.Content = &content
	history := &fakeHistory{records: map[string]*V1AuditHistoryRecord{
		V1AuditImportPublisher + ":" + items[0].EventID: storedHistory(t, items[0]),
		V1AuditImportPublisher + ":" + items[1].EventID: {Publisher: V1AuditImportPublisher, EventID: items[1].EventID, Erased: true},
		V1AuditImportPublisher + ":" + items[2].EventID: conflicting,
		V1AuditImportPublisher + ":" + items[3].EventID: changedSnapshot,
		// items[4] is published but has no history yet.
	}}
	var journal []V1AuditImportRecord
	record := func(r V1AuditImportRecord) error { journal = append(journal, r); return nil }

	report, err := ReconcileV1AuditImport(context.Background(), bytes.NewReader(file), state, history, record, now)
	require.NoError(t, err)
	require.Equal(t, 9, report.Events)
	require.Equal(t, 5, report.Checked)
	require.Equal(t, 4, report.NeverPublished)
	require.Equal(t, 1, report.Persisted)
	require.Equal(t, 1, report.Erased)
	require.Equal(t, 2, report.Conflicts)
	require.Equal(t, 1, report.Missing)
	require.False(t, report.Complete)
	require.Equal(t, []string{items[1].EventID}, report.ErasedEvents)
	require.Equal(t, []string{items[2].EventID, items[3].EventID}, report.ConflictEvents)
	require.Len(t, journal, 5)
	require.Equal(t, V1AuditImportPersisted, state.Status(items[0].EventID))
	require.Equal(t, V1AuditImportMissing, state.Status(items[4].EventID))

	// Settled events are not read again; a still-missing event is not re-recorded.
	history.err = nil
	journal = nil
	report, err = ReconcileV1AuditImport(context.Background(), bytes.NewReader(file), state, history, record, now)
	require.NoError(t, err)
	require.Equal(t, 1, report.Checked)
	require.Empty(t, journal)

	// Stored nested audit metadata and the actor-verified flags must match too.
	for name, change := range map[string]func(*V1AuditHistoryRecord){
		"nested event id":        func(r *V1AuditHistoryRecord) { r.Audit.EventID = "other" },
		"nested publisher":       func(r *V1AuditHistoryRecord) { r.Audit.Publisher = "committee-service" },
		"nested revision":        func(r *V1AuditHistoryRecord) { r.Audit.ResourceRevision++ },
		"nested occurred at":     func(r *V1AuditHistoryRecord) { r.Audit.OccurredAt = r.Audit.OccurredAt.Add(time.Second) },
		"nested actor":           func(r *V1AuditHistoryRecord) { r.Audit.Attribution.ActorRef = "legacy:other/x" },
		"nested scope":           func(r *V1AuditHistoryRecord) { r.Audit.Scope.After = []string{"other"} },
		"actor verified":         func(r *V1AuditHistoryRecord) { r.ActorVerified = true },
		"effective verified":     func(r *V1AuditHistoryRecord) { r.EffectiveVerified = true },
		"nested actor verified":  func(r *V1AuditHistoryRecord) { r.Audit.Attribution.ActorVerified = true },
		"nested effective verif": func(r *V1AuditHistoryRecord) { r.Audit.Attribution.EffectiveActorVerified = true },
	} {
		t.Run(name, func(t *testing.T) {
			stored := storedHistory(t, items[0])
			require.True(t, v1AuditHistoryMatches(items[0], stored))
			change(stored)
			require.False(t, v1AuditHistoryMatches(items[0], stored))
		})
	}

	// Complete once every event is persisted or erased.
	all := NewV1AuditImportState("x")
	stored := map[string]*V1AuditHistoryRecord{}
	for _, item := range items {
		all.Apply(V1AuditImportRecord{EventID: item.EventID, Status: V1AuditImportPublished, At: now()})
		stored[V1AuditImportPublisher+":"+item.EventID] = storedHistory(t, item)
	}
	report, err = ReconcileV1AuditImport(context.Background(), bytes.NewReader(file), all, &fakeHistory{records: stored}, func(V1AuditImportRecord) error { return nil }, now)
	require.NoError(t, err)
	require.True(t, report.Complete)
	require.Equal(t, 9, report.Persisted)

	// A read failure stops reconciliation without recording anything.
	failing := NewV1AuditImportState("x")
	failing.Apply(V1AuditImportRecord{EventID: items[0].EventID, Status: V1AuditImportPublished, At: now()})
	journal = nil
	_, err = ReconcileV1AuditImport(context.Background(), bytes.NewReader(file), failing, &fakeHistory{err: errors.New("private-marker")}, record, now)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-marker")
	require.Empty(t, journal)
}
