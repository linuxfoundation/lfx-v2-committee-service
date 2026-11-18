// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"net/url"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// ValidateCharterURL preserves explicit clears and ordinary HTTP(S) links, but
// rejects credentials before a new charter can enter source or audit storage.
func ValidateCharterURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errs.NewValidation("charter URL must be an HTTP(S) link without credentials")
	}
	return nil
}

// sanitizeCharterURL removes legacy userinfo before emitting either snapshots
// or projections. Unparseable legacy links are omitted, never echoed as errors.
func sanitizeCharterURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}
