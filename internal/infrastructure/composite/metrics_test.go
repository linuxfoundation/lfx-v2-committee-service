// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collect returns the metrics the reader holds, by name.
func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	out := map[string]metricdata.Metrics{}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

func TestMetrics(t *testing.T) {
	ctx := context.Background()
	reader := sdkmetric.NewManualReader()
	m := newMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test"))

	m.RecordMode(ctx, ModeDual)
	m.SecondaryWriteFailed(ctx, ModeDual, "postgres", "create_member", "uid-1", errors.New("connection reset"))
	m.SecondaryWriteFailed(ctx, ModeDual, "postgres", "create_member", "uid-1", errors.New("connection reset"))
	m.SecondaryWriteFailed(ctx, ModeDualPG, "kv", "update_base", "uid-1", errors.New("timeout"))
	m.ReadMismatch(ctx, ModeDual, "get_committee")
	m.ReadCompareFailed(ctx, ModeDual, "get_committee", errors.New("x"))

	got := collect(t, reader)

	failures, ok := got["committee.storage.secondary_write_failures"].Data.(metricdata.Sum[int64])
	require.True(t, ok)
	counts := map[string]int64{}
	for _, point := range failures.DataPoints {
		mode, _ := point.Attributes.Value("mode")
		store, _ := point.Attributes.Value("store")
		operation, _ := point.Attributes.Value("operation")
		counts[mode.AsString()+"/"+store.AsString()+"/"+operation.AsString()] = point.Value
	}
	assert.Equal(t, map[string]int64{"dual/postgres/create_member": 2, "dual-pg/kv/update_base": 1}, counts)

	mismatches, ok := got["committee.storage.read_compare_mismatches"].Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, mismatches.DataPoints, 1)
	assert.EqualValues(t, 1, mismatches.DataPoints[0].Value)

	compareErrors, ok := got["committee.storage.read_compare_errors"].Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, compareErrors.DataPoints, 1)
	assert.EqualValues(t, 1, compareErrors.DataPoints[0].Value)

	mode, ok := got["committee.storage.mode"].Data.(metricdata.Gauge[int64])
	require.True(t, ok)
	require.Len(t, mode.DataPoints, 1)
	assert.EqualValues(t, 1, mode.DataPoints[0].Value)
	value, _ := mode.DataPoints[0].Attributes.Value(attribute.Key("mode"))
	assert.Equal(t, "dual", value.AsString())
}

func TestMetrics_NeverFailAndNeverBlock(t *testing.T) {
	ctx := context.Background()

	t.Run("a nil Metrics still logs and does nothing else", func(t *testing.T) {
		var m *Metrics
		assert.NotPanics(t, func() {
			m.RecordMode(ctx, ModeKV)
			m.SecondaryWriteFailed(ctx, ModeDual, "postgres", "create_member", "uid-1", errors.New("x"))
			m.ReadMismatch(ctx, ModeDual, "get_committee")
			m.ReadCompareFailed(ctx, ModeDual, "get_committee", errors.New("x"))
		})
	})

	t.Run("the default metrics work with no meter provider set", func(t *testing.T) {
		m := NewMetrics()
		assert.NotPanics(t, func() {
			m.RecordMode(ctx, ModeKV)
			m.SecondaryWriteFailed(ctx, ModeDual, "postgres", "create_member", "uid-1", errors.New("x"))
			m.ReadMismatch(ctx, ModeDual, "get_committee")
			m.ReadCompareFailed(ctx, ModeDual, "get_committee", errors.New("x"))
		})
	})

	t.Run("an instrument that cannot be created is a no-op and not a failure", func(t *testing.T) {
		m := newMetrics(failingMeter{noop.NewMeterProvider().Meter("test")})
		assert.NotPanics(t, func() {
			m.RecordMode(ctx, ModeDual)
			m.SecondaryWriteFailed(ctx, ModeDual, "postgres", "create_member", "uid-1", errors.New("x"))
			m.ReadMismatch(ctx, ModeDual, "get_committee")
			m.ReadCompareFailed(ctx, ModeDual, "get_committee", errors.New("x"))
		})
	})
}

// failingMeter is a meter that cannot create any instrument.
type failingMeter struct{ metric.Meter }

func (failingMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("no counters")
}

func (failingMeter) Int64Gauge(string, ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	return nil, errors.New("no gauges")
}
