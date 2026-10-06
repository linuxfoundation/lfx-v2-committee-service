// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
)

// mockCLIUserReader is a minimal UserReader for CLI tests, covering only UsernameByEmail.
type mockCLIUserReader struct {
	usernames map[string]string // email → username
	err       error             // if non-nil, returned for all lookups
}

func newMockCLIUserReader() *mockCLIUserReader {
	return &mockCLIUserReader{usernames: make(map[string]string)}
}

// withUsername registers an email → username mapping.
func (m *mockCLIUserReader) withUsername(email, username string) *mockCLIUserReader {
	m.usernames[email] = username
	return m
}

// withErr configures a global error returned for every UsernameByEmail call.
func (m *mockCLIUserReader) withErr(err error) *mockCLIUserReader {
	m.err = err
	return m
}

func (m *mockCLIUserReader) UsernameByEmail(_ context.Context, email string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if username, ok := m.usernames[email]; ok {
		return username, nil
	}
	return "", errs.NewNotFound("mock: username not found for email: " + email)
}

func (m *mockCLIUserReader) EmailsByAuthToken(_ context.Context, _ string) (*model.UserEmails, error) {
	return nil, nil
}

func (m *mockCLIUserReader) UserMetadataByPrincipal(_ context.Context, _ string) (*model.UserMetadata, error) {
	return nil, nil
}

// mockApplicationReader records application list calls.
type mockApplicationReader struct {
	applications []*model.CommitteeApplication
	listAllErr   error
	listErr      error
}

func (r *mockApplicationReader) GetApplication(_ context.Context, uid string) (*model.CommitteeApplication, uint64, error) {
	for _, a := range r.applications {
		if a.UID == uid {
			cp := *a
			return &cp, 1, nil
		}
	}
	return nil, 0, errors.New("not found")
}

func (r *mockApplicationReader) ListApplications(_ context.Context, committeeUID string) ([]*model.CommitteeApplication, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	var out []*model.CommitteeApplication
	for _, a := range r.applications {
		if a.CommitteeUID == committeeUID {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *mockApplicationReader) ListAllApplications(_ context.Context) ([]*model.CommitteeApplication, error) {
	if r.listAllErr != nil {
		return nil, r.listAllErr
	}
	var out []*model.CommitteeApplication
	for _, a := range r.applications {
		cp := *a
		out = append(out, &cp)
	}
	return out, nil
}

func makeTestApplication(uid, committeeUID, email, status string) *model.CommitteeApplication {
	return &model.CommitteeApplication{
		UID:            uid,
		CommitteeUID:   committeeUID,
		ApplicantEmail: email,
		Status:         status,
		Message:        "I'd like to join",
	}
}

func TestReindexApplications_NoApplications_Succeeds(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{},
		Publisher:                  &mockPublisher{},
	}
	err := sub.Run(context.Background(), rc)
	require.NoError(t, err)
}

func TestReindexApplications_MissingReader_ReturnsError(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	rc := commands.RunContext{
		Publisher: &mockPublisher{},
	}
	err := sub.Run(context.Background(), rc)
	require.Error(t, err)
}

func TestReindexApplications_DryRun_NoPublishes(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			applications: []*model.CommitteeApplication{
				makeTestApplication("app-1", "comm-1", "first.last@example.com", "pending"),
				makeTestApplication("app-2", "comm-1", "first.last@example.com", "approved"),
			},
		},
		Publisher: pub,
		Args:      []string{"--dry-run=true"},
	}
	err := sub.Run(context.Background(), rc)
	require.NoError(t, err)
	assert.Equal(t, 0, pub.indexerCalls, "no indexer calls in dry-run")
	assert.Equal(t, 0, pub.updateAccessCalls, "no FGA calls in dry-run")
}

func TestReindexApplications_PublishesIndexerAndFGA(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			applications: []*model.CommitteeApplication{
				makeTestApplication("app-1", "comm-1", "first.last@example.com", "pending"),
			},
		},
		Publisher: pub,
	}
	err := sub.Run(context.Background(), rc)
	require.NoError(t, err)
	assert.Equal(t, 1, pub.indexerCalls)
	assert.Equal(t, 1, pub.updateAccessCalls)
}

func TestReindexApplications_IndexerFail_CountedAsFailed(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{indexerErr: errors.New("indexer down")}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			applications: []*model.CommitteeApplication{
				makeTestApplication("app-1", "comm-1", "first.last@example.com", "pending"),
			},
		},
		Publisher: pub,
	}
	err := sub.Run(context.Background(), rc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to reindex")
}

func TestReindexApplications_FilterByCommitteeUID(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{}
	reader := &mockApplicationReader{
		applications: []*model.CommitteeApplication{
			makeTestApplication("app-1", "comm-1", "first.last@example.com", "pending"),
			makeTestApplication("app-2", "comm-2", "first.last@example.com", "pending"),
		},
	}
	rc := commands.RunContext{
		CommitteeApplicationReader: reader,
		Publisher:                  pub,
		Args:                       []string{"--committee-uid=comm-1"},
	}
	err := sub.Run(context.Background(), rc)
	require.NoError(t, err)
	// Only app-1 (comm-1) should be published.
	assert.Equal(t, 1, pub.indexerCalls)
	assert.Equal(t, 1, pub.updateAccessCalls)
}

