// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	internalsvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// stubGroupWeeklyBriefReader returns canned hits/misses (and an optional error
// to exercise the infrastructure-failure path).
type stubGroupWeeklyBriefReader struct {
	brief    *model.GroupWeeklyBrief
	throttle []byte
	err      error
}

func (s *stubGroupWeeklyBriefReader) GetCurrent(_ context.Context, _ string, _ time.Time) (*model.GroupWeeklyBrief, []byte, error) {
	return s.brief, s.throttle, s.err
}

// stubCommitteeReader implements internalsvc.CommitteeReader for the GetBase
// path used by the handler. Only GetBase is exercised; the rest panic so a
// test that accidentally relies on them fails loudly.
type stubCommitteeReader struct {
	base *model.CommitteeBase
	rev  uint64
	err  error
}

func (r *stubCommitteeReader) GetBase(_ context.Context, _ string) (*model.CommitteeBase, uint64, error) {
	return r.base, r.rev, r.err
}

func (r *stubCommitteeReader) GetSettings(_ context.Context, _ string) (*model.CommitteeSettings, uint64, error) {
	panic("not used")
}

func (r *stubCommitteeReader) GetMember(_ context.Context, _, _ string) (*model.CommitteeMember, uint64, error) {
	panic("not used")
}

func (r *stubCommitteeReader) ListMembersByCommittee(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	panic("not used")
}

func (r *stubCommitteeReader) ListAllUIDs(_ context.Context) ([]string, error) { panic("not used") }

func (r *stubCommitteeReader) GetBaseAttributeValue(_ context.Context, _, _ string) (any, error) {
	panic("not used")
}

func (r *stubCommitteeReader) GetMemberRevision(_ context.Context, _ string) (uint64, error) {
	panic("not used")
}

func (r *stubCommitteeReader) ListInvites(_ context.Context, _ string) ([]*model.CommitteeInvite, error) {
	panic("not used")
}

// ensure the stub satisfies the interface at compile time
var _ internalsvc.CommitteeReader = (*stubCommitteeReader)(nil)

func newBriefSvc(reader internalsvc.GroupWeeklyBriefDataReader, base *model.CommitteeBase) *committeeServicesrvc {
	return &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{base: base, rev: 1},
		weeklyBriefReader:           reader,
	}
}

func TestGetCurrentWeeklyBrief_MissReturns200WithNullBrief(t *testing.T) {
	// reader returns no brief and no throttle — handler must return a non-nil
	// envelope with nil Brief and nil Throttle (BFF expects 200/null, NOT 404).
	svc := newBriefSvc(&stubGroupWeeklyBriefReader{}, &model.CommitteeBase{})

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Nil(t, res.Brief)
	assert.Nil(t, res.Throttle)
}

func TestGetCurrentWeeklyBrief_Hit(t *testing.T) {
	now := time.Now().UTC()
	start, end := model.WeeklyWindow(now)
	brief := &model.GroupWeeklyBrief{
		UID:          "b-1",
		CommitteeUID: "c-1",
		WindowStart:  start,
		WindowEnd:    end,
		State:        model.GroupWeeklyBriefStateGenerated,
		BriefText:    "hello",
	}
	reader := &stubGroupWeeklyBriefReader{brief: brief}
	svc := newBriefSvc(reader, &model.CommitteeBase{})

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.Brief)
	assert.Equal(t, "b-1", *res.Brief.UID)
	assert.Equal(t, "generated", *res.Brief.State)
	assert.Equal(t, "hello", *res.Brief.BriefText)
	// The window/committee fields the stub sets must round-trip into the response.
	assert.Equal(t, "c-1", *res.Brief.CommitteeUID)
	assert.Equal(t, start.UTC().Format(time.RFC3339Nano), *res.Brief.WindowStart)
	assert.Equal(t, end.UTC().Format(time.RFC3339Nano), *res.Brief.WindowEnd)
}

