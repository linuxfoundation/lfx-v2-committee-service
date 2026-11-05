// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// meterName is the name of the meter the composite store's instruments belong to.
const meterName = "github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/composite"

// Metrics counts what the stores that run side by side do that no request sees: a write the secondary store
// refused, and a read of the two stores that disagreed. It never fails and never blocks: a method on a nil *Metrics
// only logs, and an instrument that could not be created is a no-op, so counting can never take a request down.
type Metrics struct {
	secondaryFailures metric.Int64Counter
	mismatches        metric.Int64Counter
	effectiveMode     metric.Int64Gauge
}

// NewMetrics creates the instruments on the global meter provider, which is the one the service sets up at startup.
func NewMetrics() *Metrics {
	return newMetrics(otel.Meter(meterName))
}

func newMetrics(meter metric.Meter) *Metrics {
	m := &Metrics{}
	fallback := noop.NewMeterProvider().Meter(meterName)
	var err error
	if m.secondaryFailures, err = meter.Int64Counter("committee.storage.secondary_write_failures",
		metric.WithDescription("Writes the secondary store refused while the primary store succeeded"),
		metric.WithUnit("{failure}")); err != nil {
		slog.Warn("failed to create the secondary write failure counter", "error", err)
		m.secondaryFailures, _ = fallback.Int64Counter("committee.storage.secondary_write_failures")
	}
	if m.mismatches, err = meter.Int64Counter("committee.storage.read_compare_mismatches",
		metric.WithDescription("Sampled reads whose result differed between the two stores"),
		metric.WithUnit("{mismatch}")); err != nil {
		slog.Warn("failed to create the read compare mismatch counter", "error", err)
		m.mismatches, _ = fallback.Int64Counter("committee.storage.read_compare_mismatches")
	}
	if m.effectiveMode, err = meter.Int64Gauge("committee.storage.mode",
		metric.WithDescription("The storage mode the process runs in: 1 for the mode named by the mode attribute")); err != nil {
		slog.Warn("failed to create the storage mode gauge", "error", err)
		m.effectiveMode, _ = fallback.Int64Gauge("committee.storage.mode")
	}
	return m
}

// RecordMode logs the mode the process runs in, so it can be read from the pod logs, and records it.
func (m *Metrics) RecordMode(ctx context.Context, mode Mode) {
	slog.InfoContext(ctx, "storage mode", "mode", string(mode))
	if m == nil {
		return
	}
	m.effectiveMode.Record(ctx, 1, metric.WithAttributes(attribute.String("mode", string(mode))))
}

// SecondaryWriteFailed counts and logs a write the secondary store refused. The caller goes on: the primary store's
// write decided the result, and what the secondary store is missing is left to be reconciled. store names the store
// that failed and operation the write, for example "create_member".
func (m *Metrics) SecondaryWriteFailed(ctx context.Context, mode Mode, store, operation string, err error) {
	slog.WarnContext(ctx, "secondary store write failed; the primary store's result stands and the secondary is left to reconcile",
		"mode", string(mode), "store", store, "operation", operation, "error", err)
	if m == nil {
		return
	}
	m.secondaryFailures.Add(ctx, 1, metric.WithAttributes(
		attribute.String("mode", string(mode)), attribute.String("store", store), attribute.String("operation", operation)))
}

// ReadMismatch counts and logs a sampled read whose result differed between the two stores, which is the one the
// caller returns the primary store's side of. operation names the read, for example "get_committee".
func (m *Metrics) ReadMismatch(ctx context.Context, mode Mode, operation string) {
	slog.WarnContext(ctx, "the two stores returned different results for a read", "mode", string(mode), "operation", operation)
	if m == nil {
		return
	}
	m.mismatches.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", string(mode)), attribute.String("operation", operation)))
}
