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
| `LOG_LEVEL` | `info` | Log verbosity (e.g. `debug`) |

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

## Building

### Local binary

```sh
make build-cli
# produces bin/committee-cli
```

Or directly with Go:
```sh
go build -o bin/committee-cli ./cmd/cli
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