func TestGetCurrentWeeklyBrief_HitWithThrottleAndSourceRefs(t *testing.T) {
	now := time.Now().UTC()
	start, end := model.WeeklyWindow(now)
	brief := &model.GroupWeeklyBrief{
		UID:          "b-1",
		CommitteeUID: "c-1",
		WindowStart:  start,
		WindowEnd:    end,
		State:        model.GroupWeeklyBriefStateGenerated,
		BriefText:    "hello",
		SourceRefs: []model.SourceRef{
			{Kind: "meeting", ID: "m-1", Title: "Sync", Excerpt: "notes"},
		},
	}
	throttleBytes, _ := json.Marshal(model.GroupWeeklyBriefThrottle{
		CommitteeUID: "c-1", WindowStart: start, GeneratesUsed: 2, RegenerationsUsed: 1,
	})
	svc := newBriefSvc(&stubGroupWeeklyBriefReader{brief: brief, throttle: throttleBytes}, &model.CommitteeBase{})

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.NoError(t, err)
	require.NotNil(t, res.Brief)
	// source_refs map through to the response.
	require.Len(t, res.Brief.SourceRefs, 1)
	assert.Equal(t, "meeting", *res.Brief.SourceRefs[0].Kind)
	assert.Equal(t, "m-1", *res.Brief.SourceRefs[0].ID)
	assert.Equal(t, "notes", *res.Brief.SourceRefs[0].Excerpt)
	// throttle decodes and maps.
	require.NotNil(t, res.Throttle)
	assert.Equal(t, 2, *res.Throttle.GeneratesUsed)
	assert.Equal(t, 1, *res.Throttle.RegenerationsUsed)
}

func TestGetCurrentWeeklyBrief_MalformedThrottleIgnored(t *testing.T) {
	now := time.Now().UTC()
	start, end := model.WeeklyWindow(now)
	brief := &model.GroupWeeklyBrief{
		UID: "b-1", CommitteeUID: "c-1", WindowStart: start, WindowEnd: end,
		State: model.GroupWeeklyBriefStateGenerated,
	}
	// Malformed throttle bytes must not fail the read — the brief is still
	// returned and the throttle is simply dropped.
	svc := newBriefSvc(&stubGroupWeeklyBriefReader{brief: brief, throttle: []byte("{not-json")}, &model.CommitteeBase{})

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.NoError(t, err)
	require.NotNil(t, res.Brief)
	assert.Nil(t, res.Throttle)
}

func TestGetCurrentWeeklyBrief_ReaderError(t *testing.T) {
	// An infrastructure failure from the reader (e.g. NATS down) surfaces as 5xx.
	svc := newBriefSvc(&stubGroupWeeklyBriefReader{err: errors.NewUnexpected("nats unavailable", nil)}, &model.CommitteeBase{})

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)
	var ise *committeeservice.InternalServerError
	require.ErrorAs(t, err, &ise)
}

func TestGetCurrentWeeklyBrief_GetBaseServiceUnavailable(t *testing.T) {
	// A non-NotFound error from the committee existence check propagates.
	svc := &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{err: errors.NewServiceUnavailable("nats unavailable")},
		weeklyBriefReader:           &stubGroupWeeklyBriefReader{},
	}
	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)
	var su *committeeservice.ServiceUnavailableError
	require.ErrorAs(t, err, &su)
}

func TestGetCurrentWeeklyBrief_ReaderNotConfigured(t *testing.T) {
	// A nil reader is a misconfiguration → 503.
	svc := &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{base: &model.CommitteeBase{}, rev: 1},
		weeklyBriefReader:           nil,
	}
	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)
	var su *committeeservice.ServiceUnavailableError
	require.ErrorAs(t, err, &su)
}

func TestGetCurrentWeeklyBrief_CommitteeNotFound(t *testing.T) {
	svc := &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{err: errors.NewNotFound("committee not found")},
		weeklyBriefReader:           &stubGroupWeeklyBriefReader{},
	}

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "missing"})
	require.Error(t, err)
	assert.Nil(t, res)
	var nf *committeeservice.NotFoundError
	require.ErrorAs(t, err, &nf)
}

