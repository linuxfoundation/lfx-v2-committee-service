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
		case errors.EditedBriefExists:
			return &committeeservice.GroupWeeklyBriefEditedExistsError{
				Code:     "edited_brief_exists",
				Revision: e.Revision,
			}
		case errors.Conflict:
			return &committeeservice.ConflictError{
				Message: e.Error(),
			}
		case errors.Forbidden:
			return &committeeservice.ForbiddenError{
				Message: e.Error(),
			}
		case errors.Unprocessable:
			return &committeeservice.GroupWeeklyBriefNoSourceError{
				Code:    e.Code,
				Message: e.Error(),
			}
		case errors.TooManyRequests:
			return &committeeservice.GroupWeeklyBriefThrottleExceededError{
				Code:               "throttle_exceeded",
				GeneratesUsed:      e.GeneratesUsed,
				GeneratesLimit:     e.GeneratesLimit,
				RegenerationsUsed:  e.RegenerationsUsed,
				RegenerationsLimit: e.RegenerationsLimit,
				WindowResetsAt:     e.WindowResetsAt,
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
