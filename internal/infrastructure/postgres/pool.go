// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
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

// ConnectionURL returns the connection string: POSTGRES_URL when it is set, otherwise one composed from PGHOST,
// PGPORT (5432 when empty), PGUSER, PGPASSWORD and PGDATABASE, as the newsletter service does, so the credentials can
// come straight from the keys of the RDS secret and follow its rotation. It reports false when neither is complete.
// The composed string names no sslmode: pgx takes it from PGSSLMODE and PGSSLROOTCERT, and defaults to prefer. The
// variables follow the newsletter service, not all of libpq: PGHOST must be a single host name or address, not a Unix
// socket directory or a list. pgx also fills in from the environment whatever a POSTGRES_URL leaves out.
func ConnectionURL(getenv func(string) string) (string, bool) {
	if raw := getenv("POSTGRES_URL"); strings.TrimSpace(raw) != "" {
		return raw, true
	}
	host := strings.TrimSpace(getenv("PGHOST"))
	user := strings.TrimSpace(getenv("PGUSER"))
	password := getenv("PGPASSWORD")
	database := strings.TrimSpace(getenv("PGDATABASE"))
	if host == "" || user == "" || password == "" || database == "" {
		return "", false
	}
	port := strings.TrimSpace(getenv("PGPORT"))
	if port == "" {
		port = "5432"
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + database,
	}
	return u.String(), true
}

// ConfigFromEnv reads the connection settings from ConnectionURL and, when set, POSTGRES_MAX_CONNS. A connection
// string is required; no value appears in the errors.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	connection, ok := ConnectionURL(getenv)
	if !ok {
		return Config{}, errors.New("POSTGRES_URL is empty and PGHOST, PGUSER, PGPASSWORD and PGDATABASE are not all set")
	}
	cfg := Config{URL: connection}
	if raw := getenv("POSTGRES_MAX_CONNS"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n <= 0 {
			return Config{}, errors.New("invalid POSTGRES_MAX_CONNS: must be a positive integer")
		}
		cfg.MaxConns = int32(n)
	}
	return cfg, nil
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

var kvPassword = regexp.MustCompile(`(?i)(password\s*=\s*)('(?:[^'\\]|\\.)*'|(?:[^\s\\]|\\.)+)`)

// escaped matches a backslash escape in a keyword/value string, as in password=a\ b.
var escaped = regexp.MustCompile(`\\(.)`)

// userinfoPassword matches the password of a scheme://user:password@host string without parsing it,
// so a URL that net/url rejects (a bad port, say) is still scrubbed. The password is greedy and the
// host part that follows may not contain "@", so a password holding "@" is matched up to the last one.
// The s flag lets the password hold a newline.
var userinfoPassword = regexp.MustCompile(`(?is)^([a-z][a-z0-9+.-]*://[^:/@]*:)(.*)(@[^@/?#]*(?:[/?#]|$))`)

// RedactURL hides the password in a connection string, in URL or keyword/value form.
func RedactURL(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), redacted)
		}
		// A password can also be passed as a query parameter. The raw query is scrubbed as text: Query()
		// drops a pair it cannot parse (a ";" separator, a bad escape) and matches the key case-sensitively.
		u.RawQuery = redactQueryString(u.RawQuery)
		// A DSN has no use for a fragment, and one can only be password text after a "#".
		u.Fragment, u.RawFragment = "", ""
		return u.String()
	}
	// The query goes first: the greedy userinfo match below can run through it up to a later "@".
	dsn = redactRawQuery(dsn)
	dsn = userinfoPassword.ReplaceAllString(dsn, "${1}"+redacted+"${3}")
	return kvPassword.ReplaceAllString(dsn, "${1}"+redacted)
}

// schemePrefix matches the start of a scheme://... connection string.
var schemePrefix = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)

// isPasswordKey reports whether a raw query key names a secret, decoding it first so an encoded
// spelling such as %70assword is recognized too. sslpassword is the passphrase of a client key.
func isPasswordKey(key string) bool {
	decoded, err := url.QueryUnescape(key)
	return err == nil && (strings.EqualFold(decoded, "password") || strings.EqualFold(decoded, "sslpassword"))
}

// passwordParam finds the password parameter in one "&"-separated segment of a raw query string.
// net/url's parser rejects ";" separators, so a segment can hold several pairs, and a password may
// itself contain a ";". To stay on the safe side the password is taken to run to the end of the
// segment, so nothing after a ";" is left visible. It returns the pieces of the segment split on ";",
// the index of the one that starts the password, and the key as written.
func passwordParam(segment string) (pieces []string, at int, key string, found bool) {
	pieces = strings.Split(segment, ";")
	for n, piece := range pieces {
		if k, _, ok := strings.Cut(piece, "="); ok && isPasswordKey(k) {
			return pieces, n, k, true
		}
	}
	return pieces, 0, "", false
}

// redactQueryString hides the value of password parameters in a raw query string.
func redactQueryString(query string) string {
	segments := strings.Split(query, "&")
	for n, segment := range segments {
		if pieces, at, key, ok := passwordParam(segment); ok {
			segments[n] = strings.Join(append(pieces[:at], key+"="+redacted), ";")
		}
	}
	return strings.Join(segments, "&")
}

// redactRawQuery hides password parameters in the query string of a scheme://... connection string
// without parsing the rest of it, so a URL net/url rejects is still scrubbed.
func redactRawQuery(dsn string) string {
	i := strings.IndexByte(dsn, '?')
	if i < 0 || !schemePrefix.MatchString(dsn) {
		return dsn
	}
	return dsn[:i+1] + redactQueryString(dsn[i+1:])
}

// queryPasswords returns the password parameter values of a raw query string: the value as written
// (a ";" and what follows it included), decoded, and each ";"-separated piece of it.
func queryPasswords(query string) []string {
	var found []string
	for _, segment := range strings.Split(query, "&") {
		pieces, at, _, ok := passwordParam(segment)
		if !ok {
			continue
		}
		_, first, _ := strings.Cut(pieces[at], "=")
		pieces[at] = first
		value := strings.Join(pieces[at:], ";")
		if value == "" {
			continue
		}
		found = append(found, value)
		if decoded, err := url.QueryUnescape(value); err == nil && decoded != value {
			found = append(found, decoded)
		}
		for _, piece := range pieces[at:] {
			if piece != "" && piece != value {
				found = append(found, piece)
			}
		}
	}
	return found
}

// rawQueryPasswords returns the password parameter values of a scheme://... connection string, read
// without parsing the rest of it.
func rawQueryPasswords(dsn string) []string {
	i := strings.IndexByte(dsn, '?')
	if i < 0 || !schemePrefix.MatchString(dsn) {
		return nil
	}
	return queryPasswords(dsn[i+1:])
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
	// The raw (still percent-encoded) password, which an error can echo without the full string.
	if m := userinfoPassword.FindStringSubmatch(dsn); m != nil && m[2] != "" {
		found = append(found, m[2])
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if p, has := u.User.Password(); has && p != "" {
			found = append(found, p)
		}
		found = append(found, queryPasswords(u.RawQuery)...)
		if u.Fragment != "" {
			found = append(found, u.Fragment)
		}
		return found
	}
	found = append(found, rawQueryPasswords(dsn)...)
	for _, m := range kvPassword.FindAllStringSubmatch(dsn, -1) {
		if p := strings.Trim(m[2], "'"); p != "" {
			found = append(found, p)
			// An error can echo the value with its escapes resolved.
			if plain := escaped.ReplaceAllString(p, "$1"); plain != p {
				found = append(found, plain)
			}
		}
	}
	return found
}
