// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeRetriever provides access to committee reading operations
type CommitteeRetriever interface {
	Base() CommitteeBaseRetriever
	Settings() CommitteeSettingsRetriever
}

// CommitteeBaseRetriever handles committee base data reading operations
type CommitteeBaseRetriever interface {
	Get(ctx context.Context, uid string) (*model.Committee, error)
	ByNameProject(ctx context.Context, name, projectUID string) (*model.Committee, error)
	BySSOGroupName(ctx context.Context, name string) (*model.Committee, error)
}

// CommitteeSettingsRetriever handles committee settings reading operations
type CommitteeSettingsRetriever interface {
	Get(ctx context.Context, committeeUID string) (*model.CommitteeSettings, error)
}
