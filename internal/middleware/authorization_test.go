// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/stretchr/testify/assert"
)

func TestAuthorizationMiddleware(t *testing.T) {
	tests := []struct {
		name                 string
		authorizationHeader  string
		expectInContext      bool
		expectedContextValue string
	}{
		{
			name:                 "adds bearer token to context",
			authorizationHeader:  "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			expectInContext:      true,
			expectedContextValue: "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:                 "adds basic auth to context",
			authorizationHeader:  "Basic dXNlcjpwYXNzd29yZA==",
			expectInContext:      true,
			expectedContextValue: "Basic dXNlcjpwYXNzd29yZA==",
		},
		{
			name:                 "handles empty authorization header",
			authorizationHeader:  "",
			expectInContext:      true,
			expectedContextValue: "",
		},
		{
			name:                 "handles custom auth scheme",
			authorizationHeader:  "Custom some-custom-token",
			expectInContext:      true,
			expectedContextValue: "Custom some-custom-token",
		},
		{
			name:                 "handles authorization with multiple spaces",
			authorizationHeader:  "Bearer    token-with-spaces",
			expectInContext:      true,
			expectedContextValue: "Bearer    token-with-spaces",
		},
	}

	assertion := assert.New(t)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedContext context.Context
			var capturedAuthorization string

			// Test handler that captures the context and authorization value
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedContext = r.Context()
				capturedAuthorization = getAuthorizationFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			// Wrap handler with Authorization middleware
			middleware := AuthorizationMiddleware()
			wrappedHandler := middleware(handler)

			// Create request
			req := httptest.NewRequest("GET", "/test", nil)
			if tc.authorizationHeader != "" {
				req.Header.Set(constants.AuthorizationHeader, tc.authorizationHeader)
			}

			rec := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(rec, req)

			// Verify authorization was added to context
			if tc.expectInContext {
				assertion.Equal(tc.expectedContextValue, capturedAuthorization)
			}

			// Verify context contains authorization value
			contextAuthorization := getAuthorizationFromContext(capturedContext)
			assertion.Equal(tc.expectedContextValue, contextAuthorization)
		})
	}
}

func TestAuthorizationMiddlewareIntegration(t *testing.T) {
	tests := []struct {
		name           string
		setupRequest   func(*http.Request)
		checkContext   func(*testing.T, context.Context)
		expectedStatus int
	}{
		{
			name: "passes authorization through multiple handlers",
			setupRequest: func(req *http.Request) {
				req.Header.Set(constants.AuthorizationHeader, "Bearer test-token")
			},
			checkContext: func(t *testing.T, ctx context.Context) {
				auth := getAuthorizationFromContext(ctx)
				assert.Equal(t, "Bearer test-token", auth)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "works with request ID middleware",
			setupRequest: func(req *http.Request) {
				req.Header.Set(constants.AuthorizationHeader, "Bearer integration-token")
				req.Header.Set(string(constants.RequestIDHeader), "test-request-id")
			},
			checkContext: func(t *testing.T, ctx context.Context) {
				auth := getAuthorizationFromContext(ctx)
				assert.Equal(t, "Bearer integration-token", auth)

				// Check if request ID is also in context (if both middlewares are used)
				if requestID, ok := ctx.Value(constants.RequestIDHeader).(string); ok {
					assert.Equal(t, "test-request-id", requestID)
				}
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "handles missing authorization header gracefully",
			setupRequest: func(req *http.Request) {
				// Don't set any authorization header
			},
			checkContext: func(t *testing.T, ctx context.Context) {
				auth := getAuthorizationFromContext(ctx)
				assert.Empty(t, auth)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "preserves other headers",
			setupRequest: func(req *http.Request) {
				req.Header.Set(constants.AuthorizationHeader, "Bearer preserve-test")
				req.Header.Set("X-Custom-Header", "custom-value")
				req.Header.Set("Content-Type", "application/json")
			},
			checkContext: func(t *testing.T, ctx context.Context) {
				auth := getAuthorizationFromContext(ctx)
				assert.Equal(t, "Bearer preserve-test", auth)
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedContext context.Context

			// Test handler that captures the context
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedContext = r.Context()

				// Verify other headers are preserved
				if tc.name == "preserves other headers" {
					assert.Equal(t, "custom-value", r.Header.Get("X-Custom-Header"))
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				}

				w.WriteHeader(tc.expectedStatus)
			})

			// Create middleware chain
			authMiddleware := AuthorizationMiddleware()
			requestIDMiddleware := RequestIDMiddleware()

			// Chain middlewares: RequestID -> Authorization -> Handler
			wrappedHandler := authMiddleware(handler)
			if tc.name == "works with request ID middleware" {
				wrappedHandler = requestIDMiddleware(authMiddleware(handler))
			}

			// Create request
			req := httptest.NewRequest("GET", "/test", nil)
			tc.setupRequest(req)

			rec := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(rec, req)

			// Verify response status
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Run context checks
			tc.checkContext(t, capturedContext)
		})
	}
}

func TestAuthorizationMiddlewareConcurrency(t *testing.T) {
	// Test that the middleware handles concurrent requests correctly
	numRequests := 10
	tokens := make([]string, numRequests)
	results := make([]string, numRequests)

	// Generate unique tokens
	for i := 0; i < numRequests; i++ {
		tokens[i] = fmt.Sprintf("Bearer token-%d", i)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate some processing time
		time.Sleep(10 * time.Millisecond)
		auth := getAuthorizationFromContext(r.Context())
		w.Write([]byte(auth)) //nolint:errcheck
	})

	middleware := AuthorizationMiddleware()
	wrappedHandler := middleware(handler)

	// Run concurrent requests
	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set(constants.AuthorizationHeader, tokens[index])

			rec := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(rec, req)

			results[index] = rec.Body.String()
		}(i)
	}

	wg.Wait()

	// Verify each request got its own authorization token
	for i := 0; i < numRequests; i++ {
		assert.Equal(t, tokens[i], results[i])
	}
}

// Helper function to extract authorization from context
func getAuthorizationFromContext(ctx context.Context) string {
	if authorization, ok := ctx.Value(constants.AuthorizationContextID).(string); ok {
		return authorization
	}
	return ""
}
