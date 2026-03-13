# NATS Key-Value Storage Patterns

This service uses NATS JetStream key-value (KV) buckets as its database. Think of each bucket as a table — you store and retrieve JSON-encoded data by a string key (usually a UID).

## The Three Buckets

| Bucket | Constant | Stores |
|--------|----------|--------|
| `committees` | `constants.KVBucketNameCommittees` | Committee base data |
| `committee-settings` | `constants.KVBucketNameCommitteeSettings` | Committee settings |
| `committee-members` | `constants.KVBucketNameCommitteeMembers` | Committee member data |

## Adding a New Bucket

Each new data entity that needs its own storage requires a bucket. You need to create it in two places: locally for development, and in the Helm chart for Kubernetes deployments.

### 1. Local development (nats CLI)

Create the bucket manually using the `nats` CLI once your local NATS server is running:

```bash
nats kv add <bucket-name> \
  --history=20 \
  --storage=file \
  --max-value-size=10485760 \
  --max-bucket-size=1073741824
```

Replace `<bucket-name>` with the actual bucket name (e.g. `committee-invites`). Use the same defaults as the existing buckets: history=20, file storage, 10MB max value size, 1GB max bucket size.

### 2. Kubernetes deployment (Helm chart)

Two files must be updated:

**Step 1 — Add the bucket config to `charts/lfx-v2-committee-service/values.yaml`:**

```yaml
  # <your_entity>_kv_bucket is the configuration for the KV bucket for storing <your_entity>
  <your_entity>_kv_bucket:
    creation: true
    keep: true
    name: <bucket-name>
    history: 20
    storage: file
    maxValueSize: 10485760  # 10MB
    maxBytes: 1073741824    # 1GB
    compression: true
```

Follow the naming convention: `<entity>_kv_bucket` for the key (e.g. `committee_invites_kv_bucket`).

**Step 2 — Add a `KeyValue` CRD block to `charts/lfx-v2-committee-service/templates/nats-kv-buckets.yaml`:**

```yaml
---
{{- if .Values.nats.<your_entity>_kv_bucket.creation }}
apiVersion: jetstream.nats.io/v1beta2
kind: KeyValue
metadata:
  name: {{ .Values.nats.<your_entity>_kv_bucket.name }}
  namespace: {{ .Release.Namespace }}
  {{- if .Values.nats.<your_entity>_kv_bucket.keep }}
  annotations:
    "helm.sh/resource-policy": keep
  {{- end }}
spec:
  bucket: {{ .Values.nats.<your_entity>_kv_bucket.name }}
  history: {{ .Values.nats.<your_entity>_kv_bucket.history }}
  storage: {{ .Values.nats.<your_entity>_kv_bucket.storage }}
  maxValueSize: {{ .Values.nats.<your_entity>_kv_bucket.maxValueSize }}
  maxBytes: {{ .Values.nats.<your_entity>_kv_bucket.maxBytes }}
  compression: {{ .Values.nats.<your_entity>_kv_bucket.compression }}
{{- end }}
```

Add a `---` separator before each new block. The `keep: true` annotation tells Helm to preserve the bucket (and its data) when the chart is uninstalled — always set this to `true` for production buckets.

### 3. Register the bucket constant

Add the bucket name as a constant in `pkg/constants/storage.go` alongside the existing ones, then initialize it in the NATS client in `internal/infrastructure/nats/client.go`.

## How Data Flows

The storage layer lives in `internal/infrastructure/nats/storage.go`. It implements the port interfaces from `internal/domain/port/`.

All storage operations follow this pattern:
1. Marshal the domain model to JSON (`json.Marshal`)
2. Call the NATS KV method (`Create`, `Put`, `Get`, `Delete`)
3. Handle errors using `pkg/errors/`
4. Log debug info with `slog.DebugContext`

## Port Interfaces

The service layer never calls NATS directly — it calls interfaces:

```go
// CommitteeBaseReader — read operations
type CommitteeBaseReader interface {
    GetBase(ctx context.Context, uid string) (*model.CommitteeBase, uint64, error)
    GetRevision(ctx context.Context, uid string) (uint64, error)
}

// CommitteeBaseWriter — write operations
type CommitteeBaseWriter interface {
    Create(ctx context.Context, committee *model.Committee) error
    UpdateBase(ctx context.Context, committee *model.Committee, revision uint64) error
    Delete(ctx context.Context, uid string, revision uint64) error
    UniqueNameProject(ctx context.Context, committee *model.Committee) (string, error)
    UniqueSSOGroupName(ctx context.Context, committee *model.Committee) (string, error)
}
```

When adding a new storage operation, **add it to the interface first**, then implement it in `storage.go`, then add a mock in `internal/infrastructure/mock/`.

## NATS KV Operations

### Create (insert, fails if key exists)
```go
rev, err := s.client.kvStore[constants.KVBucketNameCommittees].Create(ctx, uid, dataBytes)
if err != nil {
    if errors.Is(err, jetstream.ErrKeyExists) {
        return errs.NewConflict("committee already exists")
    }
    return errs.NewUnexpected("failed to create committee", err)
}
```

### Get (read by key)
```go
entry, err := s.client.kvStore[constants.KVBucketNameCommittees].Get(ctx, uid)
if err != nil {
    if errors.Is(err, jetstream.ErrKeyNotFound) {
        return nil, 0, errs.NewNotFound("committee not found")
    }
    return nil, 0, errs.NewUnexpected("failed to get committee", err)
}
var base model.CommitteeBase
if err := json.Unmarshal(entry.Value(), &base); err != nil {
    return nil, 0, errs.NewUnexpected("failed to unmarshal committee", err)
}
return &base, entry.Revision(), nil
```

### Update (optimistic concurrency — requires revision number)
```go
rev, err := s.client.kvStore[constants.KVBucketNameCommittees].Update(ctx, uid, dataBytes, revision)
if err != nil {
    return errs.NewUnexpected("failed to update committee", err)
}
```

### Delete
```go
err := s.client.kvStore[constants.KVBucketNameCommittees].Delete(ctx, uid, jetstream.LastRevision(revision))
if err != nil {
    return errs.NewUnexpected("failed to delete committee", err)
}
```

## The Revision Number

Every NATS KV entry has a revision number that increments on each write. Updates and deletes require passing the current revision — this is **optimistic concurrency control**. If two processes try to update the same entry simultaneously, only the first one succeeds; the second gets an error because the revision they passed no longer matches.

This is why `GetBase` returns `(model, revision, error)` — callers need to pass that revision to subsequent updates.

## Error Types

Always use `pkg/errors/` for errors, not raw Go errors:
- `errs.NewNotFound("message")` — 404, resource doesn't exist
- `errs.NewConflict("message")` — 409, uniqueness violation
- `errs.NewValidation("message")` — 400, bad input
- `errs.NewUnexpected("message", cause)` — 500, unexpected error

## Logging

Use structured logging for storage operations:
```go
slog.DebugContext(ctx, "created committee in NATS storage",
    "committee_uid", uid,
    "revision", rev,
)
```

Use `DebugContext` for successful operations, `WarnContext` for recoverable issues.

## Adding Storage to a Mock (for tests)

When you add a method to a port interface, also add it to the corresponding mock in `internal/infrastructure/mock/`. Mocks implement the same interface but store data in memory, allowing tests to run without a real NATS server.
