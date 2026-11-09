// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bulkTestMemberWriter is a concurrency-safe member store for bulk tests. It counts writes, tracks the
// peak number of concurrent writes, and records created members in the repository so a later
// ListMembersByCommittee sees them, as storage would.
type bulkTestMemberWriter struct {
	*TestMockCommitteeMemberWriter
	repo *mock.MockRepository

	mu          sync.Mutex
	writes      atomic.Int32
	inFlight    atomic.Int32
	maxInFlight atomic.Int32

	errAll error            // returned for every write when set
	errFor map[string]error // normalized email → error returned for that write
	delay  time.Duration    // how long each write takes
}

func (w *bulkTestMemberWriter) CreateMember(ctx context.Context, member *model.CommitteeMember) error {
	current := w.inFlight.Add(1)
	defer w.inFlight.Add(-1)
	for {
		peak := w.maxInFlight.Load()
		if current <= peak || w.maxInFlight.CompareAndSwap(peak, current) {
			break
		}
	}
	w.writes.Add(1)
	if w.delay > 0 {
		time.Sleep(w.delay)
	}
	if w.errAll != nil {
		return w.errAll
	}
	if err := w.errFor[normalizeBulkMemberEmail(member.Email)]; err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.TestMockCommitteeMemberWriter.CreateMember(ctx, member); err != nil {
		return err
	}
	w.repo.AddCommitteeMember(member.CommitteeUID, member)
	return nil
}

// bulkTestUserReader is a concurrency-safe UserReader: emails in usernames resolve, others are a
// confirmed absence, and err fails every lookup.
type bulkTestUserReader struct {
	calls     atomic.Int32
	usernames map[string]string
	err       error
}

func (r *bulkTestUserReader) UsernameByEmail(_ context.Context, email string) (string, error) {
	r.calls.Add(1)
	if r.err != nil {
		return "", r.err
	}
	if username, ok := r.usernames[email]; ok {
		return username, nil
	}
	return "", errs.NewNotFound("user not found")
}

func (r *bulkTestUserReader) UserMetadataByPrincipal(context.Context, string) (*model.UserMetadata, error) {
	return nil, nil
}

func (r *bulkTestUserReader) EmailsByAuthToken(context.Context, string) (*model.UserEmails, error) {
	return nil, nil
}

// listMembersFailingReader fails the existing-member listing and reads everything else from the repository.
type listMembersFailingReader struct{ port.CommitteeReader }

func (listMembersFailingReader) ListMembersByCommittee(context.Context, string) ([]*model.CommitteeMember, error) {
	return nil, errs.NewServiceUnavailable("listing members failed")
}

// bulkFixture wires an orchestrator to bulk test doubles. The mock repository is a process-wide
// singleton, so each fixture uses its own committee UID.
type bulkFixture struct {
	committeeUID string
	orchestrator *committeeWriterOrchestrator
	repo         *mock.MockRepository
	writer       *bulkTestMemberWriter
	reader       *bulkTestUserReader
	publisher    *mock.MockCommitteePublisher
	enriched     atomic.Int32 // calls to the enrich callback, when the case sets enrich
}

func newBulkFixture() *bulkFixture {
	orchestrator, repo, base := setupMemberWriterTest()
	f := &bulkFixture{
		committeeUID: "committee-bulk-" + uuid.NewString(),
		orchestrator: orchestrator,
		repo:         repo,
		writer:       &bulkTestMemberWriter{TestMockCommitteeMemberWriter: base, repo: repo, errFor: map[string]error{}},
		reader:       &bulkTestUserReader{usernames: map[string]string{}},
		publisher:    &mock.MockCommitteePublisher{},
	}
	orchestrator.committeeWriter = f.writer
	orchestrator.userReader = f.reader
	orchestrator.committeePublisher = f.publisher
	addMemberWriterTestCommittee(repo, f.committeeUID)
	return f
}

func bulkTestEmail(n int) string {
	return fmt.Sprintf("first.last+%d@example.com", n)
}

func bulkTestMember(n int) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		Email:     bulkTestEmail(n),
		FirstName: "First",
		LastName:  "Last",
	}}
}

