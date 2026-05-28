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
	internalsvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
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

func (r *stubCommitteeReader) ListMembers(_ context.Context, _ string) ([]*model.CommitteeMember, error) {
	panic("not used")
}

func (r *stubCommitteeReader) ListAllUIDs(_ context.Context) ([]string, error) { panic("not used") }

func (r *stubCommitteeReader) GetBaseAttributeValue(_ context.Context, _, _ string) (any, error) {
	panic("not used")
}

func (r *stubCommitteeReader) GetSettingsUIDByInviteUID(_ context.Context, _ string) (string, error) {
	panic("not used")
}

func (r *stubCommitteeReader) GetMemberRevision(_ context.Context, _ string) (uint64, error) {
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