// ─────────────────────────────────────────────────────────────────────────────
//  POST /committees/{uid}/weekly-briefs/generate
// ─────────────────────────────────────────────────────────────────────────────

// stubGroupWeeklyBriefGenerator records the Claim input and returns canned
// output/err. Fulfill panics — the handler never calls it directly.
type stubGroupWeeklyBriefGenerator struct {
	out     *internalsvc.GroupWeeklyBriefGenerateOutput
	err     error
	gotIn   internalsvc.GroupWeeklyBriefGenerateInput
	claimed bool
}

func (g *stubGroupWeeklyBriefGenerator) Claim(_ context.Context, in internalsvc.GroupWeeklyBriefGenerateInput) (*internalsvc.GroupWeeklyBriefGenerateOutput, error) {
	g.claimed = true
	g.gotIn = in
	return g.out, g.err
}

func (g *stubGroupWeeklyBriefGenerator) Fulfill(_ context.Context, _ internalsvc.GroupWeeklyBriefGenerateInput) error {
	panic("Fulfill is not called from the handler under test")
}

var _ internalsvc.GroupWeeklyBriefGenerator = (*stubGroupWeeklyBriefGenerator)(nil)

// stubPublisher records the most recent Event call and returns a canned error.
// Indexer/Access panic — the generate handler only calls Event.
type stubPublisher struct {
	eventErr     error
	gotSubject   string
	gotEvent     any
	gotEventSync bool
	called       bool
}

func (p *stubPublisher) Indexer(_ context.Context, _ string, _ any, _ bool) error {
	panic("Indexer is not called from the handler under test")
}
func (p *stubPublisher) Access(_ context.Context, _ string, _ any, _ bool) error {
	panic("Access is not called from the handler under test")
}
func (p *stubPublisher) Event(_ context.Context, subject string, event any, sync bool) error {
	p.called = true
	p.gotSubject = subject
	p.gotEvent = event
	p.gotEventSync = sync
	return p.eventErr
}

var _ port.CommitteePublisher = (*stubPublisher)(nil)

func newGenerateSvc(base *model.CommitteeBase, gen internalsvc.GroupWeeklyBriefGenerator, pub port.CommitteePublisher) *committeeServicesrvc {
	return &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{base: base, rev: 1},
		weeklyBriefGenerator:        gen,
		publisher:                   pub,
	}
}

func TestGenerateWeeklyBrief_Success(t *testing.T) {
	now := time.Now().UTC()
	start, end := model.WeeklyWindow(now)
	brief := &model.GroupWeeklyBrief{
		UID: "b-1", CommitteeUID: "c-1", WindowStart: start, WindowEnd: end,
		State: model.GroupWeeklyBriefStateGenerating,
	}
	gen := &stubGroupWeeklyBriefGenerator{out: &internalsvc.GroupWeeklyBriefGenerateOutput{Brief: brief}}
	pub := &stubPublisher{}
	base := &model.CommitteeBase{Name: "TAC", ProjectName: "Project X"}
	svc := newGenerateSvc(base, gen, pub)

	res, err := svc.GenerateWeeklyBrief(context.Background(), &committeeservice.GenerateWeeklyBriefPayload{UID: "c-1", Force: true})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.Brief)
	assert.Equal(t, "b-1", *res.Brief.UID)
	assert.Equal(t, "generating", *res.Brief.State)

	// Claim got CommitteeName + ProjectName from the base, plus Force.
	require.True(t, gen.claimed, "expected Claim to be invoked")
	assert.Equal(t, "c-1", gen.gotIn.CommitteeUID)
	assert.Equal(t, "TAC", gen.gotIn.CommitteeName)
	assert.Equal(t, "Project X", gen.gotIn.ProjectName)
	assert.True(t, gen.gotIn.Force)

	// Publisher got the generate-requested event with the same identity fields.
	require.True(t, pub.called, "expected publisher.Event to be invoked")
	assert.Equal(t, constants.GenerateWeeklyBriefRequestedSubject, pub.gotSubject)
	assert.False(t, pub.gotEventSync, "generate-requested event must be enqueued async (sync=false)")
	event, ok := pub.gotEvent.(internalsvc.GenerateWeeklyBriefRequestedEvent)
	require.True(t, ok, "expected event to be GenerateWeeklyBriefRequestedEvent, got %T", pub.gotEvent)
	assert.Equal(t, "c-1", event.CommitteeUID)
	assert.Equal(t, "TAC", event.CommitteeName)
	assert.Equal(t, "Project X", event.ProjectName)
	assert.True(t, event.Force)
	// Claim and event share the same "now" so the async phase resolves the same window.
	assert.Equal(t, gen.gotIn.Now, event.RequestedAt)
}

