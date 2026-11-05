// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/postgres/postgrestest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// verifyEntities are the lines of a full report, in order.
var verifyEntities = []string{
	entityCommittees, resultSettings, entityMembers, entityInvites, entityApplications, entityLinks, entityFolders,
	entityDocuments, entityBriefs, entityThrottles,
}

func verifyResultOf(t *testing.T, results []verifyResult, entity string) verifyResult {
	t.Helper()
	for _, r := range results {
		if r.Entity == entity {
			return r
		}
	}
	require.Failf(t, "no result", "entity %s", entity)
	return verifyResult{}
}

// migratedSource copies childSource to a PostgreSQL schema of its own and returns both.
func migratedSource(t *testing.T, opts backfillOptions) (*fakeSource, *postgres.Store) {
	t.Helper()
	src := childSource()
	store := postgrestest.NewStore(t)
	_, err := runBackfill(context.Background(), src, store, opts)
	require.NoError(t, err)
	return src, store
}

func TestRunVerify_AfterAMigrateThereAreNoDifferences(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})

	results, err := runVerify(ctx, src, store, verifyOptions{})

	require.NoError(t, err)
	require.Len(t, results, len(verifyEntities))
	for _, entity := range verifyEntities {
		r := verifyResultOf(t, results, entity)
		assert.Zero(t, r.differences(), entity)
		assert.Positive(t, r.Matched, "%s: something was compared", entity)
		assert.Equal(t, r.KV-r.KVOrphans, r.Matched, entity)
		assert.Equal(t, r.Matched, r.PostgreSQL, entity)
		assert.Zero(t, r.PostgreSQLOrphans, entity)
	}
	for _, entity := range append([]string{entityMembers}, childEntities...) {
		r := verifyResultOf(t, results, entity)
		assert.EqualValues(t, 1, r.KVOrphans, "%s: the child of a committee KV no longer has is counted and is not a difference", entity)
	}
}

// mutation changes the KV store of a migrated pair so that one entity differs in one way.
type mutation struct {
	// affected are the report lines the change shows on.
	affected []string
	add      func(*fakeSource) // a record KV has and PostgreSQL has not
	drop     func(*fakeSource) // a record PostgreSQL has and KV no longer has
	change   func(*fakeSource) // a record whose content differs
}

func mutations() map[string]mutation {
	week := storetest.FixtureWindow.AddDate(0, 0, 7)
	return map[string]mutation{
		entityCommittees: {
			affected: []string{entityCommittees, resultSettings},
			add: func(s *fakeSource) {
				s.committees = append(s.committees, kvCommittee("c-3", "Committee Three", 300, 301))
			},
			drop:   func(s *fakeSource) { s.committees = s.committees[1:] },
			change: func(s *fakeSource) { s.committees[0].Base.Description = "changed" },
		},
		resultSettings: {
			affected: []string{resultSettings},
			add: func(s *fakeSource) {
				settings := storetest.NewCommittee("c-2", "project-1", "Committee Two").CommitteeSettings
				settings.UID = "c-2" // KV keys the settings by the committee uid
				s.committees[1].Settings, s.committees[1].SettingsRevision = settings, 400
			},
			drop:   func(s *fakeSource) { s.committees[0].Settings = nil },
			change: func(s *fakeSource) { s.committees[0].Settings.MemberVisibility = "changed" },
		},
		entityMembers: {
			affected: []string{entityMembers},
			add: func(s *fakeSource) {
				s.members = append(s.members, kvMember("m-new", "c-1", "first.last4@example.com", 410))
			},
			drop:   func(s *fakeSource) { s.members = s.members[1:] },
			change: func(s *fakeSource) { s.members[0].record.JobTitle = "changed" },
		},
		entityInvites: {
			affected: []string{entityInvites},
			add: func(s *fakeSource) {
				s.invites = append(s.invites, rec(storetest.NewInvite("i-new", "c-1", "first.last4@example.com"), 420))
			},
			drop:   func(s *fakeSource) { s.invites = s.invites[1:] },
			change: func(s *fakeSource) { s.invites[0].record.Role = "changed" },
		},
		entityApplications: {
			affected: []string{entityApplications},
			add: func(s *fakeSource) {
				s.applications = append(s.applications, rec(storetest.NewApplication("a-new", "c-1", "first.last4@example.com"), 430))
			},
			drop:   func(s *fakeSource) { s.applications = s.applications[1:] },
			change: func(s *fakeSource) { s.applications[0].record.Message = "changed" },
		},
		entityLinks: {
			affected: []string{entityLinks},
			add:      func(s *fakeSource) { s.links = append(s.links, rec(storetest.NewLink("l-new", "c-1"), 440)) },
			drop:     func(s *fakeSource) { s.links = s.links[1:] },
			change:   func(s *fakeSource) { s.links[0].record.Name = "changed" },
		},
		entityFolders: {
			affected: []string{entityFolders},
			add: func(s *fakeSource) {
				s.folders = append(s.folders, rec(storetest.NewFolder("f-new", "c-1", "Other"), 450))
			},
			drop:   func(s *fakeSource) { s.folders = s.folders[1:] },
			change: func(s *fakeSource) { s.folders[0].record.Name = "changed" },
		},
		entityDocuments: {
			affected: []string{entityDocuments},
			add: func(s *fakeSource) {
				s.documents = append(s.documents, rec(storetest.NewDocument("d-new", "c-1", "Other"), 460))
			},
			drop:   func(s *fakeSource) { s.documents = s.documents[1:] },
			change: func(s *fakeSource) { s.documents[0].record.Name = "changed" },
		},
		entityBriefs: {
			affected: []string{entityBriefs},
			add: func(s *fakeSource) {
				s.briefs = append(s.briefs, kvBrief(storetest.NewBrief("b-new", "c-1", week), 470))
			},
			drop:   func(s *fakeSource) { s.briefs = s.briefs[1:] },
			change: func(s *fakeSource) { s.briefs[0].record.BriefText = "changed" },
		},
		entityThrottles: {
			affected: []string{entityThrottles},
			add: func(s *fakeSource) {
				// First, so that a key that ignored the window would collide with the one of the first week.
				s.throttles = append([]childRecord[*model.GroupWeeklyBriefThrottle]{kvThrottle(storetest.NewThrottle("c-1", week), 480)}, s.throttles...)
			},
			drop:   func(s *fakeSource) { s.throttles = s.throttles[1:] },
			change: func(s *fakeSource) { s.throttles[0].record.GeneratesUsed++ },
		},
	}
}

