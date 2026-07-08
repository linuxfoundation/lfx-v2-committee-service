# committee-cli

A CLI tool for running operational tasks against the committee service. Commands are designed to be run manually by engineers or as one-off Kubernetes Jobs after incidents or data migrations.

## Why this exists

As services grow, there is a recurring need to reprocess data, apply corrections, or back-fill attributes — either to fix bugs, recover from incidents, or support new features. The CLI pattern addresses this by providing a single binary with shared infrastructure wiring, a consistent command/subcommand structure, dry-run support, structured output, and a deterministic exit code.

This is intended to replace the `scripts/migrations/` folder over time. Individual scripts are a simpler starting point, but they do not scale well: each one duplicates connection setup, lacks a shared contract for dry-run and statistics, and is harder to test. The CLI reuses the existing service and domain layers, which means every command benefits from the same NATS wiring, port abstractions, and orchestrators already in production.

## Usage

```text
committee-cli <command> <subcommand> [subcommand flags]
```

### Environment variables

| Env var | Default | Description |
|---|---|---|
| `NATS_URL` | `nats://localhost:4222` | NATS server address |
| `QUERY_SERVICE_URL` | `""` | Query-service base URL (required for `sync member-cdp-org-id`) |
| `AUTH_TOKEN` | `""` | Bearer token for query-service (required for `sync member-cdp-org-id`) |
| `LOG_LEVEL` | `debug` | Log verbosity (e.g. `info`) |

### Commands

#### `sync total-members-attribute`

Reconciles `CommitteeBase.TotalMembers` against the actual member count in the KV store. Drift can occur when a member event is missed or a consumer is lagged.

**Subcommand flags**

| Flag | Default | Description |
|---|---|---|
| `--committee-uid` | `""` | Limit sync to a single committee |
| `--project-uid` | `""` | Limit sync to committees belonging to a project |
| `--sleep` | `0` | Wait between each update to reduce indexer pressure (e.g. `200ms`, `1s`) |
| `--dry-run` | `false` | Compute diffs without writing |

**Exit code:** `0` if no committees failed, `1` otherwise.

**Output:** Structured JSON log line on completion with fields `total`, `updated`, `skipped`, `failed`, `duration_ms`, `rate_per_sec`.

**Examples**

Dry-run across all committees (safe first step):
```sh
NATS_URL=nats://localhost:4222 LOG_LEVEL=debug \
  committee-cli sync total-members-attribute --dry-run
```

Full run with a 200ms pause between updates:
```sh
NATS_URL=nats://localhost:4222 \
  committee-cli sync total-members-attribute --sleep=200ms
```

Target a single committee:
```sh
NATS_URL=nats://localhost:4222 \
  committee-cli sync total-members-attribute --committee-uid=abc-123
```

#### `sync members-by-committee-index`

Backfills the committee→member secondary index (`lookup/committee-members-by-committee/<committeeUID>.<memberUID>`) for members that existed before the index was introduced. The new `ListMembersByCommittee` implementation reads exclusively from this index, so this command must be run against each environment before deploying the updated service.

**Subcommand flags**

| Flag | Default | Description |
|---|---|---|
| `--committee-uid` | `""` | Limit backfill to members of a single committee |
| `--sleep` | `0` | Wait between each write to reduce pressure (e.g. `200ms`, `1s`) |
| `--dry-run` | `false` | Log what would be written without writing |

**Exit code:** `0` if no members failed, `1` otherwise.

**Output:** Structured JSON log line on completion with fields `total`, `updated`, `skipped`, `failed`, `duration_ms`, `rate_per_sec`.

The command is idempotent — index entries are written with write-if-absent semantics and `ErrKeyExists` is treated as success, so re-running is safe.

**Examples**

Dry-run to preview scope (safe first step):
```sh
NATS_URL=nats://localhost:4222 LOG_LEVEL=info \
  committee-cli sync members-by-committee-index --dry-run
```

Full backfill with a 100ms pause between writes:
```sh
NATS_URL=nats://localhost:4222 \
  committee-cli sync members-by-committee-index --sleep=100ms
```

Backfill a single committee:
```sh
NATS_URL=nats://localhost:4222 \
  committee-cli sync members-by-committee-index --committee-uid=abc-123
```

#### `sync reindex-invites`

Re-publishes all committee invites from NATS KV to OpenSearch (via the indexer) and OpenFGA (via fga-sync). For each invite, the command fetches the parent committee's base and settings, backfills `committee_name` (if missing) and recomputes `organization_required` (`enable_voting || business_email_required`), persists any changed fields back to NATS KV, then publishes the updated invite to the indexer and access-control subjects.