func TestGenerateWeeklyBrief_CommitteeNotFound(t *testing.T) {
	// stubCommitteeReader with base=nil → handler returns 404.
	svc := newGenerateSvc(nil, &stubGroupWeeklyBriefGenerator{}, &stubPublisher{})

	res, err := svc.GenerateWeeklyBrief(context.Background(), &committeeservice.GenerateWeeklyBriefPayload{UID: "missing"})
	require.Error(t, err)
	assert.Nil(t, res)
	var nf *committeeservice.NotFoundError
	require.ErrorAs(t, err, &nf)
}

func TestGenerateWeeklyBrief_GetBaseError(t *testing.T) {
	// Non-NotFound error from the committee existence check propagates as 503.
	svc := &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{err: errors.NewServiceUnavailable("nats unavailable")},
		weeklyBriefGenerator:        &stubGroupWeeklyBriefGenerator{},
		publisher:                   &stubPublisher{},
	}
	res, err := svc.GenerateWeeklyBrief(context.Background(), &committeeservice.GenerateWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)
	var su *committeeservice.ServiceUnavailableError
	require.ErrorAs(t, err, &su)
}

func TestGenerateWeeklyBrief_GeneratorNotConfigured(t *testing.T) {
	// A nil generator is a misconfiguration → 503.
	svc := newGenerateSvc(&model.CommitteeBase{}, nil, &stubPublisher{})
	res, err := svc.GenerateWeeklyBrief(context.Background(), &committeeservice.GenerateWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)
	var su *committeeservice.ServiceUnavailableError
	require.ErrorAs(t, err, &su)
}

func TestGenerateWeeklyBrief_PublisherNotConfigured(t *testing.T) {
	// A nil publisher would leave a claimed brief stuck "generating" — fail
	// fast before claiming with 503.
	svc := newGenerateSvc(&model.CommitteeBase{}, &stubGroupWeeklyBriefGenerator{}, nil)
	res, err := svc.GenerateWeeklyBrief(context.Background(), &committeeservice.GenerateWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)
	var su *committeeservice.ServiceUnavailableError
	require.ErrorAs(t, err, &su)
}

// ─────────────────────────────────────────────────────────────────────────────
//  PUT /committees/{uid}/weekly-briefs/current
// ─────────────────────────────────────────────────────────────────────────────

// stubGroupWeeklyBriefWriter records the Update input and returns canned
// output/err so handler tests can assert wiring and error mapping.
type stubGroupWeeklyBriefWriter struct {
	out    *model.GroupWeeklyBrief
	err    error
	gotIn  internalsvc.GroupWeeklyBriefUpdateInput
	called bool
}

func (w *stubGroupWeeklyBriefWriter) Update(_ context.Context, in internalsvc.GroupWeeklyBriefUpdateInput) (*model.GroupWeeklyBrief, error) {
	w.called = true
	w.gotIn = in
	return w.out, w.err
}

var _ internalsvc.GroupWeeklyBriefDataWriter = (*stubGroupWeeklyBriefWriter)(nil)

func newUpdateSvc(base *model.CommitteeBase, writer internalsvc.GroupWeeklyBriefDataWriter) *committeeServicesrvc {
	return &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{base: base, rev: 1},
		weeklyBriefWriter:           writer,
	}
}

