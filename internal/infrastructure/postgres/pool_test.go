// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test credentials are assembled at run time so no connection string with an inline password
// is committed (secret scanners flag those, even for obvious placeholders).
const fakePassword = "not-a-real-secret"

// fakeTail is the part of a fake password that follows a ";", which a query parser would split off.
const fakeTail = "not-a-real-tail"

func urlWithPassword(host string) string {
	return "postgres" + "://first-last:" + fakePassword + "@" + host + "/committee?sslmode=disable"
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"url the parser rejects", "postgres://first-last:" + fakePassword + "@db.example.com:port/committee", "postgres://first-last:xxxxx@db.example.com:port/committee"},
		{"url the parser rejects, with @ in the password", "postgres://first-last:p@ss-" + fakePassword + "@db.example.com:port/committee", "postgres://first-last:xxxxx@db.example.com:port/committee"},
		{"url the parser rejects, host without a path", "postgres://first-last:" + fakePassword + "@db.example.com:port", "postgres://first-last:xxxxx@db.example.com:port"},
		{"url the parser rejects, with an encoded password parameter", "postgres://db.example.com:port/committee?sslmode=disable&%70assword=" + fakePassword, "postgres://db.example.com:port/committee?sslmode=disable&%70assword=xxxxx"},
		{"url the parser rejects, with a newline in the password", "postgres://first-last:a\n" + fakePassword + "@db.example.com/committee", "postgres://first-last:xxxxx@db.example.com/committee"},
		{"url the parser rejects, password parameter after an @ in the query", "postgres://db.example.com:port/committee?application_name=a@b&%70assword=" + fakePassword, "postgres://db.example.com:xxxxx@b&%70assword=xxxxx"},
		{"query with a semicolon separator", "postgres://first-last@db.example.com/committee?sslmode=require;password=" + fakePassword, "postgres://first-last@db.example.com/committee?sslmode=require;password=xxxxx"},
		{"query with a bad escape", "postgres://first-last@db.example.com/committee?password=" + fakePassword + "%ZZ", "postgres://first-last@db.example.com/committee?password=xxxxx"},
		{"password holding a semicolon", "postgres://first-last@db.example.com/committee?password=" + fakePassword + ";" + fakeTail, "postgres://first-last@db.example.com/committee?password=xxxxx"},
		{"url the parser rejects, encoded password key holding a semicolon", "postgres://db.example.com:port/committee?%70assword=" + fakePassword + ";" + fakeTail, "postgres://db.example.com:port/committee?%70assword=xxxxx"},
		{"password key in capitals", "postgres://first-last@db.example.com/committee?PASSWORD=" + fakePassword, "postgres://first-last@db.example.com/committee?PASSWORD=xxxxx"},
		{"client key passphrase", "postgres://first-last@db.example.com/committee?sslmode=verify-full&sslpassword=" + fakePassword, "postgres://first-last@db.example.com/committee?sslmode=verify-full&sslpassword=xxxxx"},
		{"url the parser rejects, with an encoded client key passphrase", "postgres://db.example.com:port/committee?%73slpassword=" + fakePassword, "postgres://db.example.com:port/committee?%73slpassword=xxxxx"},
		{"client key passphrase in keyword form", "host=db.example.com sslpassword=" + fakePassword + " dbname=committee", "host=db.example.com sslpassword=xxxxx dbname=committee"},
		{"password text after a # becomes a fragment", "postgres://first-last@db.example.com/committee?password=first-part#" + fakePassword, "postgres://first-last@db.example.com/committee?password=xxxxx"},
		{"password holding a semicolon, followed by another parameter", "postgres://first-last@db.example.com/committee?password=" + fakePassword + ";" + fakeTail + "&sslmode=require", "postgres://first-last@db.example.com/committee?password=xxxxx&sslmode=require"},
		{"url form", urlWithPassword("db.example.com:5432"), "postgres://first-last:xxxxx@db.example.com:5432/committee?sslmode=disable"},
		{"url without a password is unchanged", "postgres://first-last@db.example.com/committee", "postgres://first-last@db.example.com/committee"},
		{"password as a query parameter", "postgres://db.example.com/committee?password=" + fakePassword, "postgres://db.example.com/committee?password=xxxxx"},
		{"keyword form", "host=db.example.com user=first-last password=" + fakePassword + " dbname=committee", "host=db.example.com user=first-last password=xxxxx dbname=committee"},
		{"keyword form with an escaped space", `host=db.example.com password=first\ ` + fakePassword + ` dbname=committee`, "host=db.example.com password=xxxxx dbname=committee"},
		{"quoted keyword form", "host=db.example.com password='" + fakePassword + " with space' dbname=committee", "host=db.example.com password=xxxxx dbname=committee"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactURL(tt.dsn)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, fakePassword)
			assert.NotContains(t, got, fakeTail)
		})
	}
}

