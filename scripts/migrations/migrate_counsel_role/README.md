# migrate_counsel_role

One-time data migration that rewrites every committee member whose `role.name` is `Counsel` to `None`, directly in the `committee-members` NATS KV bucket.

## When to run

Run once per environment after the `Counsel` role has been removed from the allowed role set, to clean up legacy member records that still carry it. Records with any other role are left untouched, so it is safe to run on a bucket that is already clean.

No `committee-cli sync` subcommand replaces this script.

## What it changes

For each non-`lookup/` and non-`slug/` key in the bucket:

- If `role.name == "Counsel"`: sets `role.name = "None"`, sets `updated_at` to the current time, and writes the record back with an optimistic-concurrency `Update` (up to 3 attempts, re-reading the record between attempts; if a concurrent writer already changed the role, the record is counted as skipped).
- Otherwise: skipped.

After a successful write it publishes the updated member JSON to the index subject (default `lfx.index.committee_member`). The message is the bare record, not the full indexer envelope that the service publishes, and publish failures are counted as a failed record. Index publishing is best-effort: the script does not call `Flush`, so a connection failure after `Publish` returns but before the server receives the message can lose the index update while the record still counts as updated.

## Prerequisites

- Network access to the NATS server and credentials if it requires them (the startup log redacts the URL, but the successful-connection log prints `nc.ConnectedUrl()` unredacted, so avoid embedding credentials in `NATS_URL`).
- The `committee-members` KV bucket must exist.

## Configuration

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--nats-url` | `NATS_URL` | `nats://localhost:4222` | NATS server URL |
| `--bucket-name` | | `committee-members` | KV bucket to migrate |
| `--index-subject` | | `lfx.index.committee_member` | Subject for index messages |
| `--dry-run` | | `false` | Log what would change; no KV writes, no publishes |
| `--debug` | | `false` | Debug logging |

## Usage

Always dry-run first:

```bash
go build -o scripts/migrations/migrate_counsel_role/bin/migrate_counsel_role ./scripts/migrations/migrate_counsel_role

NATS_URL=nats://nats.example:4222 \
  ./scripts/migrations/migrate_counsel_role/bin/migrate_counsel_role --dry-run

NATS_URL=nats://nats.example:4222 \
  ./scripts/migrations/migrate_counsel_role/bin/migrate_counsel_role
```

`go run ./scripts/migrations/migrate_counsel_role` works as well.

## Output and verification

JSON logs on stdout, a progress line every 10 records, then a summary:

```text
Total records / Updated / Skipped (role was not Counsel) / Failed / Success rate / Duration / Rate
```

The process exits non-zero if any record failed. Re-run it after a failure: already-migrated records are skipped. Success means `Failed: 0`; a second run should report `Updated: 0`. In dry-run, `Updated` counts records that *would* change.

## Risks

- **Irreversible.** The original role is not recorded. To undo, restore the bucket from a backup or snapshot; take one before running.
- **Search repair is not automatic.** The KV write happens before the search publish. If the publish fails, the member already has role `None`, so a re-run skips it and can report `Failed: 0` without sending the missing search update. Check the logs for publish warnings; repairing them means republishing the affected member records to the indexer by other means (this script will not do it).
- Dry-run is the only preview; there is no per-committee scoping.
- Writes go straight to KV, bypassing the service's validation, FGA and event publishing.