func TestUpdateCurrentWeeklyBrief_Success(t *testing.T) {
	now := time.Now().UTC()
	start, end := model.WeeklyWindow(now)
	edited := &model.GroupWeeklyBrief{
		UID: "b-1", CommitteeUID: "c-1", WindowStart: start, WindowEnd: end,
		State: model.GroupWeeklyBriefStateEdited, BriefText: "edited body",
		LastEditedBy: "alice", Revision: 6,
	}
	writer := &stubGroupWeeklyBriefWriter{out: edited}
	svc := newUpdateSvc(&model.CommitteeBase{Name: "TAC"}, writer)

	// Principal flows from the Heimdall context into last_edited_by.
	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
	res, err := svc.UpdateCurrentWeeklyBrief(ctx, &committeeservice.UpdateCurrentWeeklyBriefPayload{
		UID: "c-1", BriefText: "edited body", Revision: 5,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "edited", *res.State)
	assert.Equal(t, "edited body", *res.BriefText)
	require.NotNil(t, res.Revision)
	assert.Equal(t, uint64(6), *res.Revision)

	// The handler forwarded the payload + principal to the writer.
	require.True(t, writer.called)
	assert.Equal(t, "c-1", writer.gotIn.CommitteeUID)
	assert.Equal(t, "edited body", writer.gotIn.BriefText)
	assert.Equal(t, uint64(5), writer.gotIn.Revision)
	assert.Equal(t, "alice", writer.gotIn.EditedBy)
}

func TestUpdateCurrentWeeklyBrief_CommitteeNotFound(t *testing.T) {
	// base=nil → 404 before the writer is consulted.
	writer := &stubGroupWeeklyBriefWriter{}
	svc := newUpdateSvc(nil, writer)

	res, err := svc.UpdateCurrentWeeklyBrief(context.Background(), &committeeservice.UpdateCurrentWeeklyBriefPayload{
		UID: "missing", BriefText: "x", Revision: 1,
	})
	require.Error(t, err)
	assert.Nil(t, res)
	var nf *committeeservice.NotFoundError
	require.ErrorAs(t, err, &nf)
	assert.False(t, writer.called, "writer must not be called when the committee is missing")
}

func TestUpdateCurrentWeeklyBrief_RevisionConflict(t *testing.T) {
	// A stale revision maps to the 409 revision-conflict body carrying the current revision.
	writer := &stubGroupWeeklyBriefWriter{err: errors.NewRevisionMismatch(8)}
	svc := newUpdateSvc(&model.CommitteeBase{}, writer)

	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
	res, err := svc.UpdateCurrentWeeklyBrief(ctx, &committeeservice.UpdateCurrentWeeklyBriefPayload{
		UID: "c-1", BriefText: "x", Revision: 7,
	})
	require.Error(t, err)
	assert.Nil(t, res)
	var rc *committeeservice.GroupWeeklyBriefRevisionConflictError
	require.ErrorAs(t, err, &rc)
	assert.Equal(t, "revision_conflict", rc.Code)
	assert.Equal(t, uint64(8), rc.Revision)
}

func TestUpdateCurrentWeeklyBrief_BriefNotFound(t *testing.T) {
	// No brief for the window → 404.
	writer := &stubGroupWeeklyBriefWriter{err: errors.NewNotFound("no weekly brief exists for the current window")}
	svc := newUpdateSvc(&model.CommitteeBase{}, writer)

	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
	res, err := svc.UpdateCurrentWeeklyBrief(ctx, &committeeservice.UpdateCurrentWeeklyBriefPayload{
		UID: "c-1", BriefText: "x", Revision: 1,
	})
	require.Error(t, err)
	assert.Nil(t, res)
	var nf *committeeservice.NotFoundError
	require.ErrorAs(t, err, &nf)
}

func TestUpdateCurrentWeeklyBrief_EmptyBriefTextBadRequest(t *testing.T) {
	// Empty brief_text → 400 (Validation → BadRequest).
	writer := &stubGroupWeeklyBriefWriter{err: errors.NewValidation("brief_text is required")}
	svc := newUpdateSvc(&model.CommitteeBase{}, writer)

	ctx := context.WithValue(context.Background(), constants.PrincipalContextID, "alice")
	res, err := svc.UpdateCurrentWeeklyBrief(ctx, &committeeservice.UpdateCurrentWeeklyBriefPayload{
		UID: "c-1", BriefText: "", Revision: 1,
	})
	require.Error(t, err)
	assert.Nil(t, res)
	var br *committeeservice.BadRequestError
	require.ErrorAs(t, err, &br)
}

func TestUpdateCurrentWeeklyBrief_MissingPrincipalBadRequest(t *testing.T) {
	// No principal in context (e.g. a misconfigured edge) must not persist an
	// empty last_edited_by — the handler rejects it with 400 before calling the
	// writer, matching the sibling write handlers (CreateCommitteeLink, etc.).
	writer := &stubGroupWeeklyBriefWriter{}
	svc := newUpdateSvc(&model.CommitteeBase{}, writer)

	res, err := svc.UpdateCurrentWeeklyBrief(context.Background(), &committeeservice.UpdateCurrentWeeklyBriefPayload{
		UID: "c-1", BriefText: "x", Revision: 1,
	})
	require.Error(t, err)
	assert.Nil(t, res)
	var br *committeeservice.BadRequestError
	require.ErrorAs(t, err, &br)
	assert.False(t, writer.called, "writer must not be called when the principal is missing")
}

func TestUpdateCurrentWeeklyBrief_WriterNotConfigured(t *testing.T) {
	// A nil writer is a misconfiguration → 503.
	svc := &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{base: &model.CommitteeBase{}, rev: 1},
		weeklyBriefWriter:           nil,
	}
	res, err := svc.UpdateCurrentWeeklyBrief(context.Background(), &committeeservice.UpdateCurrentWeeklyBriefPayload{
		UID: "c-1", BriefText: "x", Revision: 1,
	})
	require.Error(t, err)
	assert.Nil(t, res)
	var su *committeeservice.ServiceUnavailableError
	require.ErrorAs(t, err, &su)
}

