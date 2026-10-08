# migrate_writers_auditors_to_user_objects

One-time data migration that converts committee settings `writers` and `auditors` from the legacy `[]string` format (usernames) to `[]object` entries (`{"username": "..."}`), directly in the `committee-settings` NATS KV bucket, then re-indexes each migrated record.

## When to run

Run once per environment after deploying the service version that reads `writers`/`auditors` as user objects, to convert legacy records. Records already in object format (or with empty/missing lists) are skipped, so re-running is safe.

Use `--force-reindex` when KV is already migrated but OpenSearch still holds old documents. For plain settings re-indexing, `committee-cli sync reindex-settings` also exists, but it is not a replacement for the KV conversion.

## What it changes

For each non-`lookup/` and non-`slug/` key in `committee-settings`:

- A field needs migration when it is a non-empty array whose first element is a string. Each non-empty string becomes `{"username": <string>}`; `email`, `name` and `avatar` are not populated. `writers` and `auditors` are checked and converted independently.
- Sets `updated_at` and writes with optimistic-concurrency `Update` (up to 3 attempts, re-reading between attempts).
- Publishes a full indexer envelope (`action: updated`, an `Authorization` header from `AUTH_TOKEN`, and an `IndexingConfig` with `public=false`, `auditor` access relation, and tags built from the `committees` base record: `project_uid`, `project_slug`, `parent_uid`, `category`, uid) to `lfx.index.committee_settings`. If the base record cannot be read, only the uid tags are used. Envelope build or publish failures are logged as warnings and the record still counts as updated.

With `--force-reindex`, no migration check or KV write happens: every settings record is republished to the indexer as-is.

## Prerequisites

- Network access to NATS (credentials, if needed, go in `NATS_URL`).
- The `committee-settings` and `committees` KV buckets must exist (the bucket names are not configurable).
- `AUTH_TOKEN` set to a bearer token the indexer accepts for the `Authorization` header. It is deliberately an env var, not a flag. If it is empty, the messages carry an empty header.

## Configuration

| Flag / env | Default | Description |
|---|---|---|
| `--nats-url` / `NATS_URL` | `nats://localhost:4222` | NATS server URL |
| `AUTH_TOKEN` (env only) | empty | Bearer token placed in the indexer message `Authorization` header |
| `--dry-run` | `false` | Log which records would be migrated; no KV writes, no publishes |
| `--force-reindex` | `false` | Republish every record to the indexer without modifying KV |
| `--debug` | `false` | Debug logging |

`--dry-run` has no effect when combined with `--force-reindex`: the force-reindex path returns before the dry-run check and publishes.

## Usage

```bash
go build -o scripts/migrations/migrate_writers_auditors_to_user_objects/bin/migrate_writers_auditors \
  ./scripts/migrations/migrate_writers_auditors_to_user_objects

export NATS_URL=nats://nats.example:4222 AUTH_TOKEN=<bearer-token>
./scripts/migrations/migrate_writers_auditors_to_user_objects/bin/migrate_writers_auditors --dry-run
./scripts/migrations/migrate_writers_auditors_to_user_objects/bin/migrate_writers_auditors
# only if KV is already migrated but search is stale:
./scripts/migrations/migrate_writers_auditors_to_user_objects/bin/migrate_writers_auditors --force-reindex
```

## Output and verification

JSON logs on stdout, a progress line every 10 records, then a summary (Total, Updated, Skipped "already migrated", Failed, Success rate, Duration, Rate). Exits non-zero if any record failed. Success means `Failed: 0` and a second run reporting `Updated: 0`. In dry-run, `Updated` counts records that *would* change. Spot-check a settings key in KV: `writers`/`auditors` should be arrays of objects.

## Risks

- **Lossy to roll back.** Only `username` is carried over, and the script keeps no copy of the old arrays; restore from a KV backup to undo. Take one first.
- Resulting entries have no `email`/`name`/`avatar`; they stay empty until populated by the service.
- Writes bypass the service's validation and FGA tuple sync; check whether writer/auditor FGA tuples need a separate sync after the change.
- `--force-reindex` publishes `public=false` envelopes for every settings record.
