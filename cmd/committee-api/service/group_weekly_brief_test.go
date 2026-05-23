// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	internalsvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// denyAccessChecker is a CommitteeAccessChecker that always denies — used to
// assert that the handler returns 403 when authz fails.
type denyAccessChecker struct{}

func (denyAccessChecker) CanWriteCommittee(_ context.Context, _ string) error {
	return errors.NewForbidden("caller lacks writer access on committee")
}

// allowAccessChecker permits every request — used for non-authz test paths.
type allowAccessChecker struct{}

func (allowAccessChecker) CanWriteCommittee(_ context.Context, _ string) error { return nil }

// stubGroupWeeklyBriefReader returns canned hits/misses.
type stubGroupWeeklyBriefReader struct {
	brief    *model.GroupWeeklyBrief
	throttle []byte
}

func (s *stubGroupWeeklyBriefReader) GetCurrent(_ context.Context, _ string, _ time.Time) (*model.GroupWeeklyBrief, []byte, error) {
	return s.brief, s.throttle, nil
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

func newBriefSvc(reader internalsvc.GroupWeeklyBriefDataReader, access port.CommitteeAccessChecker, base *model.CommitteeBase) *committeeServicesrvc {
	return &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{base: base, rev: 1},
		accessChecker:               access,
		weeklyBriefReader:           reader,
	}
}

func TestGetCurrentWeeklyBrief_Forbidden(t *testing.T) {
	svc := newBriefSvc(&stubGroupWeeklyBriefReader{}, denyAccessChecker{}, &model.CommitteeBase{})

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.Error(t, err)
	assert.Nil(t, res)

	var fb *committeeservice.ForbiddenError
	require.ErrorAs(t, err, &fb, "expected Forbidden error type")
	assert.Contains(t, fb.Message, "writer access")
}

func TestGetCurrentWeeklyBrief_MissReturns200WithNullBrief(t *testing.T) {
	// reader returns no brief and no throttle — handler must return a non-nil
	// envelope with nil Brief and nil Throttle (BFF expects 200/null, NOT 404).
	svc := newBriefSvc(&stubGroupWeeklyBriefReader{}, allowAccessChecker{}, &model.CommitteeBase{})

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
	svc := newBriefSvc(reader, allowAccessChecker{}, &model.CommitteeBase{})

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "c-1"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.Brief)
	assert.Equal(t, "b-1", *res.Brief.UID)
	assert.Equal(t, "generated", *res.Brief.State)
	assert.Equal(t, "hello", *res.Brief.BriefText)
}

func TestGetCurrentWeeklyBrief_CommitteeNotFound(t *testing.T) {
	svc := &committeeServicesrvc{
		committeeReaderOrchestrator: &stubCommitteeReader{err: errors.NewNotFound("committee not found")},
		accessChecker:               allowAccessChecker{},
		weeklyBriefReader:           &stubGroupWeeklyBriefReader{},
	}

	res, err := svc.GetCurrentWeeklyBrief(context.Background(), &committeeservice.GetCurrentWeeklyBriefPayload{UID: "missing"})
	require.Error(t, err)
	assert.Nil(t, res)
	var nf *committeeservice.NotFoundError
	require.ErrorAs(t, err, &nf)
}
