// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
)

func TestDocumentReaderOrchestrator_GetDocumentMetadata(t *testing.T) {
	ctx := context.Background()

	committeeUID := uuid.New().String()
	documentUID := uuid.New().String()
	testDoc := &model.CommitteeDocument{
		UID:                documentUID,
		CommitteeUID:       committeeUID,
		Name:               "Architecture Overview",
		Description:        "High-level architecture document",
		FileName:           "arch.pdf",
		FileSize:           1024,
		ContentType:        "application/pdf",
		UploadedByUsername: "user-1",
		CreatedAt:          time.Now().Add(-24 * time.Hour),
		UpdatedAt:          time.Now(),
	}

	tests := []struct {
		name         string
		setupMock    func(*mock.MockDocumentRepository)
		committeeUID string
		documentUID  string
		wantErr      bool
		validate     func(*testing.T, *model.CommitteeDocument, uint64)
	}{
		{
			name: "successful metadata retrieval",
			setupMock: func(repo *mock.MockDocumentRepository) {
				_ = repo.CreateDocumentMetadata(ctx, testDoc)
			},
			committeeUID: committeeUID,
			documentUID:  documentUID,
			wantErr:      false,
			validate: func(t *testing.T, doc *model.CommitteeDocument, revision uint64) {
				require.NotNil(t, doc)
				assert.Equal(t, documentUID, doc.UID)
				assert.Equal(t, committeeUID, doc.CommitteeUID)
				assert.Equal(t, "Architecture Overview", doc.Name)
				assert.Equal(t, "High-level architecture document", doc.Description)
				assert.Equal(t, "arch.pdf", doc.FileName)
				assert.Equal(t, int64(1024), doc.FileSize)
				assert.Equal(t, "application/pdf", doc.ContentType)
				assert.Equal(t, "user-1", doc.UploadedByUsername)
				assert.False(t, doc.CreatedAt.IsZero())
				assert.Equal(t, uint64(1), revision)
			},
		},
		{
			name:         "document not found",
			setupMock:    func(_ *mock.MockDocumentRepository) {},
			committeeUID: committeeUID,
			documentUID:  "nonexistent-uid",
			wantErr:      true,
			validate: func(t *testing.T, doc *model.CommitteeDocument, revision uint64) {
				assert.Nil(t, doc)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name: "wrong committee UID returns not found",
			setupMock: func(repo *mock.MockDocumentRepository) {
				_ = repo.CreateDocumentMetadata(ctx, testDoc)
			},
			committeeUID: "wrong-committee-uid",
			documentUID:  documentUID,
			wantErr:      true,
			validate: func(t *testing.T, doc *model.CommitteeDocument, revision uint64) {
				assert.Nil(t, doc)
				assert.Equal(t, uint64(0), revision)
			},
		},
		{
			name:         "empty document UID",
			setupMock:    func(_ *mock.MockDocumentRepository) {},
			committeeUID: committeeUID,
			documentUID:  "",
			wantErr:      true,
			validate: func(t *testing.T, doc *model.CommitteeDocument, revision uint64) {
				assert.Nil(t, doc)
				assert.Equal(t, uint64(0), revision)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := mock.NewMockDocumentRepository()
			tt.setupMock(repo)

			reader := NewDocumentReaderOrchestrator(WithDocumentReader(repo))

			doc, revision, err := reader.GetDocumentMetadata(ctx, tt.committeeUID, tt.documentUID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			tt.validate(t, doc, revision)
		})
	}
}

func TestDocumentReaderOrchestrator_GetDocumentFile(t *testing.T) {
	ctx := context.Background()

	documentUID := uuid.New().String()
	fileData := []byte("PDF file content here")

	tests := []struct {
		name        string
		setupMock   func(*mock.MockDocumentRepository)
		documentUID string
		wantErr     bool
		validate    func(*testing.T, []byte)
	}{
		{
			name: "successful file retrieval",
			setupMock: func(repo *mock.MockDocumentRepository) {
				_ = repo.PutDocumentFile(ctx, documentUID, fileData)
			},
			documentUID: documentUID,
			wantErr:     false,
			validate: func(t *testing.T, data []byte) {
				require.NotNil(t, data)
				assert.Equal(t, fileData, data)
			},
		},
		{
			name:        "file not found",
			setupMock:   func(_ *mock.MockDocumentRepository) {},
			documentUID: "nonexistent-uid",
			wantErr:     true,
			validate: func(t *testing.T, data []byte) {
				assert.Nil(t, data)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := mock.NewMockDocumentRepository()
			tt.setupMock(repo)

			reader := NewDocumentReaderOrchestrator(WithDocumentReader(repo))

			data, err := reader.GetDocumentFile(ctx, tt.documentUID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			tt.validate(t, data)
		})
	}
}

func TestNewDocumentReaderOrchestrator_PanicsWithoutReader(t *testing.T) {
	assert.Panics(t, func() {
		NewDocumentReaderOrchestrator()
	})
}
