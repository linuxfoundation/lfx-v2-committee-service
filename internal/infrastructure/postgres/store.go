// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// revisionKey names a revision carried in the context, see WithRevision.
type revisionKey int

const (
	primaryRevision  revisionKey = iota // the record a write targets
	settingsRevision                    // the settings row written along with a committee
)

// WithRevision makes the next write through the returned context store revision as the record's
// revision, instead of taking one from the sequence. The composite store uses it in dual mode to carry
// the KV revision into the row. The update and delete guards still compare against the revision the
// caller read, so it only decides the revision the write leaves behind. It applies to creates and
// updates of any record, and to the committee row of a committee create. A revision of 0, or one above
// the range of the column, is an error.
func WithRevision(ctx context.Context, revision uint64) (context.Context, error) {
	return withRevision(ctx, primaryRevision, revision)
}

// WithSettingsRevision is WithRevision for the settings row a committee create writes along with the
// committee, which has a revision of its own.
func WithSettingsRevision(ctx context.Context, revision uint64) (context.Context, error) {
	return withRevision(ctx, settingsRevision, revision)
}

func withRevision(ctx context.Context, key revisionKey, revision uint64) (context.Context, error) {
	if revision == 0 || revision > math.MaxInt64 {
		return ctx, errs.NewValidation("revision must be between 1 and the largest BIGINT")
	}
	return context.WithValue(ctx, key, int64(revision)), nil
}

// explicitRevision is the revision the context carries for key, or nil, which the statements
// replace with the next value of the sequence.
func explicitRevision(ctx context.Context, key revisionKey) any {
	if revision, ok := ctx.Value(key).(int64); ok {
		return revision
	}
	return nil
}

// withRevisionArg appends the explicit revision, the last parameter of every insert and update.
func withRevisionArg(ctx context.Context, key revisionKey, args []any) []any {
	return append(args, explicitRevision(ctx, key))
}

// revisionParam is the SQL for the revision a write leaves behind: the value of parameter n when the
// caller gave one, the next value of the sequence otherwise.
func revisionParam(n int) string {
	return fmt.Sprintf("COALESCE($%d::bigint, nextval('committee_revision_seq'))", n)
}

// insertStatement is the INSERT for a table: its columns, then the revision as the last parameter.
func insertStatement(table string, columns []string) string {
	return "INSERT INTO " + table + " (" + columnList(columns) + ", revision) VALUES (" +
		placeholders(len(columns)) + ", " + revisionParam(len(columns)+1) + ")"
}

// updateAssignments is assignments followed by the revision assignment, whose parameter comes after the columns.
func updateAssignments(columns []string, first int) string {
	return assignments(columns, first) + ", revision = " + revisionParam(first+len(columns))
}

// AdvanceRevisionSequence raises the revision sequence so that the next revision it issues is above
// atLeast, and never lowers it. At the flip from KV to PostgreSQL, call it with the highest KV
// revision so a token issued before the flip can never equal a revision issued after it. Call it
// while no writes are in flight: the read and the set are not one atomic step.
func (s *Store) AdvanceRevisionSequence(ctx context.Context, atLeast uint64) error {
	if atLeast > math.MaxInt64 {
		return errs.NewValidation("revision must not exceed the largest BIGINT")
	}
	if _, err := s.pool.Exec(ctx,
		"SELECT setval('committee_revision_seq', GREATEST($1::bigint, (SELECT last_value FROM committee_revision_seq)))", int64(atLeast)); err != nil {
		return unexpected("failed to advance the revision sequence", err)
	}
	return nil
}

// revisionArg converts a revision from the API (unsigned) to the BIGINT the column holds. A value
// above the BIGINT range cannot be any row's revision, so it becomes -1, which the sequence never
// issues: the guarded write then matches no row and is reported as not found or stale like any
// other revision that is not current, with no wrap-around to a value that could match.
func revisionArg(revision uint64) int64 {
	if revision > math.MaxInt64 {
		return -1
	}
	return int64(revision)
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