func bulkTestMembers(n int) []*model.CommitteeMember {
	members := make([]*model.CommitteeMember, n)
	for i := range members {
		members[i] = bulkTestMember(i + 1)
	}
	return members
}

func countBulkResults(results []*model.BulkMemberResult) map[model.BulkMemberStatus]int {
	counts := map[model.BulkMemberStatus]int{}
	for _, r := range results {
		counts[r.Status]++
	}
	return counts
}

func TestCommitteeWriterOrchestrator_CreateMembers(t *testing.T) {
	tests := []struct {
		name  string
		rows  func() []*model.CommitteeMember
		setup func(f *bulkFixture)
		opts  BulkMemberCreateOptions
		// enrich installs an Enrich callback that counts calls in the fixture.
		enrich bool
		check  func(t *testing.T, f *bulkFixture, rows []*model.CommitteeMember, results []*model.BulkMemberResult, err error)
	}{
		{
			name: "adds every row once, resolving found and absent identities",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(3) },
			setup: func(f *bulkFixture) {
				f.reader.usernames[bulkTestEmail(1)] = "first-last"
			},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.EqualValues(t, 3, f.writer.writes.Load())
				for _, r := range results {
					assert.Equal(t, model.BulkMemberAdded, r.Status)
					assert.Empty(t, r.Reason)
					require.NotNil(t, r.Member)
					assert.Equal(t, r.Member.UID, r.MemberUID)
					assert.Equal(t, f.committeeUID, r.Member.CommitteeUID)
				}
				assert.Equal(t, "first-last", results[0].Member.Username, "a found identity stores its LFID")
				assert.Empty(t, results[1].Member.Username, "a confirmed absence stores an email-only member")
				assert.Equal(t, 1, f.publisher.MemberPutCallCount, "only the member with an LFID gets an access tuple")
			},
		},
		{
			name: "skips an existing member with its uid and makes no lookup or write for it",
			rows: func() []*model.CommitteeMember {
				rows := bulkTestMembers(2)
				rows[0].Email = "  FIRST.LAST+1@example.com "
				return rows
			},
			setup: func(f *bulkFixture) {
				f.repo.AddCommitteeMember(f.committeeUID, &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
					UID: "existing-member", CommitteeUID: f.committeeUID, Email: bulkTestEmail(1),
				}})
			},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, model.BulkMemberSkipped, results[0].Status)
				assert.Equal(t, model.BulkReasonAlreadyMember, results[0].Reason)
				assert.Equal(t, "existing-member", results[0].MemberUID)
				assert.False(t, results[0].Retryable)
				assert.Nil(t, results[0].Member)
				assert.Equal(t, model.BulkMemberAdded, results[1].Status)
				assert.EqualValues(t, 1, f.reader.calls.Load())
				assert.EqualValues(t, 1, f.writer.writes.Load())
			},
		},
		{
			name: "skips a later row repeating an earlier email and enriches only rows it creates",
			rows: func() []*model.CommitteeMember {
				rows := bulkTestMembers(3)
				rows[1].Email = " First.Last+1@Example.com"
				return rows
			},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, model.BulkMemberAdded, results[0].Status)
				assert.Equal(t, model.BulkMemberSkipped, results[1].Status)
				assert.Equal(t, model.BulkReasonDuplicateInRequest, results[1].Reason)
				assert.Equal(t, model.BulkMemberAdded, results[2].Status)
				assert.EqualValues(t, 2, f.writer.writes.Load())
			},
		},
		{
			name: "fails a voting None row as invalid on a voting committee and still adds the others",
			rows: func() []*model.CommitteeMember {
				rows := bulkTestMembers(3)
				for _, row := range rows {
					row.Organization = model.CommitteeMemberOrganization{Name: "Example", Website: "https://example.com"}
					row.Voting.Status = "Voting Rep"
				}
				rows[1].Voting.Status = "None"
				return rows
			},
			setup: func(f *bulkFixture) {
				f.repo.AddCommittee(&model.Committee{
					CommitteeBase:     model.CommitteeBase{UID: f.committeeUID, Name: "Test Committee", Category: "Technical", EnableVoting: true},
					CommitteeSettings: &model.CommitteeSettings{UID: f.committeeUID},
				})
			},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, model.BulkMemberFailed, results[1].Status)
				assert.Equal(t, model.BulkReasonInvalid, results[1].Reason)
				assert.False(t, results[1].Retryable)
				assert.Contains(t, results[1].Message, "voting_status")
				assert.Equal(t, model.BulkMemberAdded, results[0].Status)
				assert.Equal(t, model.BulkMemberAdded, results[2].Status, "an invalid row never halts the import")
				assert.EqualValues(t, 2, f.writer.writes.Load())
			},
		},
		{
			name: "keeps an unproven storage conflict retryable",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(2) },
			setup: func(f *bulkFixture) {
				f.writer.errFor[bulkTestEmail(1)] = errs.NewConflict("member with the same email already exists in the committee")
			},
			check: func(t *testing.T, _ *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, model.BulkMemberFailed, results[0].Status)
				assert.Equal(t, model.BulkReasonTemporary, results[0].Reason)
				assert.True(t, results[0].Retryable)
			},
		},
		{
			name: "stops starting rows after a temporary failure",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(10) },
			setup: func(f *bulkFixture) {
				f.writer.errAll = errs.NewUnexpected("kv bucket unavailable: secret-detail")
			},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				writes := int(f.writer.writes.Load())
				assert.GreaterOrEqual(t, writes, 1)
				assert.LessOrEqual(t, writes, maxBulkMemberWorkers, "only rows already started may finish")
				var temporary, notAttempted int
				for _, r := range results {
					assert.Equal(t, model.BulkMemberFailed, r.Status)
					assert.True(t, r.Retryable)
					assert.NotContains(t, r.Message, "secret-detail")
					switch r.Reason {
					case model.BulkReasonTemporary:
						temporary++
					case model.BulkReasonNotAttempted:
						notAttempted++
					}
				}
				assert.Equal(t, writes, temporary)
				assert.Equal(t, len(results)-writes, notAttempted)
				assert.Zero(t, f.publisher.MemberPutCallCount)
			},
		},
		{
			name: "does not write a row whose identity lookup stays unresolved",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(3) },
			setup: func(f *bulkFixture) {
				f.reader.err = errs.NewServiceUnavailable("auth-service unavailable")
			},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Zero(t, f.writer.writes.Load())
				assert.Zero(t, f.publisher.MemberPutCallCount)
				var temporary int
				for _, r := range results {
					assert.Equal(t, model.BulkMemberFailed, r.Status)
					assert.True(t, r.Retryable)
					if r.Reason == model.BulkReasonTemporary {
						temporary++
						assert.Contains(t, r.Message, "identity lookup unavailable")
					}
				}
				assert.GreaterOrEqual(t, temporary, 1)
			},
		},
		{
			name: "skip enrichment stores rows as submitted without any lookup",
			rows: func() []*model.CommitteeMember {
				rows := bulkTestMembers(2)
				rows[0].Username = "first-last"
				return rows
			},
			setup: func(f *bulkFixture) {
				f.reader.err = errs.NewServiceUnavailable("auth-service unavailable")
			},
			opts: BulkMemberCreateOptions{SkipEnrichment: true},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, 2, countBulkResults(results)[model.BulkMemberAdded])
				assert.Equal(t, "first-last", results[0].Member.Username)
				assert.Zero(t, f.reader.calls.Load())
			},
		},
		{
			name: "runs the enrich callback once per row it creates",
			rows: func() []*model.CommitteeMember {
				rows := bulkTestMembers(3)
				rows[2].Email = bulkTestEmail(1)
				return rows
			},
			enrich: true,
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, model.BulkReasonDuplicateInRequest, results[2].Reason)
				assert.EqualValues(t, 2, f.enriched.Load(), "the duplicate row is never enriched")
			},
		},
		{
			name: "returns a request error when the committee does not exist",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(1) },
			setup: func(f *bulkFixture) {
				f.committeeUID = "committee-bulk-missing-" + uuid.NewString()
			},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				var notFound errs.NotFound
				require.ErrorAs(t, err, &notFound)
				assert.Nil(t, results)
				assert.Zero(t, f.writer.writes.Load())
			},
		},
		{
			name: "rejects an empty request",
			rows: func() []*model.CommitteeMember { return nil },
			check: func(t *testing.T, _ *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				var validation errs.Validation
				require.ErrorAs(t, err, &validation)
				assert.Nil(t, results)
			},
		},
		{
			name: "rejects more than the maximum number of rows",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(model.MaxBulkMembers + 1) },
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				var validation errs.Validation
				require.ErrorAs(t, err, &validation)
				assert.Nil(t, results)
				assert.Zero(t, f.writer.writes.Load())
			},
		},
		{
			name: "proceeds without the existing-member pre-check when listing fails",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(2) },
			setup: func(f *bulkFixture) {
				f.orchestrator.committeeReader = listMembersFailingReader{CommitteeReader: f.repo}
				f.writer.errFor[bulkTestEmail(1)] = errs.NewConflict("member with the same email already exists in the committee")
			},
			check: func(t *testing.T, _ *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, model.BulkReasonTemporary, results[0].Reason, "a reservation alone never proves membership")
				assert.True(t, results[0].Retryable)
				assert.Empty(t, results[0].MemberUID)
			},
		},
		{
			name: "never runs more than the worker limit at once",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(12) },
			setup: func(f *bulkFixture) {
				f.writer.delay = 10 * time.Millisecond
			},
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, 12, countBulkResults(results)[model.BulkMemberAdded])
				assert.LessOrEqual(t, int(f.writer.maxInFlight.Load()), maxBulkMemberWorkers)
			},
		},
		{
			name: "resubmitting completed rows adds nothing again",
			rows: func() []*model.CommitteeMember { return bulkTestMembers(3) },
			check: func(t *testing.T, f *bulkFixture, _ []*model.CommitteeMember, results []*model.BulkMemberResult, err error) {
				require.NoError(t, err)
				require.Equal(t, 3, countBulkResults(results)[model.BulkMemberAdded])
				indexed := len(f.publisher.IndexerMessages)

				again, err := f.orchestrator.CreateMembers(context.Background(), f.committeeUID, bulkTestMembers(3), BulkMemberCreateOptions{})
				require.NoError(t, err)
				for i, r := range again {
					assert.Equal(t, model.BulkMemberSkipped, r.Status)
					assert.Equal(t, model.BulkReasonAlreadyMember, r.Reason)
					assert.Equal(t, results[i].MemberUID, r.MemberUID)
				}
				assert.EqualValues(t, 3, f.writer.writes.Load(), "the resubmission writes nothing")
				assert.Len(t, f.publisher.IndexerMessages, indexed, "the resubmission publishes nothing")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newBulkFixture()
			if tc.setup != nil {
				tc.setup(f)
			}
			rows := tc.rows()
			opts := tc.opts
			if tc.enrich {
				opts.Enrich = func(context.Context, *model.CommitteeMember) { f.enriched.Add(1) }
			}

			results, err := f.orchestrator.CreateMembers(context.Background(), f.committeeUID, rows, opts)

			if err == nil {
				// Every submitted row gets exactly one result, in submission order.
				require.Len(t, results, len(rows))
				for i, r := range results {
					assert.Equal(t, i, r.Index)
					assert.Equal(t, rows[i].Email, r.Email)
					assert.Contains(t, []model.BulkMemberStatus{model.BulkMemberAdded, model.BulkMemberSkipped, model.BulkMemberFailed}, r.Status)
					if r.Status != model.BulkMemberAdded {
						assert.NotEmpty(t, r.Reason)
						assert.Nil(t, r.Member)
					}
				}
			}
			tc.check(t, f, rows, results, err)
		})
	}
}

