# migrate_member_visibility

One-time data migration that adds the `member_visibility` field, set to `hidden`, to every committee settings record that does not already have it. It writes directly to the `committee-settings` NATS KV bucket.

## When to run

Run once per environment when rolling out the `member_visibility` setting, for settings records created before the field existed. Records that already have the field (any value) are skipped, so re-running is safe.

No `committee-cli sync` subcommand replaces this script.

## What it changes

For each non-`lookup/` and non-`slug/` key in the bucket:

- If `member_visibility` is absent: sets it to `"hidden"`, sets `updated_at` to the current time, and writes with an optimistic-concurrency `Update` (up to 3 attempts, re-reading between attempts; if a concurrent writer added the field, the record is counted as skipped).
- Otherwise: skipped.

After a successful write it publishes the updated settings JSON to the index subject (default `lfx.index.committee_settings`). The message is the bare record, not the full indexer envelope the service publishes, and a publish failure is only logged as a warning (the record still counts as updated). If OpenSearch looks stale afterwards, run `committee-cli sync reindex-settings`.

## Prerequisites

- Network access to NATS (credentials, if needed, go in `NATS_URL`).
- The `committee-settings` KV bucket must exist.

## Configuration

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--nats-url` | `NATS_URL` | `nats://localhost:4222` | NATS server URL |
| `--bucket-name` | | `committee-settings` | KV bucket to migrate |
| `--index-subject` | | `lfx.index.committee_settings` | Subject for index messages |
| `--dry-run` | | `false` | Log what would change; no KV writes, no publishes |
| `--debug` | | `false` | Debug logging |

## Usage

```bash
go build -o scripts/migrations/migrate_member_visibility/bin/migrate_member_visibility ./scripts/migrations/migrate_member_visibility

NATS_URL=nats://nats.example:4222 \
  ./scripts/migrations/migrate_member_visibility/bin/migrate_member_visibility --dry-run

NATS_URL=nats://nats.example:4222 \
  ./scripts/migrations/migrate_member_visibility/bin/migrate_member_visibility
```

## Output and verification

JSON logs on stdout, a progress line every 10 records, then a summary (Total, Updated, Skipped "already had field", Failed, Success rate, Duration, Rate). Exits non-zero if any record failed. Success means `Failed: 0`; a second run should report `Updated: 0`. In dry-run, `Updated` counts records that *would* change.

## Risks

- **Not reversible by the script.** Rolling back means deleting the field or restoring a KV backup; take a backup first.
- Existing records get `hidden`, which is the most restrictive value. Confirm that is the intended default before running.
- Writes bypass the service's validation, FGA and event publishing.
