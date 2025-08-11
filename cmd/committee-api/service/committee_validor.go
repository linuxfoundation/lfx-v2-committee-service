// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"strconv"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

func etagValidator(etag *string) (uint64, error) {

	// Parse ETag to get revision for optimistic locking
	if etag == nil || *etag == "" {
		return 0, errors.NewValidation("ETag is required for update operations")
	}
	parsedRevision, errParse := strconv.ParseUint(*etag, 10, 64)
	if errParse != nil {
		return 0, errors.NewValidation("invalid ETag format", errParse)
	}

	return parsedRevision, nil
}
