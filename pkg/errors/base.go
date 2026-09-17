// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package errors

import "fmt"

// base is a struct that holds the common fields for error types
type base struct {
	message string
	err     error
}

// error is a method that returns the error message for the base struct
// any changes to the error message here will be reflected in all error types that embed base
func (b base) error() string {
	if b.err == nil {
		return b.message
	}
	return fmt.Sprintf("%s: %v", b.message, b.err)
}

// Unwrap returns the underlying cause so that errors.Is and errors.As can
// traverse the chain. When no cause was supplied the field is nil and the
// standard library short-circuits normally.
func (b base) Unwrap() error {
	return b.err
}
