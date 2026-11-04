// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"testing"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/storetest"
)

// The PostgreSQL store runs the same behavioral suites as the NATS KV store (see
// internal/infrastructure/nats/contract_test.go).
func TestContract_Committee(t *testing.T) {
	storetest.RunCommitteeContract(t, storetest.Backend{Name: "postgres"}, func(t *testing.T) port.CommitteeReaderWriter {
		return newTestStore(t)
	})
}
