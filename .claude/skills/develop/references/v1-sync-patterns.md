# V1 Sync Patterns

When a data model change in this service (e.g. adding a new field to `CommitteeBase`) also needs to be reflected in the V1 system, three repos must be updated together. This document describes where exactly to make changes in `lfx-v1-sync-helper`.

## Repos involved

| Repo | Role |
| ---- | ---- |
| [`lfx-v2-committee-service`](https://github.com/linuxfoundation/lfx-v2-committee-service) | V2 domain model and API (this repo) |
| [`project-management`](https://github.com/linuxfoundation/project-management) | V1 API and data model |
| [`lfx-v1-sync-helper`](https://github.com/linuxfoundation/lfx-v1-sync-helper) | Sync service that bridges V1 ↔ V2 |

## V2 → V1 direction (write happens in V2)

When a committee is created or updated in V2, the data is indexed in OpenSearch and triggers the sync helper to write it to V1 (PostgreSQL via the V1 Project Service API).

**File:** [`cmd/lfx-v1-sync-helper/ingest_indexer.go`](https://github.com/linuxfoundation/lfx-v1-sync-helper/blob/main/cmd/lfx-v1-sync-helper/ingest_indexer.go)

The relevant functions that build the V1 payload from V2 data are:

- `syncCommitteeCreateToV1` — handles committee create events from V2
- `syncCommitteeUpdateToV1` — handles committee update events from V2

Add your new field to the `projectServiceCommitteeCreate` / `projectServiceCommitteeUpdate` payload struct and map it from the V2 `data` map inside these functions. Example:

```go
// In syncCommitteeUpdateToV1, add to the payload:
ChatChannel: data["chat_channel"],
```

If the field requires a value transformation between V2 and V1 formats (like `category` does), add a dedicated mapper function following the pattern of `mapV2CategoryToV1`.

## V1 → V2 direction (write happens in V1)

When a committee is created or updated in V1 (sourced from Salesforce), the sync helper calls the V2 committee service API to upsert the record.

**File:** [`cmd/lfx-v1-sync-helper/handlers_committees.go`](https://github.com/linuxfoundation/lfx-v1-sync-helper/blob/main/cmd/lfx-v1-sync-helper/handlers_committees.go)

The relevant functions that build the V2 payload from V1 Salesforce data are:

- `mapV1DataToCommitteeCreatePayload` — used when creating a committee in V2 from a V1 event
- `mapV1DataToCommitteeUpdateBasePayload` — used when updating a committee in V2 from a V1 event

Add your new field to the `CreateCommitteePayload` / `UpdateCommitteeBasePayload` struct mapping inside these functions. The V1 field names come from the raw Salesforce field map (e.g. `v1Data["chat_channel__c"]`). Example:

```go
// In mapV1DataToCommitteeCreatePayload:
ChatChannel: v1Data["chat_channel__c"],
```

If the field requires a value transformation (like `type__c` → category does via `mapTypeToCategory`), add a dedicated mapper function.

## Module versioning when working in parallel

The `lfx-v1-sync-helper` imports this repo as a Go module to use the V2 data model types:

```
github.com/linuxfoundation/lfx-v2-committee-service vX.Y.Z
```

If your V2 model changes haven't been released and tagged yet, the new fields won't be visible in the sync helper. To develop both in parallel, point the sync helper's `go.mod` to your branch:

```bash
# In lfx-v1-sync-helper
go get github.com/linuxfoundation/lfx-v2-committee-service@<your-branch-or-commit-sha>
```

Revert to a tagged version before merging the sync helper PR. Coordinate timing so the V2 tag exists before the sync helper is merged.

## Checklist for a cross-system field change

- [ ] Add the field to the V2 domain model (`internal/domain/model/`)
- [ ] Expose it in the V2 API design (`cmd/committee-api/design/`)
- [ ] Add the field to the V1 data model and API in `project-management`
- [ ] Update `syncCommitteeCreateToV1` / `syncCommitteeUpdateToV1` in `ingest_indexer.go`
- [ ] Update `mapV1DataToCommitteeCreatePayload` / `mapV1DataToCommitteeUpdateBasePayload` in `handlers_committees.go`
- [ ] If V2 changes are not yet tagged, update `go.mod` in the sync helper to point to the branch; revert before merging
