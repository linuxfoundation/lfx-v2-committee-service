# split_member_index

Migration script that backfills the two-index structure introduced when the
single `committee_member` index was split into:

- **`lfx.index.committee_member`** — roster index (non-sensitive, gated by `roster_viewer`)
- **`lfx.index.committee_member_sensitive`** — email index (sensitive, gated by `email_viewer`)

Existing documents indexed under the old single-index structure
(`AccessCheckRelation: "viewer"`) must be re-published so the indexer writes
both indexes with the correct access/history check relations.

## When to run

Run this once after deploying the service version that introduces the
`roster_viewer` / `email_viewer` split. It reads every committee member from the
NATS KV store and re-publishes two indexer messages per member.

## Usage

```bash
# Dry run (default) — log what would be published without sending anything
NATS_URL=nats://localhost:4222 \
  go run ./scripts/migrations/split_member_index/

# Live run
NATS_URL=nats://localhost:4222 \
  go run ./scripts/migrations/split_member_index/ --dry-run=false
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `true` | Log what would be published without actually publishing |

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222` | NATS server URL |

## Rollback

To undo this migration, run the companion script:

```bash
NATS_URL=nats://localhost:4222 \
  go run ./scripts/migrations/rollback_split_member_index/ --dry-run=false
```

The rollback script restores the pre-split state by:
1. Re-publishing every member to `lfx.index.committee_member` with
   `AccessCheckRelation: "viewer"` and the email included in the payload.
2. Publishing `ActionDeleted` for every member to
   `lfx.index.committee_member_sensitive` so the email documents are removed.