func TestReindexApplications_IndexerMessagePayload(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			applications: []*model.CommitteeApplication{
				makeTestApplication("app-uid-42", "comm-uid-42", "first.last@example.com", "pending"),
			},
		},
		Publisher: pub,
	}
	err := sub.Run(context.Background(), rc)
	require.NoError(t, err)
	require.Len(t, pub.indexerMsgs, 1)
	msg, ok := pub.indexerMsgs[0].(*model.CommitteeIndexerMessage)
	require.True(t, ok, "indexer message should be *model.CommitteeIndexerMessage")
	assert.Equal(t, model.ActionUpdated, msg.Action)
	require.NotNil(t, msg.IndexingConfig)
	assert.Equal(t, "committee_application:app-uid-42", msg.IndexingConfig.AccessCheckObject)
	assert.Equal(t, "viewer", msg.IndexingConfig.AccessCheckRelation)
	assert.Equal(t, "committee:comm-uid-42", msg.IndexingConfig.HistoryCheckObject)
	assert.Equal(t, "auditor", msg.IndexingConfig.HistoryCheckRelation)
	assert.Equal(t, []string{"committee:comm-uid-42"}, msg.IndexingConfig.ParentRefs)
	require.NotNil(t, msg.IndexingConfig.Public, "Public must be set (not nil) to prevent public exposure")
	assert.False(t, *msg.IndexingConfig.Public, "applications must never be indexed as public")
}

func TestReindexApplications_FGATupleUsesCommitteeApplicationType(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			applications: []*model.CommitteeApplication{
				makeTestApplication("app-uid-1", "comm-uid-1", "first.last@example.com", "pending"),
			},
		},
		Publisher: pub,
	}
	err := sub.Run(context.Background(), rc)
	require.NoError(t, err)
	require.Len(t, pub.updateAccessMsgs, 1)
	msg, ok := pub.updateAccessMsgs[0].(fgatypes.GenericFGAMessage)
	require.True(t, ok)
	assert.Equal(t, "committee_application", msg.ObjectType)
	assert.Equal(t, "update_access", msg.Operation)
	data, ok := msg.Data.(fgatypes.GenericAccessData)
	require.True(t, ok)
	assert.Equal(t, "app-uid-1", data.UID)
	refs := data.References["committee"]
	require.Len(t, refs, 1)
	assert.Equal(t, "comm-uid-1", refs[0])
}

// TestReindexApplications_ListAllErr_ReturnsError verifies that a failure to list applications
// from the KV bucket is surfaced as an error (not silently swallowed).
func TestReindexApplications_ListAllErr_ReturnsError(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			listAllErr: errors.New("kv scan failed"),
		},
		Publisher: &mockPublisher{},
	}
	err := sub.Run(context.Background(), rc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list applications")
}

// TestReindexApplications_UserReaderResolvesUsername verifies that when the UserReader resolves
// the applicant email to a username, the applicant relation is written in the FGA message.
func TestReindexApplications_UserReaderResolvesUsername(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			applications: []*model.CommitteeApplication{
				makeTestApplication("app-1", "comm-1", "first.last@example.com", "pending"),
			},
		},
		Publisher:  pub,
		UserReader: newMockCLIUserReader().withUsername("first.last@example.com", "first-last"),
	}
	err := sub.Run(context.Background(), rc)
	require.NoError(t, err)
	require.Len(t, pub.updateAccessMsgs, 1)
	msg, ok := pub.updateAccessMsgs[0].(fgatypes.GenericFGAMessage)
	require.True(t, ok)
	data, ok := msg.Data.(fgatypes.GenericAccessData)
	require.True(t, ok)
	// Applicant relation should be written.
	assert.Equal(t, map[string][]string{"applicant": {"first-last"}}, data.Relations)
	assert.Nil(t, data.ExcludeRelations, "ExcludeRelations must be nil when applicant tuple is written")
}

// TestReindexApplications_UserReaderNotFound verifies that when the applicant email has no LFID,
// ExcludeRelations is set and the applicant relation is not written.
func TestReindexApplications_UserReaderNotFound(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			applications: []*model.CommitteeApplication{
				makeTestApplication("app-1", "comm-1", "first.last@example.com", "pending"),
			},
		},
		Publisher: pub,
		// newMockCLIUserReader without a registered mapping returns NotFound for any email.
		UserReader: newMockCLIUserReader(),
	}
	err := sub.Run(context.Background(), rc)
	require.NoError(t, err) // NotFound is not an error; item is not counted as failed.
	require.Len(t, pub.updateAccessMsgs, 1)
	msg, ok := pub.updateAccessMsgs[0].(fgatypes.GenericFGAMessage)
	require.True(t, ok)
	data, ok := msg.Data.(fgatypes.GenericAccessData)
	require.True(t, ok)
	assert.Nil(t, data.Relations, "applicant relation must not be written when email has no LFID")
	assert.Equal(t, []string{"applicant"}, data.ExcludeRelations)
}

// TestReindexApplications_UserReaderTransientError verifies that a transient (non-NotFound)
// username lookup error causes the application to be counted as failed, returning a non-zero exit.
func TestReindexApplications_UserReaderTransientError(t *testing.T) {
	t.Parallel()
	sub := &reindexApplicationsSubcommand{}
	pub := &mockPublisher{}
	rc := commands.RunContext{
		CommitteeApplicationReader: &mockApplicationReader{
			applications: []*model.CommitteeApplication{
				makeTestApplication("app-1", "comm-1", "first.last@example.com", "pending"),
			},
		},
		Publisher:  pub,
		UserReader: newMockCLIUserReader().withErr(errors.New("auth-service timeout")),
	}
	err := sub.Run(context.Background(), rc)
	require.Error(t, err, "transient UserReader error must cause non-zero exit")
	assert.Contains(t, err.Error(), "failed to reindex")
	// FGA message should not have been published for the failed application.
	assert.Equal(t, 0, pub.updateAccessCalls)
}
