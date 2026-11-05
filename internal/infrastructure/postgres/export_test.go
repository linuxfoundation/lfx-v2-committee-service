// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// withRevisions makes the next write leave the revision on its row.
func withRevisions(t *testing.T, revision uint64) context.Context {
	t.Helper()
	ctx, err := WithRevision(context.Background(), revision)
	require.NoError(t, err)
	return ctx
}

func TestExport_StreamsEveryRowWithItsRevision(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	committee := storetest.NewCommittee("c-1", "project-1", "Committee One")
	base, err := WithRevision(ctx, 10)
	require.NoError(t, err)
	base, err = WithSettingsRevision(base, 11)
	require.NoError(t, err)
	require.NoError(t, store.Create(base, committee))
	require.NoError(t, store.CreateMember(withRevisions(t, 20), storetest.NewMember("m-1", "c-1", "first.last@example.com")))
	require.NoError(t, store.CreateInvite(withRevisions(t, 30), storetest.NewInvite("i-1", "c-1", "first.last@example.com")))
	require.NoError(t, store.CreateApplication(withRevisions(t, 40), storetest.NewApplication("a-1", "c-1", "first.last@example.com")))
	require.NoError(t, store.CreateLink(withRevisions(t, 50), storetest.NewLink("l-1", "c-1")))
	require.NoError(t, store.CreateLinkFolder(withRevisions(t, 60), storetest.NewFolder("f-1", "c-1", "Charters")))
	require.NoError(t, store.CreateDocumentMetadata(withRevisions(t, 70), storetest.NewDocument("d-1", "c-1", "Minutes")))
	_, err = store.PutGroupWeeklyBrief(withRevisions(t, 80), testBrief("b-1", "c-1", testWindow))
	require.NoError(t, err)
	_, err = store.PutGroupWeeklyBriefThrottle(withRevisions(t, 90), testThrottle("c-1", testWindow))
	require.NoError(t, err)

	got := map[string]map[string]uint64{}
	collect := func(entity string) func(uid string, revision uint64) {
		got[entity] = map[string]uint64{}
		return func(uid string, revision uint64) { got[entity][uid] = revision }
	}
	committees, settings, members := collect("committees"), collect("settings"), collect("members")
	invites, applications, links := collect("invites"), collect("applications"), collect("links")
	folders, documents, briefs, throttles := collect("folders"), collect("documents"), collect("briefs"), collect("throttles")

	require.NoError(t, store.EachCommitteeExport(ctx, func(c *model.CommitteeBase, r uint64) error { committees(c.UID, r); return nil }))
	require.NoError(t, store.EachSettingsExport(ctx, func(s *model.CommitteeSettings, r uint64) error { settings(s.UID, r); return nil }))
	require.NoError(t, store.EachMemberExport(ctx, func(m *model.CommitteeMember, r uint64) error { members(m.UID, r); return nil }))
	require.NoError(t, store.EachInviteExport(ctx, func(i *model.CommitteeInvite, r uint64) error { invites(i.UID, r); return nil }))
	require.NoError(t, store.EachApplicationExport(ctx, func(a *model.CommitteeApplication, r uint64) error { applications(a.UID, r); return nil }))
	require.NoError(t, store.EachLinkExport(ctx, func(l *model.CommitteeLink, r uint64) error { links(l.UID, r); return nil }))
	require.NoError(t, store.EachFolderExport(ctx, func(f *model.CommitteeLinkFolder, r uint64) error { folders(f.UID, r); return nil }))
	require.NoError(t, store.EachDocumentExport(ctx, func(d *model.CommitteeDocument, r uint64) error { documents(d.UID, r); return nil }))
	require.NoError(t, store.EachBriefExport(ctx, func(b *model.GroupWeeklyBrief, r uint64) error {
		assert.Equal(t, r, b.Revision, "the brief carries its revision")
		briefs(b.UID, r)
		return nil
	}))
	require.NoError(t, store.EachThrottleExport(ctx, func(th *model.GroupWeeklyBriefThrottle, r uint64) error {
		assert.Equal(t, r, th.Revision, "the throttle entry carries its revision")
		throttles(th.CommitteeUID, r)
		return nil
	}))

	assert.Equal(t, map[string]map[string]uint64{
		"committees":   {"c-1": 10},
		"settings":     {"c-1": 11},
		"members":      {"m-1": 20},
		"invites":      {"i-1": 30},
		"applications": {"a-1": 40},
		"links":        {"l-1": 50},
		"folders":      {"f-1": 60},
		"documents":    {"d-1": 70},
		"briefs":       {"b-1": 80},
		"throttles":    {"c-1": 90},
	}, got)
}

func TestExport_AnEmptyTableStreamsNothingAndTheErrorFnReturnsStopsTheStream(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	called := false
	require.NoError(t, store.EachMemberExport(ctx, func(*model.CommitteeMember, uint64) error { called = true; return nil }))
	assert.False(t, called)

	require.NoError(t, store.CreateMember(withRevisions(t, 20), storetest.NewMember("m-1", "c-1", "first.last@example.com")))
	require.NoError(t, store.CreateMember(withRevisions(t, 21), storetest.NewMember("m-2", "c-1", "first.last2@example.com")))
	stop := errors.New("stop")
	calls := 0
	err := store.EachMemberExport(ctx, func(*model.CommitteeMember, uint64) error { calls++; return stop })
	assert.ErrorIs(t, err, stop)
	assert.Equal(t, 1, calls)
}

func TestExport_AFailedQueryIsAnError(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.Error(t, store.EachInviteExport(ctx, func(*model.CommitteeInvite, uint64) error { return nil }))
}