func TestGenerateWeeklyBrief_ClaimError(t *testing.T) {
	// Claim errors propagate through wrapError; here a ServiceUnavailable from
	// the generator surfaces as a 503 to the caller.
	gen := &stubGroupWeeklyBriefGenerator{err: errors.NewServiceUnavailable("throttle bucket missing")}
	pub := &stubPublisher{}
	svc := newGenerateSvc(&model.CommitteeBase{Name: "TAC"}, gen, pub)
	res, err := svc.GenerateWeeklyBrief(context.Background(), &committeeservice.GenerateWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)
	var su *committeeservice.ServiceUnavailableError
	require.ErrorAs(t, err, &su)
	// No event must be published when claim fails.
	assert.False(t, pub.called, "publisher.Event must not be called when Claim fails")
}

func TestGenerateWeeklyBrief_PublishError(t *testing.T) {
	// A publish failure leaves the brief "generating" with nothing to advance
	// it → surface 503 so the caller retries.
	now := time.Now().UTC()
	start, end := model.WeeklyWindow(now)
	brief := &model.GroupWeeklyBrief{
		UID: "b-1", CommitteeUID: "c-1", WindowStart: start, WindowEnd: end,
		State: model.GroupWeeklyBriefStateGenerating,
	}
	gen := &stubGroupWeeklyBriefGenerator{out: &internalsvc.GroupWeeklyBriefGenerateOutput{Brief: brief}}
	pub := &stubPublisher{eventErr: errors.NewUnexpected("nats publish failed", nil)}
	svc := newGenerateSvc(&model.CommitteeBase{Name: "TAC"}, gen, pub)

	res, err := svc.GenerateWeeklyBrief(context.Background(), &committeeservice.GenerateWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)
	var su *committeeservice.ServiceUnavailableError
	require.ErrorAs(t, err, &su)
}
