// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	infrastructure "github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/mock"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

type stubReadiness struct {
	err   error
	calls int
}

func (s *stubReadiness) IsReady(context.Context) error {
	s.calls++
	return s.err
}

func TestCommitteeServiceReadyz(t *testing.T) {
	storage := &infrastructure.MockCommitteeReaderWriter{}

	t.Run("ready with no extra checks", func(t *testing.T) {
		svc := &committeeServicesrvc{storage: storage}
		res, err := svc.Readyz(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "OK\n", string(res))
	})

	t.Run("ready when every extra check passes", func(t *testing.T) {
		first, second := &stubReadiness{}, &stubReadiness{}
		svc := &committeeServicesrvc{storage: storage, extraReadiness: []port.ReadinessChecker{first, second}}
		_, err := svc.Readyz(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 1, first.calls)
		assert.Equal(t, 1, second.calls)
	})

	t.Run("not ready when an extra check fails, without leaking its error", func(t *testing.T) {
		failing := &stubReadiness{err: errors.New("dial tcp 10.0.0.1:5432: connection refused")}
		svc := &committeeServicesrvc{storage: storage, extraReadiness: []port.ReadinessChecker{failing}}
		res, err := svc.Readyz(context.Background())
		require.Error(t, err)
		assert.Nil(t, res)

		var unavailable errs.ServiceUnavailable
		require.ErrorAs(t, err, &unavailable)
		assert.NotContains(t, err.Error(), "10.0.0.1")
	})
}
