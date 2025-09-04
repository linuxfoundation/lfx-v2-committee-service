// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// MessageHandler defines the interface for handling NATS messages
type MessageHandler interface {
	// HandleCommitteeGetAttribute handles committee get attribute messages
	HandleCommitteeGetAttribute(ctx context.Context, msg TransportMessenger, attribute string) ([]byte, error)
	// HandleCommitteeListMembers handles committee list members messages
	HandleCommitteeListMembers(ctx context.Context, msg TransportMessenger) ([]byte, error)
}
