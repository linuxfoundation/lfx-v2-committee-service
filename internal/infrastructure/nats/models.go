// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// Config represents NATS configuration
type Config struct {
	// URL is the NATS server URL
	URL string `json:"url"`
	// Timeout is the request timeout duration
	Timeout time.Duration `json:"timeout"`
	// MaxReconnect is the maximum number of reconnection attempts
	MaxReconnect int `json:"max_reconnect"`
	// ReconnectWait is the time to wait between reconnection attempts
	ReconnectWait time.Duration `json:"reconnect_wait"`
}

// AccessCheckNATSRequest represents a NATS request for access checking
type AccessCheckNATSRequest struct {
	// Subject is the NATS subject for the request
	Subject string `json:"subject"`
	// Message is the serialized request data
	Message []byte `json:"message"`
	// Timeout is the request timeout duration
	Timeout time.Duration `json:"timeout"`
}

// AccessCheckNATSResponse represents a NATS response for access checking
type AccessCheckNATSResponse map[string]string

// ErrorMessageNATSResponse represents a NATS response for error message
type ErrorMessageNATSResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// UserEmailsNATSResponse represents the response from lfx.auth-service.user_emails.read
type UserEmailsNATSResponse struct {
	Success bool                    `json:"success"`
	Error   string                  `json:"error,omitempty"`
	Data    *UserEmailsNATSDataBody `json:"data,omitempty"`
}

// UserEmailsNATSDataBody holds the email payload from the auth-service user_emails response
type UserEmailsNATSDataBody struct {
	PrimaryEmail    string                    `json:"primary_email"`
	AlternateEmails []UserEmailsNATSAlternate `json:"alternate_emails"`
}

// UserEmailsNATSAlternate represents a single alternate email entry
type UserEmailsNATSAlternate struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

// CheckError parses a JSON message and returns an error if the operation was unsuccessful.
func (e ErrorMessageNATSResponse) CheckError(message string) error {
	if errUnmarshal := json.Unmarshal([]byte(message), &e); errUnmarshal == nil {
		if !e.Success {
			if strings.Contains(e.Error, "not found") {
				return errors.NewNotFound(e.Error)
			}
			return errors.NewUnexpected(e.Error)
		}
	}
	return nil
}
