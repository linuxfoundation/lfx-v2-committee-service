// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/stretchr/testify/assert"
)

func TestRequestIDMiddleware(t *testing.T) {
	tests := []struct {
		name              string
		existingRequestID string
		expectGenerated   bool
		expectHeaderSet   bool
	}{
		{
			name:              "generates new request ID when none provided",
			existingRequestID: "",
			expectGenerated:   true,
			expectHeaderSet:   true,
		},
		{
			name:              "uses existing request ID when provided",
			existingRequestID: "existing-id-123",
			expectGenerated:   false,
			expectHeaderSet:   true,
		},
		{
			name:              "uses existing request ID with UUID format",
			existingRequestID: "550e8400-e29b-41d4-a716-446655440000",
			expectGenerated:   false,
			expectHeaderSet:   true,
		},
	}

	assertion := assert.New(t)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedRequestID string
			var capturedContext context.Context

			// Test handler that captures the request ID and context
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedRequestID = getRequestIDFromContext(r.Context())
				capturedContext = r.Context()
				w.WriteHeader(http.StatusOK)
			})

			// Wrap handler with RequestID middleware
			middleware := RequestIDMiddleware()
			wrappedHandler := middleware(handler)

			// Create request
			req := httptest.NewRequest("GET", "/test", nil)
			if tc.existingRequestID != "" {
				req.Header.Set(string(constants.RequestIDHeader), tc.existingRequestID)
			}

			rec := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(rec, req)

			// Verify request ID was captured
			assertion.NotEmpty(capturedRequestID)

			// Verify request ID matches expectation
			if tc.expectGenerated {
				// Should be a UUID format (36 characters with dashes)
				assertion.Equal(36, len(capturedRequestID))
				assertion.Contains(capturedRequestID, "-")
			} else {
				assertion.Equal(tc.existingRequestID, capturedRequestID)
			}

			// Verify response header contains request ID
			if tc.expectHeaderSet {
				responseRequestID := rec.Header().Get(string(constants.RequestIDHeader))
				assertion.Equal(capturedRequestID, responseRequestID)
			}

			// Verify context contains request ID
			contextRequestID := getRequestIDFromContext(capturedContext)
			assertion.Equal(capturedRequestID, contextRequestID)
		})
	}
}

func TestMiddlewareIntegration(t *testing.T) {
	tests := []struct {
		name         string
		numRequests  int
		expectUnique bool
	}{
		{
			name:         "generates different request IDs for multiple requests",
			numRequests:  3,
			expectUnique: true,
		},
		{
			name:         "handles single request correctly",
			numRequests:  1,
			expectUnique: true,
		},
		{
			name:         "handles many requests correctly",
			numRequests:  5,
			expectUnique: true,
		},
	}

	assertion := assert.New(t)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requestIDs []string

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestID := getRequestIDFromContext(r.Context())
				requestIDs = append(requestIDs, requestID)
				w.WriteHeader(http.StatusOK)
			})

			middleware := RequestIDMiddleware()
			wrappedHandler := middleware(handler)

			// Make multiple requests
			for i := 0; i < tc.numRequests; i++ {
				req := httptest.NewRequest("GET", "/test", nil)
				rec := httptest.NewRecorder()
				wrappedHandler.ServeHTTP(rec, req)
			}

			// Verify we got the expected number of request IDs
			assertion.Equal(tc.numRequests, len(requestIDs))

			// Verify uniqueness if expected
			if tc.expectUnique && tc.numRequests > 1 {
				uniqueIDs := make(map[string]bool)
				for _, id := range requestIDs {
					assertion.False(uniqueIDs[id], "Found duplicate request ID: %s", id)
					uniqueIDs[id] = true
				}
			}

			// Verify all IDs are non-empty and properly formatted
			for _, id := range requestIDs {
				assertion.NotEmpty(id)
				assertion.Equal(36, len(id))
			}
		})
	}
}

// Helper function to extract request ID from context
func getRequestIDFromContext(ctx context.Context) string {
	if requestID, ok := ctx.Value(constants.RequestIDHeader).(string); ok {
		return requestID
	}
	return ""
}

// Benchmark tests
func BenchmarkRequestIDMiddleware(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		getRequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestIDMiddleware()
	wrappedHandler := middleware(handler)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(rec, req)
	}
}
