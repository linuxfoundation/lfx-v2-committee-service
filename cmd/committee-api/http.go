// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"sync"
	"time"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	committeeservicesvr "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/middleware"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"goa.design/clue/debug"
	goahttp "goa.design/goa/v3/http"
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
	var mux goahttp.Muxer
	{
		mux = goahttp.NewMuxer()
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

	// Add RequestID middleware first
	handler = middleware.RequestIDMiddleware()(handler)
	// Add Authorization middleware
	handler = middleware.AuthorizationMiddleware()(handler)
	if dbg {
		// Log query and response bodies if debug logs are enabled.
		handler = debug.HTTP()(handler)
	}
	// Wrap the handler with OpenTelemetry instrumentation
	handler = otelhttp.NewHandler(handler, "committee-service")

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
			return err
		}
		switch part.FormName() {
		case "name":
			payload.Name = string(data)
		case "description":
			desc := string(data)
			payload.Description = &desc
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
