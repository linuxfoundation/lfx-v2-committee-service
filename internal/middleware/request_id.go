// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"

	"github.com/google/uuid"
)

// RequestIDMiddleware creates a middleware that adds a request ID to the context
func RequestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to get request ID from header first
			requestID := r.Header.Get(string(constants.RequestIDHeader))

			// If no request ID in header, generate a new one
			if requestID == "" {
				requestID = generateRequestID()
			}

			// Add request ID to response header
			w.Header().Set(string(constants.RequestIDHeader), requestID)

			// Add request ID to context
			ctx := context.WithValue(r.Context(), constants.RequestIDHeader, requestID)

			// Log the request ID using the context-aware logger
			// So using slog along with the context
			// This allows the request ID to be included in all logs for this request
			ctx = log.AppendCtx(ctx, slog.String(string(constants.RequestIDHeader), requestID))

			// Create a new request with the updated context
			r = r.WithContext(ctx)

			// Call the next handler
			next.ServeHTTP(w, r)
		})
	}
}

// generateRequestID generates a new unique request ID
func generateRequestID() string {
	return uuid.New().String()
}
