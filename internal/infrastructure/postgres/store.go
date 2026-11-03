// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// pgUniqueViolation is the SQLSTATE PostgreSQL reports for a unique constraint or index violation.
const pgUniqueViolation = "23505"

// Store is the PostgreSQL implementation of the committee storage ports. The unique constraints and
// indexes in the schema decide what is a duplicate; the store only translates the violation into the
// typed errors from pkg/errors, so the HTTP error mapping stays the same as with the KV store.
//
// Every record carries a revision, the opaque optimistic-concurrency token the API exposes. It is
// assigned from the shared sequence on insert and replaced on every update, so a token that was read
// before another write never matches again.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store that uses the pool. The schema must already be migrated.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// IsReady reports whether PostgreSQL answers a ping.
func (s *Store) IsReady(ctx context.Context) error {
	return NewReady(s.pool).IsReady(ctx)
}

// uniqueViolation returns the name of the violated constraint or index when err is a unique violation.
func uniqueViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return pgErr.ConstraintName, true
	}
	return "", false
}

// withTx runs fn in a transaction and commits it, or rolls it back when fn returns an error, which
// is returned unchanged. The rollback uses the caller's context, so a cancelled request still releases the connection.
func (s *Store) withTx(ctx context.Context, fn func(tx pgx.Tx) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unexpected("failed to start a transaction", err)
	}
	// Rolling back after a commit is a no-op, so this also releases the connection if fn panics.
	defer func() { _ = tx.Rollback(ctx) }()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return unexpected("failed to commit the transaction", err)
	}
	return nil
}

// columnList returns the columns as a comma separated list.
func columnList(columns []string) string {
	return strings.Join(columns, ", ")
}

// placeholders returns "$1, $2, ..., $n".
func placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}

// assignments returns "col1 = $2, col2 = $3, ..." for the columns, numbering the parameters from
// first.
func assignments(columns []string, first int) string {
	parts := make([]string, len(columns))
	for i, column := range columns {
		parts[i] = fmt.Sprintf("%s = $%d", column, first+i)
	}
	return strings.Join(parts, ", ")
}

// jsonArray encodes a list for a NOT NULL JSONB column. A nil list is stored as an empty array, not
// the JSON null that json.Marshal produces.
func jsonArray[T any](list []T) ([]byte, error) {
	if len(list) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(list)
}

// jsonObject encodes an optional value for a nullable JSONB column: nil is SQL NULL.
func jsonObject[T any](v *T) (any, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// jsonList encodes a list so that nil and empty stay different: nil is stored as the JSON value
// null and an empty list as []. Use it for lists the KV record keeps without omitempty, where the
// service tells "not set" from "set to empty".
func jsonList[T any](list []T) ([]byte, error) {
	return json.Marshal(list)
}

// decodeList reads a list written by jsonList, keeping nil and empty apart.
func decodeList[T any](raw []byte) ([]T, error) {
	var list []T
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// decodeArray reads a JSONB array. An empty array decodes to nil, which is what the KV store
// returns for a list its records omit when empty.
func decodeArray[T any](raw []byte) ([]T, error) {
	var list []T
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}

// decodeObject reads a nullable JSONB value; SQL NULL (no bytes) is nil.
func decodeObject[T any](raw []byte) (*T, error) {
	if raw == nil {
		return nil, nil
	}
	v := new(T)
	if err := json.Unmarshal(raw, v); err != nil {
		return nil, err
	}
	return v, nil
}

// unexpected wraps a database failure as the service's internal error. The cause stays on the error
// for logs. Connection errors hold no credentials because the pool redacts them, but a constraint
// violation from the driver can carry the failing row (pgconn.PgError.Detail), which for settings
// includes the webhook URL: log the error's text, never the PgError value itself.
func unexpected(message string, err error) error {
	return errs.NewUnexpected(message, err)
}
