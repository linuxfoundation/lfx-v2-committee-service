// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"
)

// CommitteePublisher defines the behavior of a service that publishes committee messages
type CommitteePublisher interface {
	Indexer(ctx context.Context, subject string, message any, sync bool) error
	UpdateAccess(ctx context.Context, message any) error
	DeleteAccess(ctx context.Context, message any) error
	MemberPut(ctx context.Context, message any) error
	MemberRemove(ctx context.Context, message any) error
	Event(ctx context.Context, subject string, event any, sync bool) error
}
