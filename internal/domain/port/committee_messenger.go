// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

// TransportMessenger represents the behavior of a message that can be sent to the committee API.
type TransportMessenger interface {
	Subject() string
	Data() []byte
	Respond(data []byte) error
}

// StreamMessenger represents the behavior of a durable stream message consumed by the committee service.
// ACK/NAK mechanics are handled by the infrastructure layer; handlers read subject and payload only.
type StreamMessenger interface {
	Subject() string
	Data() []byte
}
