// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package errors

import "errors"

// Validation represents a validation error in the application.
type Validation struct {
	base
}

// Error returns the error message for Validation.
func (v Validation) Error() string {
	return v.error()
}

// NewValidation creates a new Validation error with the provided message.
func NewValidation(message string, err ...error) Validation {
	return Validation{
		base: base{
			message: message,
			err:     errors.Join(err...),
		},
	}
}
