// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
)

// CommitteePublisher defines the behavior of a service that publishes committee messages
type CommitteePublisher interface {
	Indexer(ctx context.Context, subject string, message any) error
	Access(ctx context.Context, subject string, message any) error
}