func TestRunVerify_ADifferenceInAnEntityIsFoundAndReportedWithItsUID(t *testing.T) {
	ctx := context.Background()
	for entity, m := range mutations() {
		for name, tc := range map[string]struct {
			mutate func(*fakeSource)
			kind   string
			count  func(verifyResult) int64
		}{
			"a record KV has and PostgreSQL has not": {m.add, verifyMissing, func(r verifyResult) int64 { return r.Missing }},
			"a record PostgreSQL has and KV has not": {m.drop, verifyExtra, func(r verifyResult) int64 { return r.Extra }},
			"a record whose content differs":         {m.change, verifyMismatch, func(r verifyResult) int64 { return r.Mismatched }},
		} {
			t.Run(entity+"/"+name, func(t *testing.T) {
				src, store := migratedSource(t, backfillOptions{})
				tc.mutate(src)

				results, err := runVerify(ctx, src, store, verifyOptions{})

				require.Error(t, err)
				r := verifyResultOf(t, results, entity)
				assert.EqualValues(t, 1, tc.count(r), "the one record that differs is counted")
				assert.EqualValues(t, 1, r.differences())
				var found []verifyProblem
				for _, p := range r.Problems {
					if p.Kind == tc.kind {
						found = append(found, p)
					}
				}
				require.Len(t, found, 1)
				assert.NotEmpty(t, found[0].UID)
				assert.NotContains(t, found[0].Message, "example.com", "a finding names the record by uid, not by the personal data in it")
				for _, other := range verifyEntities {
					if other != entity && !contains(m.affected, other) {
						unaffected := verifyResultOf(t, results, other)
						assert.Zero(t, unaffected.differences(), "%s is not affected", other)
					}
				}
			})
		}
	}
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func TestRunVerify_ADifferentRevisionIsAMismatchEvenWithTheSameContent(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})
	src.invites[0].revision++

	results, err := runVerify(ctx, src, store, verifyOptions{})

	require.Error(t, err)
	r := verifyResultOf(t, results, entityInvites)
	require.EqualValues(t, 1, r.Mismatched)
	assert.Equal(t, verifyProblem{UID: "i-1", Kind: verifyMismatch, Message: "revision differs (KV 41, PostgreSQL 40)"}, r.Problems[0])
}

