// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-api/service"
	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"

	usecaseSvc "github.com/linuxfoundation/lfx-v2-committee-service/internal/service"

	logging "github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"

	"goa.design/clue/debug"
)

const (
	defaultPort = "8080"
	// gracefulShutdownSeconds should be higher than NATS client
	// request timeout, and lower than the pod or liveness probe's
	// terminationGracePeriodSeconds.
	gracefulShutdownSeconds = 25
)

func init() {
	// slog is the standard library logger, we use it to log errors and
	logging.InitStructureLogConfig()
}

func main() {
	// Define command line flags, add any other flag required to configure the
	// service.
	var (
		dbgF = flag.Bool("d", false, "enable debug logging")
		port = flag.String("p", defaultPort, "listen port")
		bind = flag.String("bind", "*", "interface to bind on")
	)
	flag.Usage = func() {
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()

	ctx := context.Background()
	slog.InfoContext(ctx, "Starting query service",
		"bind", *bind,
		"http-port", *port,
		"graceful-shutdown-seconds", gracefulShutdownSeconds,
	)

	// Initialize the repositories based on configuration
	committeeRetriever := service.CommitteeReaderImpl(ctx)
	committeeWriter := service.CommitteeWriterImpl(ctx)
	projectRetriever := service.ProjectRetrieverImpl(ctx)
	committeePublisher := service.CommitteePublisherImpl(ctx)
	authService := service.AuthServiceImpl(ctx)

	// Initialize the service with use case
	createCommitteeUseCase := usecaseSvc.NewcommitteeWriterOrchestrator(
		usecaseSvc.WithCommitteeRetriever(committeeRetriever),
		usecaseSvc.WithCommitteeWriter(committeeWriter),
		usecaseSvc.WithProjectRetriever(projectRetriever),
		usecaseSvc.WithCommitteePublisher(committeePublisher),
	)

	committeeServiceSvc := service.NewCommitteeService(createCommitteeUseCase, authService)

	// Wrap the services in endpoints that can be invoked from other services
	// potentially running in different processes.
	committeeServiceEndpoints := committeeservice.NewEndpoints(committeeServiceSvc)
	committeeServiceEndpoints.Use(debug.LogPayloads())

	// Create channel used by both the signal handler and server goroutines
	// to notify the main goroutine when to stop the server.
	errc := make(chan error)

	// Setup interrupt handler. This optional step configures the process so
	// that SIGINT and SIGTERM signals cause the services to stop gracefully.
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		errc <- fmt.Errorf("%s", <-c)
	}()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)

	// Start the servers and send errors (if any) to the error channel.
	addr := ":" + *port
	if *bind != "*" {
		addr = *bind + ":" + *port
	}

	handleHTTPServer(ctx, addr, committeeServiceEndpoints, &wg, errc, *dbgF)

	// Wait for signal.
	slog.InfoContext(ctx, "received shutdown signal, stopping servers",
		"signal", <-errc,
	)

	// Send cancellation signal to the goroutines.
	cancel()

	// Create a timeout context for graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), gracefulShutdownSeconds*time.Second)
	defer shutdownCancel()

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.InfoContext(ctx, "graceful shutdown completed")
	case <-shutdownCtx.Done():
		slog.WarnContext(ctx, "graceful shutdown timed out")
	}

	slog.InfoContext(ctx, "exited")
}
