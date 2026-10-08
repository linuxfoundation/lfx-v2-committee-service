# migrate_show_meeting_attendees

One-time data migration that adds the `show_meeting_attendees` field, set to `false`, to every committee settings record that does not already have it. It writes directly to the `committee-settings` NATS KV bucket.

## When to run

Run once per environment when rolling out the `show_meeting_attendees` setting, for settings records created before the field existed. Records that already have the field (any value) are skipped, so re-running is safe.

No `committee-cli sync` subcommand replaces this script.

## What it changes

For each non-`lookup/` and non-`slug/` key in the bucket:

- If `show_meeting_attendees` is absent: sets it to `false`, sets `updated_at` to the current time, and writes with an optimistic-concurrency `Update` (up to 3 attempts, re-reading between attempts; if a concurrent writer added the field, the record is counted as skipped).
- Otherwise: skipped.

After a successful write it publishes the updated settings JSON to the index subject (default `lfx.index.committee_settings`). The message is the bare record, not the full indexer envelope the service publishes, and a publish failure is only logged as a warning (the record still counts as updated). If OpenSearch looks stale afterwards, run `committee-cli sync reindex-settings`.

## Prerequisites

- Network access to NATS (credentials, if needed, go in `NATS_URL`). The script logs the NATS URL and `nc.ConnectedUrl()` unredacted, so credentials embedded in the URL appear in the logs; avoid embedding them or treat the logs as sensitive.
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
go build -o scripts/migrations/migrate_show_meeting_attendees/bin/migrate_show_meeting_attendees ./scripts/migrations/migrate_show_meeting_attendees

NATS_URL=nats://nats.example:4222 \
  ./scripts/migrations/migrate_show_meeting_attendees/bin/migrate_show_meeting_attendees --dry-run

NATS_URL=nats://nats.example:4222 \
  ./scripts/migrations/migrate_show_meeting_attendees/bin/migrate_show_meeting_attendees
```

## Output and verification

JSON logs on stdout, a progress line every 10 records, then a summary (Total, Updated, Skipped "already had field", Failed, Success rate, Duration, Rate). Exits non-zero if any record failed. Success means `Failed: 0`; a second run should report `Updated: 0`. In dry-run, `Updated` counts records that *would* change.

## Risks

- **Not reversible by the script.** Rolling back means deleting the field or restoring a KV backup; take a backup first.
- Writes bypass the service's validation, FGA and event publishing.
- Structurally identical to `migrate_member_visibility`; running both is fine and they do not conflict.