func TestRunVerify_ARecordThatDiffersInContentAndRevisionSaysBoth(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})
	src.invites[0].record.Role = "changed"
	src.invites[0].revision++

	results, err := runVerify(ctx, src, store, verifyOptions{})

	require.Error(t, err)
	r := verifyResultOf(t, results, entityInvites)
	assert.EqualValues(t, 1, r.Mismatched, "one record, however many ways it differs")
	assert.Equal(t, verifyProblem{UID: "i-1", Kind: verifyMismatch, Message: "content differs; revision differs (KV 41, PostgreSQL 40)"}, r.Problems[0])
}

func TestRunVerify_OrphansAreReportedAndOnlyDifferWhenTheyWereToBeCopied(t *testing.T) {
	ctx := context.Background()

	t.Run("orphans migrate copied are in both stores and match", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{IncludeOrphans: true})

		results, err := runVerify(ctx, src, store, verifyOptions{IncludeOrphans: true})

		require.NoError(t, err)
		for _, entity := range append([]string{entityMembers}, childEntities...) {
			r := verifyResultOf(t, results, entity)
			assert.EqualValues(t, 1, r.KVOrphans, entity)
			assert.EqualValues(t, 1, r.PostgreSQLOrphans, "%s: the row of a committee PostgreSQL has no row for is what would stop a foreign key", entity)
			assert.Zero(t, r.differences(), entity)
		}
		assert.Contains(t, verifyResultOf(t, results, entityMembers).Problems, verifyProblem{UID: "m-orphan", Kind: verifyPGOrphan, Message: "its committee has no row in PostgreSQL"})
	})

	t.Run("orphans migrate skipped are not missing unless they were to be copied", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})

		results, err := runVerify(ctx, src, store, verifyOptions{})
		require.NoError(t, err)
		assert.EqualValues(t, 1, verifyResultOf(t, results, entityMembers).KVOrphans)

		results, err = runVerify(ctx, src, store, verifyOptions{IncludeOrphans: true})
		require.Error(t, err)
		for _, entity := range append([]string{entityMembers}, childEntities...) {
			assert.EqualValues(t, 1, verifyResultOf(t, results, entity).Missing, entity)
		}
	})
}

func TestRunVerify_AScanThatCannotBeCompletedIsAnError(t *testing.T) {
	ctx := context.Background()

	t.Run("committees KV cannot list", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})
		src.listErr = errors.New("nats down")

		results, err := runVerify(ctx, src, store, verifyOptions{})

		require.Error(t, err)
		for _, entity := range verifyEntities {
			r := verifyResultOf(t, results, entity)
			assert.EqualValues(t, 1, r.Errors, entity)
			assert.Zero(t, r.KV+r.PostgreSQL, "%s: nothing is compared without the committees to check against", entity)
		}
	})

	t.Run("committees PostgreSQL cannot list", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})

		results, err := runVerify(ctx, src, noListSource{store}, verifyOptions{})

		require.Error(t, err)
		for _, entity := range verifyEntities {
			r := verifyResultOf(t, results, entity)
			require.EqualValues(t, 1, r.Errors, entity)
			assert.Contains(t, r.Problems[0].Message, "listing the committees in PostgreSQL", entity)
			assert.Zero(t, r.KV+r.PostgreSQL, entity)
		}
	})

	t.Run("a KV scan that ends with an error", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})
		src.memberErr = errors.New("one member could not be read")

		results, err := runVerify(ctx, src, store, verifyOptions{})

		require.Error(t, err)
		r := verifyResultOf(t, results, entityMembers)
		assert.EqualValues(t, 1, r.Errors)
		assert.Equal(t, verifyScan, r.Problems[len(r.Problems)-1].Kind)
		assert.Contains(t, r.Problems[len(r.Problems)-1].Message, "reading KV")
	})

	t.Run("a PostgreSQL scan that fails leaves nothing to say about what is missing", func(t *testing.T) {
		src, store := migratedSource(t, backfillOptions{})

		results, err := runVerify(ctx, src, failingSource{store}, verifyOptions{})

		require.Error(t, err)
		r := verifyResultOf(t, results, entityInvites)
		assert.EqualValues(t, 1, r.Errors)
		assert.Zero(t, r.Missing, "the KV records are not reported missing because PostgreSQL could not be read")
		assert.Zero(t, r.KV)
		members := verifyResultOf(t, results, entityMembers)
		assert.Zero(t, members.differences(), "the other entities are still compared")
	})
}