func TestNewPool(t *testing.T) {
	t.Run("requires a URL", func(t *testing.T) {
		_, err := NewPool(context.Background(), Config{URL: "  "})
		require.Error(t, err)
	})

	t.Run("a malformed URL error does not leak the password", func(t *testing.T) {
		// An invalid port makes the URL unparseable, which is the path where drivers echo the input.
		_, err := NewPool(context.Background(), Config{URL: "postgres://first-last:" + fakePassword + "@db.example.com:port/committee"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), fakePassword)
	})

	t.Run("an unreachable database error does not leak the password", func(t *testing.T) {
		// Port 1 refuses connections, so this exercises the connect failure path.
		_, err := NewPool(context.Background(), Config{URL: urlWithPassword("127.0.0.1:1")})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), fakePassword)
		assert.True(t, strings.HasPrefix(err.Error(), "connect to postgres:"), err.Error())
	})

	t.Run("connects, shares its connections and reports ready", func(t *testing.T) {
		pool, err := NewPool(context.Background(), Config{URL: testDSN(t), MaxConns: 2})
		require.NoError(t, err)
		defer pool.Close()
		assert.EqualValues(t, 2, pool.Config().MaxConns)

		require.NoError(t, NewReady(pool).IsReady(context.Background()))

		db := OpenDB(pool)
		require.NoError(t, db.PingContext(context.Background()))
		// Closing the migration handle must not close the shared pool.
		require.NoError(t, db.Close())
		require.NoError(t, NewReady(pool).IsReady(context.Background()))
	})

	t.Run("connects through the PG* variables", func(t *testing.T) {
		parsed, err := url.Parse(testDSN(t))
		require.NoError(t, err)
		password, _ := parsed.User.Password()
		if password == "" {
			// CI's database uses trust authentication and a URL without a password, which it ignores; the PG* path
			// requires one, as in production.
			password = fakePassword
		}
		t.Setenv("PGSSLMODE", "disable") // the composed URL names no sslmode; the driver takes it from here
		cfg, err := ConfigFromEnv(func(key string) string {
			return map[string]string{
				"PGHOST":     parsed.Hostname(),
				"PGPORT":     parsed.Port(),
				"PGUSER":     parsed.User.Username(),
				"PGPASSWORD": password,
				"PGDATABASE": strings.TrimPrefix(parsed.Path, "/"),
			}[key]
		})
		require.NoError(t, err)

		pool, err := NewPool(context.Background(), cfg)
		require.NoError(t, err)
		defer pool.Close()
		require.NoError(t, NewReady(pool).IsReady(context.Background()))
	})

	t.Run("readiness fails once the pool is closed", func(t *testing.T) {
		pool, err := NewPool(context.Background(), Config{URL: testDSN(t)})
		require.NoError(t, err)
		assert.EqualValues(t, DefaultMaxConns, pool.Config().MaxConns)
		pool.Close()
		assert.Error(t, NewReady(pool).IsReady(context.Background()))
	})
}

