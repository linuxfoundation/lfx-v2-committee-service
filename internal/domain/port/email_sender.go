// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import (
	"context"

	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// EmailSender sends transactional emails via the email service.
type EmailSender interface {
	SendEmail(ctx context.Context, req emailapi.SendEmailRequest) error
}