// noListSource is a store that cannot list its committees.
type noListSource struct{ verifySource }

func (noListSource) ListAllUIDs(context.Context) ([]string, error) {
	return nil, errors.New("connection reset")
}

// failingSource is a store that cannot stream the invites.
type failingSource struct{ verifySource }

func (failingSource) EachInviteExport(context.Context, func(*model.CommitteeInvite, uint64) error) error {
	return errors.New("connection reset")
}

func TestRunVerify_OnlyTheEntitiesAskedFor(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})

	results, err := runVerify(ctx, src, store, verifyOptions{Entities: []string{entityMembers}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, entityMembers, results[0].Entity)

	results, err = runVerify(ctx, src, store, verifyOptions{Entities: []string{entityCommittees}})
	require.NoError(t, err)
	assert.Equal(t, []string{entityCommittees, resultSettings}, []string{results[0].Entity, results[1].Entity}, "the settings come with the committees")

	_, err = runVerify(ctx, src, store, verifyOptions{Entities: []string{"widgets"}})
	require.ErrorContains(t, err, `unknown entity "widgets"`)
}

func TestRunVerify_ChangesNothing(t *testing.T) {
	ctx := context.Background()
	src, store := migratedSource(t, backfillOptions{})
	before := pgRevisions(ctx, t, store)
	src.invites[0].record.Role = "changed"

	_, err := runVerify(ctx, src, store, verifyOptions{})

	require.Error(t, err)
	assert.Equal(t, before, pgRevisions(ctx, t, store))
	invite, _, err := store.GetInvite(ctx, "i-1")
	require.NoError(t, err)
	assert.NotEqual(t, "changed", invite.Role, "a difference is reported and not repaired")
}

func TestDigestOf(t *testing.T) {
	at := time.Date(2026, 1, 4, 10, 30, 0, 123456789, time.UTC)
	base := func() *model.CommitteeLink {
		l := storetest.NewLink("l-1", "c-1")
		l.CreatedAt, l.UpdatedAt = at, at
		return l
	}
	digest := func(l *model.CommitteeLink) [32]byte {
		d, err := digestOf(l)
		require.NoError(t, err)
		return d
	}

	t.Run("what a store changes in holding a record does not matter", func(t *testing.T) {
		other := base()
		other.CreatedAt = at.Truncate(time.Microsecond).In(time.FixedZone("UTC+2", 2*60*60))
		other.UpdatedAt = other.CreatedAt
		assert.Equal(t, digest(base()), digest(other), "the zone and anything under the microsecond")
	})

	t.Run("times inside lists and objects are normalized too", func(t *testing.T) {
		digestOfValue := func(v any) [32]byte {
			d, err := digestOf(v)
			require.NoError(t, err)
			return d
		}
		zone := time.FixedZone("UTC+2", 2*60*60)
		nested := func(at time.Time) map[string]any {
			return map[string]any{"sources": []any{map[string]any{"synced_at": at}}, "charter": map[string]any{"updated_at": at}}
		}
		assert.Equal(t, digestOfValue(nested(at.Truncate(time.Microsecond))), digestOfValue(nested(at.Truncate(time.Microsecond).In(zone))))
		assert.NotEqual(t, digestOfValue(nested(at)), digestOfValue(nested(at.Add(time.Second))))
	})

	t.Run("an empty list and a missing one are different, as the store keeps them", func(t *testing.T) {
		digestMap := func(m map[string]any) [32]byte {
			d, err := digestOf(m)
			require.NoError(t, err)
			return d
		}
		none := digestMap(map[string]any{"name": "a", "writers": nil})
		assert.NotEqual(t, none, digestMap(map[string]any{"name": "a", "writers": []string{}}), "not set and set to empty")
		assert.NotEqual(t, none, digestMap(map[string]any{"name": "a", "writers": []string{"first-last"}}))
		assert.Equal(t, none, digestMap(map[string]any{"writers": nil, "name": "a"}), "the order of the keys does not matter")
	})

	t.Run("a change of content does", func(t *testing.T) {
		renamed := base()
		renamed.Name = "changed"
		assert.NotEqual(t, digest(base()), digest(renamed))
		later := base()
		later.UpdatedAt = at.Add(time.Second)
		assert.NotEqual(t, digest(base()), digest(later), "a time a microsecond or more apart")
		moved := base()
		folder := "f-2"
		moved.FolderUID = &folder
		assert.NotEqual(t, digest(base()), digest(moved))
	})
}
