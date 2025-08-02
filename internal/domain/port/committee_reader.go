// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

// CommitteeReader provides access to committee reading operations
type CommitteeReader interface {
	CommitteeBaseReader
	CommitteeSettingsReader
}

// CommitteeBaseReader handles committee base data reading operations
type CommitteeBaseReader interface {
	GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error)
	GetRevision(ctx context.Context, uid string) (uint64, error)
}

// CommitteeSettingsReader handles committee settings reading operations
type CommitteeSettingsReader interface {
	GetSettings(ctx context.Context, committeeUID string) (*model.CommitteeSettings, uint64, error)
}