Committee base and settings are fetched once per unique committee UID and cached for the duration of the run. If the committee fetch fails the invite is published as-is without modifying its stored fields.

**Subcommand flags**

| Flag | Default | Description |
|---|---|---|
| `--committee-uid` | `""` | Limit reindex to invites of a single committee |
| `--sleep` | `0` | Wait between each invite publish (e.g. `200ms`, `1s`) |
| `--dry-run` | `false` | Log what would be published and updated without writing |

**Exit code:** `0` if no invites failed, `1` otherwise.

**Output:** Structured JSON log line on completion with fields `total`, `updated`, `skipped`, `failed`, `duration_ms`, `rate_per_sec`.

**Examples**

Dry-run to preview scope (safe first step):
```sh
NATS_URL=nats://localhost:4222 LOG_LEVEL=info \
  committee-cli sync reindex-invites --dry-run
```

Full reindex with a 200ms pause between invites:
```sh
NATS_URL=nats://localhost:4222 \
  committee-cli sync reindex-invites --sleep=200ms
```

Reindex a single committee's invites:
```sh
NATS_URL=nats://localhost:4222 \
  committee-cli sync reindex-invites --committee-uid=abc-123
```

#### `sync member-cdp-org-id`

Repairs committee members that store a **CDP organization UUID** in `organization.id` (self-serve PR #779). Scans the committee-members KV bucket, resolves each affected org to the canonical **b2b_org Salesforce SFID** via query-service (`GET /query/resources?type=b2b_org`, matched by `primary_domain` / `website` / `name`), and updates the member through the writer orchestrator (reindexes + fixes the by-organization secondary index).

Tracked in [LFXV2-2647](https://linuxfoundation.atlassian.net/browse/LFXV2-2647).

**Subcommand flags**

| Flag | Default | Description |
|---|---|---|
| `--committee-uid` | `""` | Limit repair to members of a single committee |
| `--member-uid` | `""` | Limit repair to a single committee member |
| `--query-service-url` | `$QUERY_SERVICE_URL` | Override query-service base URL |
| `--clear-unresolved` | `false` | When SFID cannot be resolved, clear `organization.id` (keep name/website) |
| `--sleep` | `0` | Wait between each write (e.g. `200ms`, `1s`) |
| `--dry-run` | `true` | Log planned repairs without writing (pass `--dry-run=false` to apply) |

**Examples**

Dry-run:
```sh
NATS_URL=nats://localhost:4222 \
QUERY_SERVICE_URL=https://query-service.example \
AUTH_TOKEN=$TOKEN \
  committee-cli sync member-cdp-org-id
```

Apply repairs:
```sh
NATS_URL=nats://localhost:4222 \
QUERY_SERVICE_URL=https://query-service.example \
AUTH_TOKEN=$TOKEN \
  committee-cli sync member-cdp-org-id --dry-run=false --sleep=200ms
```

## Building

### Local binary

```sh
make build-cli
# produces bin/committee-cli
```

Or directly with Go:
```sh
go build -o bin/committee-cli ./cmd/committee-cli
```

### Docker image

```sh
make docker-build-cli
# tags: ghcr.io/linuxfoundation/lfx-v2-committee-service/committee-cli:<version>
#       ghcr.io/linuxfoundation/lfx-v2-committee-service/committee-cli:latest
```

In CI, the image is built and published automatically by the existing `ko-build-*.yaml` workflows alongside the API image.

## Running as a Kubernetes Job

```sh
kubectl create job lfx-committee-cli-sync-total-members \
  --image=ghcr.io/linuxfoundation/lfx-v2-committee-service/committee-cli:<tag> \
  --namespace=lfx \
  -- sync total-members-attribute --sleep=200ms
```

Monitor progress:
```sh
kubectl logs -f job/lfx-committee-cli-sync-total-members -n lfx
```

The Job is kept after completion so its logs and exit status remain accessible as a run record. You can re-trigger by creating a new Job with the same image.

## Adding new commands

1. Create `cmd/committee-cli/commands/<group>.go` and implement the `Command` and `Subcommand` interfaces from `command.go`.
2. Register the new command in `buildRegistry()` in `cmd/committee-cli/main.go`.

No changes to shared infrastructure or domain packages are required unless the new command needs a port method that does not yet exist.
