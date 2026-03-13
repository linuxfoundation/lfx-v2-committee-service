# NATS Key-Value Storage Patterns

This service uses NATS JetStream key-value (KV) buckets as its database. Think of each bucket as a table — you store and retrieve JSON-encoded data by a string key (usually a UID).

## The Three Buckets

| Bucket | Constant | Stores |
|--------|----------|--------|
| `committees` | `constants.KVBucketNameCommittees` | Committee base data |
| `committee-settings` | `constants.KVBucketNameCommitteeSettings` | Committee settings |
| `committee-members` | `constants.KVBucketNameCommitteeMembers` | Committee member data |

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
