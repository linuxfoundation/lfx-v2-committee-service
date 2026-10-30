// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3/lock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envTestDatabaseURL names a database the tests may modify freely, for example
// postgres://postgres:test@127.0.0.1:5432/committee_test?sslmode=disable. Every test works in its own
// schema inside it and drops the schema afterwards, so tests do not see each other's tables.
const envTestDatabaseURL = "TEST_DATABASE_URL"

// committeeTables lists every table the migrations create.
var committeeTables = []string{
	"committees",
	"committee_settings",
	"committee_members",
	"committee_invites",
	"committee_applications",
	"committee_link_folders",
	"committee_links",
	"committee_documents",
	"group_weekly_briefs",
	"group_weekly_brief_throttle",
}

// testDSN returns the database URL, or skips the test when none is configured. On CI the database
// is promised, so a missing one fails instead: a harness that silently skips everywhere proves nothing.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(envTestDatabaseURL))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s must be set on CI so the migrations are tested against a real PostgreSQL", envTestDatabaseURL)
		}
		t.Skipf("%s is not set; skipping the live PostgreSQL tests", envTestDatabaseURL)
	}
	return dsn
}

// openSchemaDB opens a connection pool whose search_path is the given schema.
func openSchemaDB(t *testing.T, dsn, schema string) *sql.DB {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newSchemaDB creates an empty schema for the test and returns a connection pool bound to it.
func newSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testDSN(t)

	suffix := make([]byte, 6)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	schema := "t_" + hex.EncodeToString(suffix)

	ctx := context.Background()
	admin := openSchemaDB(t, dsn, "public")
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	return openSchemaDB(t, dsn, schema)
}

// migratedDB returns a schema with every migration applied.
func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newSchemaDB(t)
	require.NoError(t, Migrate(context.Background(), db))
	return db
}

// tablesIn returns the tables in the current schema, ignoring goose's own version table.
func tablesIn(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name <> 'goose_db_version'
		ORDER BY table_name`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err())
	return tables
}

func TestMigrate_AppliesToAnEmptyDatabase(t *testing.T) {
	db := newSchemaDB(t)

	require.NoError(t, Migrate(context.Background(), db))

	assert.ElementsMatch(t, committeeTables, tablesIn(t, db))
}

func TestMigrate_IsIdempotent(t *testing.T) {
	db := migratedDB(t)
	provider, err := newProvider(db)
	require.NoError(t, err)
	before, err := provider.GetDBVersion(context.Background())
	require.NoError(t, err)

	require.NoError(t, Migrate(context.Background(), db), "a second run must find nothing to do")

	after, err := provider.GetDBVersion(context.Background())
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Positive(t, after)
}

func TestMigrate_DownRemovesEverything(t *testing.T) {
	db := migratedDB(t)
	provider, err := newProvider(db)
	require.NoError(t, err)

	_, err = provider.DownTo(context.Background(), 0)
	require.NoError(t, err)

	assert.Empty(t, tablesIn(t, db))
	var sequences int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM information_schema.sequences WHERE sequence_schema = current_schema()`).Scan(&sequences))
	assert.Zero(t, sequences, "the revision sequence must go with the tables")

	require.NoError(t, Migrate(context.Background(), db), "the schema must be re-creatable after a down")
	assert.ElementsMatch(t, committeeTables, tablesIn(t, db))
}

func TestMigrate_WaitsForTheMigrationLock(t *testing.T) {
	db := newSchemaDB(t)
	ctx := context.Background()

	// Another process holds goose's session lock, as a pod in the middle of migrating would.
	locker, err := lock.NewPostgresSessionLocker()
	require.NoError(t, err)
	holder, err := db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Close() }()
	require.NoError(t, locker.SessionLock(ctx, holder))

	done := make(chan error, 1)
	go func() { done <- Migrate(ctx, db) }()

	select {
	case err := <-done:
		t.Fatalf("Migrate returned while the lock was held: %v", err)
	case <-time.After(time.Second):
	}
	assert.Empty(t, tablesIn(t, db), "nothing may be applied while another process holds the lock")

	require.NoError(t, locker.SessionUnlock(ctx, holder))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("Migrate did not finish after the lock was released")
	}
	assert.ElementsMatch(t, committeeTables, tablesIn(t, db))
}