// pgx hides the password in its own errors today; redactError is the guard for a driver or a
// wrapped error that echoes the connection string or the password anyway.
func TestRedactError(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		// echoes are the forms of the password a driver error could repeat. They are written out
		// here, not derived from passwords(), so the test does not move with the code it checks.
		echoes []string
	}{
		{"url form", urlWithPassword("db.example.com:5432"), []string{fakePassword}},
		{"url the parser rejects, with a newline in the password", "postgres://first-last:a\n" + fakePassword + "@db.example.com/committee", []string{"a\n" + fakePassword}},
		{"url the parser rejects, password parameter after an @ in the query", "postgres://db.example.com:port/committee?application_name=a@b&%70assword=" + fakePassword, []string{fakePassword}},
		{"query with a semicolon separator", "postgres://first-last@db.example.com/committee?sslmode=require;password=" + fakePassword, []string{fakePassword}},
		{"query with a bad escape", "postgres://first-last@db.example.com/committee?password=" + fakePassword + "%ZZ", []string{fakePassword + "%ZZ"}},
		{"password key in capitals", "postgres://first-last@db.example.com/committee?PASSWORD=" + fakePassword, []string{fakePassword}},
		{"client key passphrase", "postgres://first-last@db.example.com/committee?sslmode=verify-full&sslpassword=" + fakePassword, []string{fakePassword}},
		{"url the parser rejects, with an encoded client key passphrase", "postgres://db.example.com:port/committee?%73slpassword=" + fakePassword, []string{fakePassword}},
		{"client key passphrase in keyword form", "host=db.example.com sslpassword=" + fakePassword + " dbname=committee", []string{fakePassword}},
		{"password text after a # becomes a fragment", "postgres://first-last@db.example.com/committee?password=first-part#" + fakePassword, []string{fakePassword}},
		{"password holding a semicolon", "postgres://first-last@db.example.com/committee?password=" + fakePassword + ";" + fakeTail, []string{fakePassword + ";" + fakeTail, fakePassword, fakeTail}},
		{"url the parser rejects, encoded password key holding a semicolon", "postgres://db.example.com:port/committee?%70assword=" + fakePassword + ";" + fakeTail, []string{fakePassword + ";" + fakeTail, fakePassword, fakeTail}},
		{"url the parser rejects, with an encoded password parameter", "postgres://db.example.com:port/committee?%70assword=" + fakePassword, []string{fakePassword}},
		{"url the parser rejects", "postgres://first-last:" + fakePassword + "@db.example.com:port/committee", []string{fakePassword}},
		{"url the parser rejects, with @ in the password", "postgres://first-last:p@ss-" + fakePassword + "@db.example.com:port/committee", []string{"p@ss-" + fakePassword}},
		{"keyword form with an escaped space", `host=db.example.com password=first\ ` + fakePassword + ` dbname=committee`, []string{`first\ ` + fakePassword, "first " + fakePassword}},
		{"percent-encoded password", "postgres://first-last:p%40ss-" + fakePassword + "@db.example.com/committee", []string{"p%40ss-" + fakePassword, "p@ss-" + fakePassword}},
		{"password query parameter", "postgres://db.example.com/committee?password=" + fakePassword, []string{fakePassword}},
		{"keyword form", "host=db.example.com user=first-last password=" + fakePassword, []string{fakePassword}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The error repeats the whole connection string, then each form of the password alone.
			got := redactError(tt.dsn, "connect to postgres", errors.New("cannot use "+tt.dsn))
			assert.NotContains(t, got.Error(), fakePassword)
			assert.True(t, strings.HasPrefix(got.Error(), "connect to postgres: "), got.Error())

			for _, echo := range tt.echoes {
				got := redactError(tt.dsn, "connect to postgres", errors.New("password "+echo+" rejected"))
				assert.NotContains(t, got.Error(), echo)
			}
		})
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	t.Run("the URL is required", func(t *testing.T) {
		_, err := ConfigFromEnv(env(nil))
		require.Error(t, err)
		_, err = ConfigFromEnv(env(map[string]string{"POSTGRES_URL": "  "}))
		require.Error(t, err)
	})

	t.Run("the connection can be given as PG* variables, as the newsletter service does", func(t *testing.T) {
		cfg, err := ConfigFromEnv(env(pgParts("db.example.com", "")))
		require.NoError(t, err)
		assert.Equal(t, Config{URL: "postgres" + "://first-last:" + fakePassword + "@db.example.com:5432/committee"}, cfg)
	})

	t.Run("an incomplete set of PG* variables is not a connection, and the error carries no credential", func(t *testing.T) {
		values := pgParts("db.example.com", "")
		delete(values, "PGUSER")
		_, err := ConfigFromEnv(env(values))
		require.ErrorContains(t, err, "POSTGRES_URL is empty")
		require.ErrorContains(t, err, "PGHOST")
		assert.NotContains(t, err.Error(), fakePassword)
		assert.NotContains(t, err.Error(), "db.example.com")
	})

	t.Run("the pool size is optional and must be a positive integer", func(t *testing.T) {
		cfg, err := ConfigFromEnv(env(map[string]string{"POSTGRES_URL": "postgres://example.com/db"}))
		require.NoError(t, err)
		assert.Equal(t, Config{URL: "postgres://example.com/db"}, cfg)

		cfg, err = ConfigFromEnv(env(map[string]string{"POSTGRES_URL": "postgres://example.com/db", "POSTGRES_MAX_CONNS": "25"}))
		require.NoError(t, err)
		assert.EqualValues(t, 25, cfg.MaxConns)

		for _, bad := range []string{"0", "-1", "many"} {
			_, err = ConfigFromEnv(env(map[string]string{"POSTGRES_URL": "postgres://example.com/db", "POSTGRES_MAX_CONNS": bad}))
			require.Error(t, err, bad)
		}
	})

	t.Run("an error carries no credential", func(t *testing.T) {
		_, err := ConfigFromEnv(env(map[string]string{"POSTGRES_URL": urlWithPassword("example.com"), "POSTGRES_MAX_CONNS": "many"}))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "example.com")
	})
}

