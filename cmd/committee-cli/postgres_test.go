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

	sink, closeSink, err := connectPostgres(context.Background())

	require.ErrorContains(t, err, "POSTGRES_URL is empty")
	require.Nil(t, sink)
	require.Nil(t, closeSink)
}