// countingB2BOrgResolver counts ResolveByUID calls and fails while err is set.
type countingB2BOrgResolver struct {
	calls atomic.Int32
	err   error
}

func (r *countingB2BOrgResolver) ResolveByUID(_ context.Context, uid string) (string, bool, error) {
	r.calls.Add(1)
	if r.err != nil {
		return "", false, r.err
	}
	return strings.ToUpper(uid), true, nil
}

// countingB2BOrgFallbackResolver counts ResolveSFID calls.
type countingB2BOrgFallbackResolver struct {
	calls atomic.Int32
}

func (r *countingB2BOrgFallbackResolver) ResolveSFID(context.Context, string, string) (string, bool, error) {
	r.calls.Add(1)
	return "001B000000IqhSLIAZ", true, nil
}

func TestOrgResolutionMemo(t *testing.T) {
	ctx := context.Background()
	const sfid = "001B000000IqhSLIAZ"

	t.Run("reuses a successful uid resolution across rows", func(t *testing.T) {
		orchestrator, _, _ := setupMemberWriterTest()
		resolver := &countingB2BOrgResolver{}
		orchestrator.b2bOrgResolver = resolver
		memo := newOrgResolutionMemo()

		first := model.CommitteeMemberOrganization{ID: sfid}
		second := model.CommitteeMemberOrganization{ID: " " + sfid + " "}
		require.NoError(t, orchestrator.sanitizeMemberOrganization(ctx, &first, memo))
		require.NoError(t, orchestrator.sanitizeMemberOrganization(ctx, &second, memo))

		assert.EqualValues(t, 1, resolver.calls.Load())
		assert.Equal(t, first.ID, second.ID)
	})

	t.Run("never caches a resolver error", func(t *testing.T) {
		orchestrator, _, _ := setupMemberWriterTest()
		resolver := &countingB2BOrgResolver{err: errs.NewServiceUnavailable("member-service unavailable")}
		orchestrator.b2bOrgResolver = resolver
		memo := newOrgResolutionMemo()

		org := model.CommitteeMemberOrganization{ID: sfid}
		require.Error(t, orchestrator.sanitizeMemberOrganization(ctx, &org, memo))
		assert.Equal(t, sfid, org.ID, "a resolver error keeps the submitted id (fail-open)")

		resolver.err = nil
		require.NoError(t, orchestrator.sanitizeMemberOrganization(ctx, &org, memo))
		assert.EqualValues(t, 2, resolver.calls.Load(), "the failed lookup is retried for the next row")
	})

	t.Run("reuses a successful name and website fallback", func(t *testing.T) {
		orchestrator, _, _ := setupMemberWriterTest()
		fallback := &countingB2BOrgFallbackResolver{}
		orchestrator.b2bOrgFallbackResolver = fallback
		memo := newOrgResolutionMemo()

		for range 2 {
			org := model.CommitteeMemberOrganization{ID: "legacy-org-id", Name: "Example", Website: "https://example.com"}
			require.NoError(t, orchestrator.sanitizeMemberOrganization(ctx, &org, memo))
			assert.Equal(t, sfid, org.ID)
		}
		assert.EqualValues(t, 1, fallback.calls.Load())
	})

	t.Run("a nil memo resolves every time", func(t *testing.T) {
		orchestrator, _, _ := setupMemberWriterTest()
		resolver := &countingB2BOrgResolver{}
		orchestrator.b2bOrgResolver = resolver

		for range 2 {
			org := model.CommitteeMemberOrganization{ID: sfid}
			require.NoError(t, orchestrator.sanitizeMemberOrganization(ctx, &org, nil))
		}
		assert.EqualValues(t, 2, resolver.calls.Load())
	})
}

func TestBulkMemberErrorMessage(t *testing.T) {
	for _, err := range []error{
		errs.NewServiceUnavailable("identity lookup unavailable", fmt.Errorf("private-infrastructure-cause")),
		errs.NewValidation("invalid voting_status", fmt.Errorf("private-infrastructure-cause")),
		errs.NewConflict("member exists", fmt.Errorf("private-infrastructure-cause")),
		errs.NewNotFound("member missing", fmt.Errorf("private-infrastructure-cause")),
		errs.NewForbidden("operation forbidden", fmt.Errorf("private-infrastructure-cause")),
		errs.NewUnexpected("private-infrastructure-cause"),
	} {
		t.Run(fmt.Sprintf("%T", err), func(t *testing.T) {
			message := bulkMemberErrorMessage(err)
			require.NotEmpty(t, message)
			require.NotContains(t, message, "private-infrastructure-cause")
		})
	}
}
