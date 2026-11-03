// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// panickingProjectReader panics on the project lookup, which both create flows run before anything
// is persisted.
type panickingProjectReader struct{ port.ProjectReader }

func (panickingProjectReader) Slug(context.Context, string) (string, error) {
	panic("project lookup blew up: secret-detail")
}

// panickingCommitteeReader panics when the member flow reads the committee.
type panickingCommitteeReader struct{ port.CommitteeReader }

func (panickingCommitteeReader) GetBase(context.Context, string) (*model.CommitteeBase, uint64, error) {
	panic("committee read blew up: secret-detail")
}

func TestHandleCreatePanic(t *testing.T) {
	t.Run("rolls back an entity that was already persisted", func(t *testing.T) {
		rolledBack := 0
		err := handleCreatePanic(context.Background(), "committee", "boom: secret-detail", true, func() { rolledBack++ })

		assert.Equal(t, 1, rolledBack)
		var unexpected errs.Unexpected
		require.ErrorAs(t, err, &unexpected)
		assert.Equal(t, "failed to create committee", unexpected.Error())
		assert.NotContains(t, err.Error(), "secret-detail", "the panic value must not reach the caller")
	})

	t.Run("does not roll back when nothing was persisted", func(t *testing.T) {
		err := handleCreatePanic(context.Background(), "committee member", "boom", false, func() {
			t.Fatal("rollback must not run before the entity is persisted")
		})

		var unexpected errs.Unexpected
		require.ErrorAs(t, err, &unexpected)
	})
}

// A panic during create used to be recovered and swallowed, so the caller got (nil, nil).
func TestCreate_PanicReturnsError(t *testing.T) {
	t.Run("committee", func(t *testing.T) {
		mockRepo := mock.NewMockRepository()
		mockRepo.ClearAll()
		orchestrator := NewCommitteeWriterOrchestrator(
			WithCommitteeRetriever(mock.NewMockCommitteeReader(mockRepo)),
			WithCommitteeWriter(NewTestMockCommitteeWriter(mockRepo)),
			WithProjectRetriever(panickingProjectReader{}),
			WithCommitteePublisher(mock.NewMockCommitteePublisher()),
		)

		created, err := orchestrator.Create(context.Background(), &model.Committee{
			CommitteeBase:     model.CommitteeBase{ProjectUID: "project-1", Name: "Test Committee", Public: true},
			CommitteeSettings: &model.CommitteeSettings{},
		}, false)

		assert.Nil(t, created)
		var unexpected errs.Unexpected
		require.ErrorAs(t, err, &unexpected)
		assert.NotContains(t, err.Error(), "secret-detail")
	})

	t.Run("committee member", func(t *testing.T) {
		orchestrator, _, memberWriter := setupMemberWriterTest()
		orchestrator.committeeReader = panickingCommitteeReader{}

		created, err := orchestrator.CreateMember(context.Background(),
			testMember("", "committee-123", "first.last@example.com", "", ""), false, true)

		assert.Nil(t, created)
		var unexpected errs.Unexpected
		require.ErrorAs(t, err, &unexpected)
		assert.NotContains(t, err.Error(), "secret-detail")
		assert.Empty(t, memberWriter.members, "nothing was persisted, so nothing is left behind")
	})
}
