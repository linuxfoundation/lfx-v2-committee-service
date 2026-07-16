// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"goa.design/clue/debug"
	goahttp "goa.design/goa/v3/http"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	committeeservicesvr "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/middleware"
)

// handleHTTPServer starts configures and starts a HTTP server on the given
// URL. It shuts down the server if any error is received in the error channel.
func handleHTTPServer(ctx context.Context, host string, committeeServiceEndpoints *committeeservice.Endpoints, wg *sync.WaitGroup, errc chan error, dbg bool) {

	// Provide the transport specific request decoder and response encoder.
	// The goa http package has built-in support for JSON, XML and gob.
	// Other encodings can be used by providing the corresponding functions,
	// see goa.design/implement/encoding.
	var (
		dec = goahttp.RequestDecoder
		enc = goahttp.ResponseEncoder
	)

	// Build the service HTTP request multiplexer and mount debug and profiler
	// endpoints in debug mode.
	var mux goahttp.MiddlewareMuxer
	{
		mux = goahttp.NewMuxer()
		// Register route-tagging middleware inside chi's routing chain so that
		// http.route is set on the OTel span after chi has matched the route pattern.
		// The span name is also updated here to avoid high-cardinality names from
		// using raw URL paths (which contain actual path parameter values).
		// Must be registered before Mount calls per chi convention.
		// Reads RoutePattern after next.ServeHTTP because chi populates the pattern
		// during routing (inside ServeHTTP), not before.
		mux.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() {
					rctx := chi.RouteContext(r.Context())
					if rctx != nil {
						routePattern := rctx.RoutePattern()
						if routePattern != "" {
							if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
								labeler.Add(semconv.HTTPRoute(routePattern))
							}
							span := trace.SpanFromContext(r.Context())
							span.SetAttributes(semconv.HTTPRoute(routePattern))
							span.SetName(r.Method + " " + routePattern)
						}
					}
				}()
				next.ServeHTTP(w, r)
			})
		})
		if dbg {
			// Mount pprof handlers for memory profiling under /debug/pprof.
			debug.MountPprofHandlers(debug.Adapt(mux))
			// Mount /debug endpoint to enable or disable debug logs at runtime.
			debug.MountDebugLogEnabler(debug.Adapt(mux))
		}
	}

	koDataPath := os.Getenv("KO_DATA_PATH")
	if koDataPath == "" {
		koDataPath = "../../gen/http/"
	}

	koDataDir := http.Dir(koDataPath)

	// Wrap the endpoints with the transport specific layers. The generated
	// server packages contains code generated from the design which maps
	// the service input and output data structures to HTTP requests and
	// responses.
	var (
		committeeServiceServer *committeeservicesvr.Server
	)
	{
		eh := errorHandler(ctx)
		committeeServiceServer = committeeservicesvr.New(
			committeeServiceEndpoints,
			mux, dec, enc, eh, nil,
			uploadCommitteeDocumentDecoder,
			koDataDir, koDataDir, koDataDir, koDataDir,
		)
	}

	// Configure the mux.
	committeeservicesvr.Mount(mux, committeeServiceServer)

	var handler http.Handler = mux

	// Accept-invite must allow an empty POST body for backward compatibility with
	// decline-invite clients. Goa decoders treat an empty body as EOF; older generated
	// code mapped that to missing_payload before optional-body support landed.
	handler = acceptInviteEmptyBodyMiddleware()(handler)

	// Add RequestID middleware first
	handler = middleware.RequestIDMiddleware()(handler)
	// Add Authorization middleware
	handler = middleware.AuthorizationMiddleware()(handler)
	if dbg {
		// Log query and response bodies if debug logs are enabled.
		handler = debug.HTTP()(handler)
	}
	// Wrap the handler with OpenTelemetry instrumentation
	handler = otelhttp.NewHandler(handler, "committee-service",
		otelhttp.WithFilter(func(r *http.Request) bool {
			p := r.URL.Path
			return p != "/healthz" && p != "/livez" && p != "/readyz"
		}),
	)

	// Start HTTP server using default configuration, change the code to
	// configure the server as required by your service.
	srv := &http.Server{Addr: host, Handler: handler, ReadHeaderTimeout: time.Second * 60}
	for _, m := range committeeServiceServer.Mounts {
		slog.InfoContext(ctx, "HTTP endpoint mounted",
			"method", m.Method,
			"verb", m.Verb,
			"pattern", m.Pattern,
		)
	}

	(*wg).Add(1)
	go func() {
		defer (*wg).Done()

		// Start HTTP server in a separate goroutine.
		go func() {
			slog.InfoContext(ctx, "HTTP server listening", "host", host)
			errc <- srv.ListenAndServe()
		}()

		<-ctx.Done()
		slog.InfoContext(ctx, "shutting down HTTP server", "host", host)

		// Shutdown gracefully with a 30s timeout.
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(gracefulShutdownSeconds-5)*time.Second)
		defer cancel()

		err := srv.Shutdown(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "failed to shutdown HTTP server", "error", err)
		}
	}()
}

