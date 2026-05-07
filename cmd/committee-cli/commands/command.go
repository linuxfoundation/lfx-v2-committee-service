// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package commands

import (
	"context"
	"log/slog"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
)

// Command represents a top-level CLI command group (e.g. "sync").
type Command interface {
	Name() string
	Help() string
	Subcommands() map[string]Subcommand
}

// Subcommand represents a single runnable operation within a command group.
type Subcommand interface {
	Name() string
	Help() string
	Run(ctx context.Context, rc RunContext) error
}

// RunContext carries the wired infrastructure and global flags into a subcommand.
type RunContext struct {
	CommitteeReader             port.CommitteeReader
	CommitteeWriterOrchestrator service.CommitteeWriter
	DryRun                      bool
	Args                        []string // remaining args after command + subcommand, for subcommand flag parsing
}

// Stats tracks counters for a command run.
type Stats struct {
	Total   int
	Updated int
	Skipped int
	Failed  int
	start   time.Time
}

// NewStats creates a Stats with the start time set to now.
func NewStats() *Stats {
	return &Stats{start: time.Now()}
}

// Log emits the run summary as a structured JSON log line.
func (s *Stats) Log(ctx context.Context, commandName string) {
	duration := time.Since(s.start)
	rate := 0.0
	if duration.Seconds() > 0 {
		rate = float64(s.Total) / duration.Seconds()
	}
	slog.InfoContext(ctx, commandName+" complete",
		"total", s.Total,
		"updated", s.Updated,
		"skipped", s.Skipped,
		"failed", s.Failed,
		"duration_ms", duration.Milliseconds(),
		"rate_per_sec", rate,
	)
}