// pgParts is a complete set of PG* variables for the host, with the port when one is given.
func pgParts(host, port string) map[string]string {
	values := map[string]string{
		"PGHOST":     host,
		"PGUSER":     "first-last",
		"PGPASSWORD": fakePassword,
		"PGDATABASE": "committee",
	}
	if port != "" {
		values["PGPORT"] = port
	}
	return values
}

func TestConnectionURL(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	t.Run("POSTGRES_URL wins over the PG* variables", func(t *testing.T) {
		values := pgParts("db.example.com", "")
		values["POSTGRES_URL"] = urlWithPassword("explicit.example.com")
		got, ok := ConnectionURL(env(values))
		require.True(t, ok)
		assert.Equal(t, urlWithPassword("explicit.example.com"), got)
	})

	t.Run("a blank POSTGRES_URL falls back to the PG* variables", func(t *testing.T) {
		values := pgParts("db.example.com", "6432")
		values["POSTGRES_URL"] = "  "
		got, ok := ConnectionURL(env(values))
		require.True(t, ok)
		assert.Equal(t, "postgres"+"://first-last:"+fakePassword+"@db.example.com:6432/committee", got)
	})

	t.Run("the port defaults to 5432 and an IPv6 host is bracketed", func(t *testing.T) {
		got, ok := ConnectionURL(env(pgParts("::1", "")))
		require.True(t, ok)
		assert.Equal(t, "postgres"+"://first-last:"+fakePassword+"@[::1]:5432/committee", got)
	})

	t.Run("a password with reserved characters survives the round trip", func(t *testing.T) {
		values := pgParts("db.example.com", "")
		values["PGPASSWORD"] = "p@ss:w/rd?#%"
		got, ok := ConnectionURL(env(values))
		require.True(t, ok)
		parsed, err := url.Parse(got)
		require.NoError(t, err)
		password, _ := parsed.User.Password()
		assert.Equal(t, "p@ss:w/rd?#%", password)
		assert.Equal(t, "db.example.com", parsed.Hostname())
		assert.Equal(t, "/committee", parsed.Path)
	})

	t.Run("each of host, user, password and database is required", func(t *testing.T) {
		for _, missing := range []string{"PGHOST", "PGUSER", "PGPASSWORD", "PGDATABASE"} {
			values := pgParts("db.example.com", "")
			delete(values, missing)
			_, ok := ConnectionURL(env(values))
			assert.False(t, ok, missing)
			values[missing] = "   "
			if missing == "PGPASSWORD" {
				values[missing] = ""
			}
			_, ok = ConnectionURL(env(values))
			assert.False(t, ok, "blank "+missing)
		}
		_, ok := ConnectionURL(env(nil))
		assert.False(t, ok)
	})

	t.Run("PGSSLMODE reaches the composed connection, which names no sslmode itself", func(t *testing.T) {
		got, ok := ConnectionURL(env(pgParts("db.example.com", "")))
		require.True(t, ok)
		assert.NotContains(t, got, "sslmode")

		t.Setenv("PGSSLMODE", "")
		prefer, err := pgxpool.ParseConfig(got)
		require.NoError(t, err)
		assert.NotNil(t, prefer.ConnConfig.TLSConfig, "pgx defaults to prefer, which offers TLS")

		t.Setenv("PGSSLMODE", "disable")
		disabled, err := pgxpool.ParseConfig(got)
		require.NoError(t, err)
		assert.Nil(t, disabled.ConnConfig.TLSConfig, "PGSSLMODE=disable is honored")
	})
}