const acceptInviteMaxBodyBytes = 1 << 20 // 1MB

// acceptInviteEmptyBodyMiddleware replaces a missing accept-invite POST body with "{}".
// Clients may omit the body entirely (same as decline-invite); without this, older
// Goa-generated decoders return missing_payload on io.EOF.
func acceptInviteEmptyBodyMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && isCommitteeInviteAcceptPath(r.URL.Path) {
				limitedBody := http.MaxBytesReader(w, r.Body, acceptInviteMaxBodyBytes)
				bodyBytes, err := io.ReadAll(limitedBody)
				_ = r.Body.Close()
				if err != nil {
					if strings.Contains(err.Error(), "request body too large") {
						http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
						return
					}
					http.Error(w, "failed to read request body", http.StatusBadRequest)
					return
				}
				if len(bytes.TrimSpace(bodyBytes)) == 0 {
					bodyBytes = []byte("{}")
				}
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				r.ContentLength = int64(len(bodyBytes))
				if r.Header.Get("Content-Type") == "" {
					r.Header.Set("Content-Type", "application/json")
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isCommitteeInviteAcceptPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 5 && parts[0] == "committees" && parts[2] == "invites" && parts[4] == "accept"
}

// uploadCommitteeDocumentDecoder is the multipart decoder for the
// upload-committee-document endpoint. It reads form parts and populates
// the payload's File, FileName, ContentType, Name, and Description fields.
func uploadCommitteeDocumentDecoder(mr *multipart.Reader, p **committeeservice.UploadCommitteeDocumentPayload) error {
	if *p == nil {
		*p = &committeeservice.UploadCommitteeDocumentPayload{}
	}
	payload := *p
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(part, model.MaxDocumentFileSize+1))
		if err != nil {
			_ = part.Close()
			return err
		}
		switch part.FormName() {
		case "name":
			payload.Name = string(data)
		case "description":
			desc := string(data)
			payload.Description = &desc
		case "folder_uid":
			if folderUID := strings.TrimSpace(string(data)); folderUID != "" {
				payload.FolderUID = &folderUID
			}
		case "file":
			if int64(len(data)) > model.MaxDocumentFileSize {
				_ = part.Close()
				return fmt.Errorf("file size exceeds maximum allowed size of %d bytes", model.MaxDocumentFileSize)
			}
			fileName := part.FileName()
			payload.FileName = fileName
			ct := part.Header.Get("Content-Type")
			// Normalize: strip parameters (e.g. "text/plain; charset=utf-8" → "text/plain")
			// so the allowlist check in the service layer matches bare MIME types.
			if mediaType, _, err := mime.ParseMediaType(ct); err == nil {
				ct = mediaType
			}
			payload.ContentType = ct
			payload.File = data
		}
		_ = part.Close()
	}
	// Validate DSL constraints (MaxLength on name/description)
	// that the custom multipart decoder bypasses.
	requestBody := &committeeservicesvr.UploadCommitteeDocumentRequestBody{
		Name:        &payload.Name,
		Description: payload.Description,
		FolderUID:   payload.FolderUID,
		FileName:    &payload.FileName,
		ContentType: &payload.ContentType,
		File:        payload.File,
	}
	if err := committeeservicesvr.ValidateUploadCommitteeDocumentRequestBody(requestBody); err != nil {
		return err
	}
	return nil
}

// errorHandler returns a function that writes and logs the given error.
// The function also writes and logs the error unique ID so that it's possible
// to correlate.
func errorHandler(logCtx context.Context) func(context.Context, http.ResponseWriter, error) {
	return func(ctx context.Context, w http.ResponseWriter, err error) {
		slog.ErrorContext(logCtx, "HTTP error occurred", "error", err)
	}
}
