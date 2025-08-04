// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	committeeservicesvr "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"

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

	// Wrap the endpoints with the transport specific layers. The generated
	// server packages contains code generated from the design which maps
	// the service input and output data structures to HTTP requests and
	// responses.
	var (
		committeeServiceServer *committeeservicesvr.Server
	)
	{
		eh := errorHandler(ctx)
		committeeServiceServer = committeeservicesvr.New(committeeServiceEndpoints, mux, dec, enc, eh, nil, nil)
	}

	// Configure the mux.
	committeeservicesvr.Mount(mux, committeeServiceServer)

	var handler http.Handler = mux
	if dbg {
		// Log query and response bodies if debug logs are enabled.
		handler = debug.HTTP()(handler)
	}

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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := srv.Shutdown(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "failed to shutdown HTTP server", "error", err)
		}
	}()
}

// errorHandler returns a function that writes and logs the given error.
// The function also writes and logs the error unique ID so that it's possible
// to correlate.
func errorHandler(logCtx context.Context) func(context.Context, http.ResponseWriter, error) {
	return func(ctx context.Context, w http.ResponseWriter, err error) {
		slog.ErrorContext(logCtx, "HTTP error occurred", "error", err)
	}
}