// TestMigrate_ConcurrentRunnersAllSucceed runs several runners at once, each with its own pool as
// separate pods would. The lock itself is covered by TestMigrate_WaitsForTheMigrationLock.
func TestMigrate_ConcurrentRunnersAllSucceed(t *testing.T) {
	dsn := testDSN(t)
	db := newSchemaDB(t)
	schema := currentSchema(t, db)

	// Each runner has its own connection pool, as separate pods would.
	const runners = 5
	errs := make([]error, runners)
	var wg sync.WaitGroup
	for i := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runnerDB := openSchemaDB(t, dsn, schema)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			errs[i] = Migrate(ctx, runnerDB)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "runner %d", i)
	}
	assert.ElementsMatch(t, committeeTables, tablesIn(t, db))
}

func currentSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	var schema string
	require.NoError(t, db.QueryRow(`SELECT current_schema()`).Scan(&schema))
	return schema
}

// uniqueViolation returns the violated constraint or index name when err is a unique violation.
func uniqueViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

func TestSchema_UniqueIndexesReproduceTheKVLookupKeys(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	cases := []struct {
		name       string
		first      string
		duplicate  string
		constraint string
		allowed    []string // statements that must be accepted alongside the first
	}{
		{
			name:       "a committee name is unique per project",
			first:      `INSERT INTO committees (uid, project_uid, name, created_at, updated_at) VALUES ('c-1', 'p-1', 'Technical Steering', now(), now())`,
			duplicate:  `INSERT INTO committees (uid, project_uid, name, created_at, updated_at) VALUES ('c-2', 'p-1', 'Technical Steering', now(), now())`,
			constraint: "committees_project_name_key",
			allowed: []string{
				// The same name in another project, and a different case, are both distinct in KV.
				`INSERT INTO committees (uid, project_uid, name, created_at, updated_at) VALUES ('c-3', 'p-2', 'Technical Steering', now(), now())`,
				`INSERT INTO committees (uid, project_uid, name, created_at, updated_at) VALUES ('c-4', 'p-1', 'technical steering', now(), now())`,
			},
		},
		{
			name:       "an SSO group name is unique, and an empty one is not",
			first:      `INSERT INTO committees (uid, project_uid, name, sso_group_name, created_at, updated_at) VALUES ('c-1', 'p-1', 'One', 'proj-one', now(), now())`,
			duplicate:  `INSERT INTO committees (uid, project_uid, name, sso_group_name, created_at, updated_at) VALUES ('c-2', 'p-2', 'Two', 'proj-one', now(), now())`,
			constraint: "committees_sso_group_name_key",
			allowed: []string{
				`INSERT INTO committees (uid, project_uid, name, created_at, updated_at) VALUES ('c-3', 'p-3', 'Three', now(), now())`,
				`INSERT INTO committees (uid, project_uid, name, created_at, updated_at) VALUES ('c-4', 'p-4', 'Four', now(), now())`,
			},
		},
		{
			name:       "a member email is unique per committee, ignoring case and surrounding space",
			first:      `INSERT INTO committee_members (uid, committee_uid, email, created_at, updated_at) VALUES ('m-1', 'c-1', 'first.last@example.com', now(), now())`,
			duplicate:  `INSERT INTO committee_members (uid, committee_uid, email, created_at, updated_at) VALUES ('m-2', 'c-1', ' First.Last@Example.com ', now(), now())`,
			constraint: "committee_members_committee_email_key",
			allowed: []string{
				`INSERT INTO committee_members (uid, committee_uid, email, created_at, updated_at) VALUES ('m-3', 'c-2', 'first.last@example.com', now(), now())`,
			},
		},
		{
			name:       "an invite is unique per committee and email",
			first:      `INSERT INTO committee_invites (uid, committee_uid, invitee_email, created_at, updated_at) VALUES ('i-1', 'c-1', 'first.last@example.com', now(), now())`,
			duplicate:  `INSERT INTO committee_invites (uid, committee_uid, invitee_email, created_at, updated_at) VALUES ('i-2', 'c-1', 'FIRST.LAST@example.com', now(), now())`,
			constraint: "committee_invites_committee_email_key",
			allowed: []string{
				`INSERT INTO committee_invites (uid, committee_uid, invitee_email, created_at, updated_at) VALUES ('i-3', 'c-2', 'first.last@example.com', now(), now())`,
			},
		},
		{
			name:       "an application is unique per committee and email",
			first:      `INSERT INTO committee_applications (uid, committee_uid, applicant_email, created_at, updated_at) VALUES ('a-1', 'c-1', 'first.last@example.com', now(), now())`,
			duplicate:  `INSERT INTO committee_applications (uid, committee_uid, applicant_email, created_at, updated_at) VALUES ('a-2', 'c-1', 'first.last@example.com ', now(), now())`,
			constraint: "committee_applications_committee_email_key",
			allowed: []string{
				`INSERT INTO committee_applications (uid, committee_uid, applicant_email, created_at, updated_at) VALUES ('a-3', 'c-2', 'first.last@example.com', now(), now())`,
			},
		},
		{
			name:       "a folder name is unique per committee and case-sensitive",
			first:      `INSERT INTO committee_link_folders (uid, committee_uid, name, created_at, updated_at) VALUES ('f-1', 'c-1', 'Docs', now(), now())`,
			duplicate:  `INSERT INTO committee_link_folders (uid, committee_uid, name, created_at, updated_at) VALUES ('f-2', 'c-1', 'Docs', now(), now())`,
			constraint: "committee_link_folders_committee_name_key",
			allowed: []string{
				`INSERT INTO committee_link_folders (uid, committee_uid, name, created_at, updated_at) VALUES ('f-3', 'c-1', 'docs', now(), now())`,
				`INSERT INTO committee_link_folders (uid, committee_uid, name, created_at, updated_at) VALUES ('f-4', 'c-2', 'Docs', now(), now())`,
			},
		},
		{
			name:       "a document name is unique per committee and case-sensitive",
			first:      `INSERT INTO committee_documents (uid, committee_uid, name, file_name, created_at, updated_at) VALUES ('d-1', 'c-1', 'Charter', 'charter.pdf', now(), now())`,
			duplicate:  `INSERT INTO committee_documents (uid, committee_uid, name, file_name, created_at, updated_at) VALUES ('d-2', 'c-1', 'Charter', 'other.pdf', now(), now())`,
			constraint: "committee_documents_committee_name_key",
			allowed: []string{
				`INSERT INTO committee_documents (uid, committee_uid, name, file_name, created_at, updated_at) VALUES ('d-3', 'c-1', 'charter', 'charter.pdf', now(), now())`,
				`INSERT INTO committee_documents (uid, committee_uid, name, file_name, created_at, updated_at) VALUES ('d-4', 'c-2', 'Charter', 'charter.pdf', now(), now())`,
			},
		},
		{
			// KV keys the brief index by the UTC date of the window start, so two windows starting on
			// the same UTC day collide even when their timestamps differ.
			name:       "a weekly brief is unique per committee and UTC window day",
			first:      `INSERT INTO group_weekly_briefs (uid, committee_uid, window_start, window_end, state, created_at, updated_at) VALUES ('b-1', 'c-1', '2026-10-26 00:00:00+00', '2026-11-02 00:00:00+00', 'ready', now(), now())`,
			duplicate:  `INSERT INTO group_weekly_briefs (uid, committee_uid, window_start, window_end, state, created_at, updated_at) VALUES ('b-2', 'c-1', '2026-10-26 13:30:00+00', '2026-11-02 13:30:00+00', 'ready', now(), now())`,
			constraint: "group_weekly_briefs_committee_window_key",
			allowed: []string{
				`INSERT INTO group_weekly_briefs (uid, committee_uid, window_start, window_end, state, created_at, updated_at) VALUES ('b-3', 'c-2', '2026-10-26 00:00:00+00', '2026-11-02 00:00:00+00', 'ready', now(), now())`,
				`INSERT INTO group_weekly_briefs (uid, committee_uid, window_start, window_end, state, created_at, updated_at) VALUES ('b-4', 'c-1', '2026-11-02 00:00:00+00', '2026-11-09 00:00:00+00', 'ready', now(), now())`,
			},
		},
		{
			name:       "a throttle row is unique per committee and UTC window day",
			first:      `INSERT INTO group_weekly_brief_throttle (committee_uid, window_start, window_end, window_resets_at) VALUES ('c-1', '2026-10-26 00:00:00+00', '2026-11-02 00:00:00+00', '2026-11-02 00:00:00+00')`,
			duplicate:  `INSERT INTO group_weekly_brief_throttle (committee_uid, window_start, window_end, window_resets_at) VALUES ('c-1', '2026-10-26 08:00:00+00', '2026-11-02 08:00:00+00', '2026-11-02 08:00:00+00')`,
			constraint: "group_weekly_brief_throttle_committee_window_key",
			allowed: []string{
				`INSERT INTO group_weekly_brief_throttle (committee_uid, window_start, window_end, window_resets_at) VALUES ('c-2', '2026-10-26 00:00:00+00', '2026-11-02 00:00:00+00', '2026-11-02 00:00:00+00')`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()

			_, err = tx.ExecContext(ctx, tc.first)
			require.NoError(t, err)
			for _, stmt := range tc.allowed {
				_, err = tx.ExecContext(ctx, stmt)
				require.NoError(t, err, "must be accepted: %s", stmt)
			}

			// A failed statement aborts the transaction, so the duplicate goes last.
			_, err = tx.ExecContext(ctx, tc.duplicate)
			constraint, isUnique := uniqueViolation(err)
			require.True(t, isUnique, "expected a unique violation, got %v", err)
			assert.Equal(t, tc.constraint, constraint)
		})
	}
}

