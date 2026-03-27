// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model_test

import (
	"context"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func TestCommitteeLink_BuildIndexKey_NotEmpty(t *testing.T) {
	link := &model.CommitteeLink{
		CommitteeUID: "committee-uid-1",
		UID:          "link-uid-1",
	}
	key := link.BuildIndexKey(context.Background())
	assert.NotEmpty(t, key)
	assert.Len(t, key, 64) // SHA-256 hex is 64 chars
}

func TestCommitteeLinkFolder_BuildIndexKey_UniquePerName(t *testing.T) {
	f1 := &model.CommitteeLinkFolder{
		CommitteeUID: "committee-uid-1",
		Name:         "Meeting Notes",
	}
	f2 := &model.CommitteeLinkFolder{
		CommitteeUID: "committee-uid-1",
		Name:         "Proposals",
	}
	assert.NotEqual(t, f1.BuildIndexKey(context.Background()), f2.BuildIndexKey(context.Background()))
}

func TestCommitteeLinkFolder_BuildIndexKey_UniquePerCommittee(t *testing.T) {
	f1 := &model.CommitteeLinkFolder{
		CommitteeUID: "committee-uid-1",
		Name:         "Meeting Notes",
	}
	f2 := &model.CommitteeLinkFolder{
		CommitteeUID: "committee-uid-2",
		Name:         "Meeting Notes",
	}
	assert.NotEqual(t, f1.BuildIndexKey(context.Background()), f2.BuildIndexKey(context.Background()))
}
