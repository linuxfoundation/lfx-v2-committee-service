// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func wrapError(ctx context.Context, err error) error {

	f := func(err error) error {
		switch e := err.(type) {
		case errors.Validation:
			return &committeeservice.BadRequestError{
				Message: e.Error(),
			}
		case errors.NotFound:
			return &committeeservice.NotFoundError{
				Message: e.Error(),
			}
		case errors.Conflict:
			return &committeeservice.ConflictError{
				Message: e.Error(),
			}
		case errors.ServiceUnavailable:
			return &committeeservice.ServiceUnavailableError{
				Message: e.Error(),
			}
		default:
			return &committeeservice.InternalServerError{
				Message: e.Error(),
			}
		}
	}

	slog.ErrorContext(ctx, "request failed",
		"error", err,
	)
	return f(err)
}
