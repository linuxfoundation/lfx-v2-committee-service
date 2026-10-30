// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	// DefaultMaxConns is the pool size used when Config.MaxConns is not set.
	DefaultMaxConns = 10

	// connectTimeout bounds the initial connection check in NewPool.
	connectTimeout = 10 * time.Second

	// readyTimeout bounds a readiness ping so a hung database cannot hang the probe.
	readyTimeout = 3 * time.Second

	redacted = "xxxxx"
)

// Config configures the connection pool.
type Config struct {
	// URL is the connection string (postgres://user:password@host:port/db?sslmode=...). It holds a
	// credential: it is never logged and never appears in an error returned by this package.
	URL string

	// MaxConns caps the pool size; zero means DefaultMaxConns.
	MaxConns int32
}

// NewPool opens a connection pool and verifies the database is reachable. Every error it returns
// has the URL and its password removed, so callers can log it as is.
func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, errors.New("postgres URL is required")
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, redactError(cfg.URL, "parse postgres URL", err)
	}
	poolCfg.MaxConns = cfg.MaxConns
	if poolCfg.MaxConns <= 0 {
		poolCfg.MaxConns = DefaultMaxConns
	}

	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, redactError(cfg.URL, "create postgres pool", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, redactError(cfg.URL, "connect to postgres", err)
	}
	return pool, nil
}

// OpenDB returns a database/sql handle that shares the pool's connections, for the migration runner.
// Closing the returned handle does not close the pool.
func OpenDB(pool *pgxpool.Pool) *sql.DB {
	return stdlib.OpenDBFromPool(pool)
}

// Ready reports whether the database answers a ping within a short timeout.
type Ready struct {
	pool *pgxpool.Pool
}

// NewReady returns a readiness check for the pool.
func NewReady(pool *pgxpool.Pool) *Ready {
	return &Ready{pool: pool}
}

// IsReady pings the database.
func (r *Ready) IsReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres not ready: %w", err)
	}
	return nil
}

var kvPassword = regexp.MustCompile(`(?i)(password\s*=\s*)('(?:[^'\\]|\\.)*'|\S+)`)

// RedactURL hides the password in a connection string, in URL or keyword/value form.
func RedactURL(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), redacted)
		}
		// A password can also be passed as a query parameter.
		if q := u.Query(); q.Has("password") {
			q.Set("password", redacted)
			u.RawQuery = q.Encode()
		}
		return u.String()
	}
	return kvPassword.ReplaceAllString(dsn, "${1}"+redacted)
}

// redactError wraps err with a context message, removing the connection string and any password
// from the text. The driver can echo either one in its errors.
func redactError(dsn, msg string, err error) error {
	text := strings.ReplaceAll(err.Error(), dsn, RedactURL(dsn))
	for _, secret := range passwords(dsn) {
		text = strings.ReplaceAll(text, secret, redacted)
	}
	return fmt.Errorf("%s: %s", msg, text)
}

// passwords extracts the credentials from a connection string so they can be scrubbed from text.
func passwords(dsn string) []string {
	var found []string
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if p, has := u.User.Password(); has && p != "" {
			found = append(found, p)
		}
		if p := u.Query().Get("password"); p != "" {
			found = append(found, p)
		}
		return found
	}
	for _, m := range kvPassword.FindAllStringSubmatch(dsn, -1) {
		if p := strings.Trim(m[2], "'"); p != "" {
			found = append(found, p)
		}
	}
	return found
}