func TestSchema_RevisionsComeFromOneSequence(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	var first, second int64
	require.NoError(t, db.QueryRowContext(ctx,
		`INSERT INTO committees (uid, project_uid, name, created_at, updated_at) VALUES ('c-1', 'p-1', 'One', now(), now()) RETURNING revision`).Scan(&first))
	require.NoError(t, db.QueryRowContext(ctx,
		`INSERT INTO committee_members (uid, committee_uid, email, created_at, updated_at) VALUES ('m-1', 'c-1', 'first.last@example.com', now(), now()) RETURNING revision`).Scan(&second))
	assert.Greater(t, second, first, "revisions must keep increasing across tables")

	var explicit int64
	require.NoError(t, db.QueryRowContext(ctx,
		`INSERT INTO committees (uid, project_uid, name, revision, created_at, updated_at) VALUES ('c-2', 'p-1', 'Two', 4242, now(), now()) RETURNING revision`).Scan(&explicit))
	assert.EqualValues(t, 4242, explicit, "the dual-write store must be able to carry the KV revision over")
}

func TestSchema_EveryRevisionColumnDrawsFromTheSequence(t *testing.T) {
	db := migratedDB(t)

	rows, err := db.QueryContext(context.Background(), `
		SELECT table_name, coalesce(column_default, '') FROM information_schema.columns
		WHERE table_schema = current_schema() AND column_name = 'revision'
		ORDER BY table_name`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var table, def string
		require.NoError(t, rows.Scan(&table, &def))
		assert.Contains(t, def, "committee_revision_seq", "table %s", table)
		tables = append(tables, table)
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, committeeTables, tables, "every table carries a revision column")
}

func TestSchema_NullableColumnsAcceptTheirZeroValues(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	// An invite with no expiry and no organization or inviter must be storable (the model omits them).
	_, err := db.ExecContext(ctx,
		`INSERT INTO committee_invites (uid, committee_uid, invitee_email, created_at, updated_at) VALUES ('i-1', 'c-1', 'first.last@example.com', now(), now())`)
	require.NoError(t, err)

	var expires, organization sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT expires_at::text, organization::text FROM committee_invites WHERE uid = 'i-1'`).Scan(&expires, &organization))
	assert.False(t, expires.Valid)
	assert.False(t, organization.Valid)

	// writers and auditors default to an empty JSON array, not NULL, so they decode to empty lists.
	_, err = db.ExecContext(ctx, `INSERT INTO committee_settings (committee_uid, created_at, updated_at) VALUES ('c-1', now(), now())`)
	require.NoError(t, err)
	var writers, auditors string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT writers::text, auditors::text FROM committee_settings WHERE committee_uid = 'c-1'`).Scan(&writers, &auditors))
	assert.Equal(t, "[]", writers)
	assert.Equal(t, "[]", auditors)
}
