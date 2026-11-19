// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectPostgres_RequiresAURL(t *testing.T) {
	t.Setenv("POSTGRES_URL", "")
	for _, key := range []string{"PGHOST", "PGUSER", "PGPASSWORD", "PGDATABASE"} {
		t.Setenv(key, "")
	}

	sink, closeSink, err := connectPostgres(context.Background())

	require.ErrorContains(t, err, "POSTGRES_URL is empty")
	require.Nil(t, sink)
	require.Nil(t, closeSink)
}

func TestConnectPostgres_TriesThePGVariables(t *testing.T) {
	t.Setenv("POSTGRES_URL", "")
	t.Setenv("PGHOST", "127.0.0.1")
	t.Setenv("PGPORT", "1")
	t.Setenv("PGUSER", "first-last")
	t.Setenv("PGPASSWORD", "not-a-real-secret")
	t.Setenv("PGDATABASE", "committee")
	t.Setenv("PGSSLMODE", "disable")

	sink, closeSink, err := connectPostgres(context.Background())

	require.Error(t, err)
	require.NotContains(t, err.Error(), "POSTGRES_URL is empty", "the variables were used, not skipped")
	require.NotContains(t, err.Error(), "not-a-real-secret")
	require.Nil(t, sink)
	require.Nil(t, closeSink)
}
