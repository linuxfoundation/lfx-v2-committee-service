# FGA Contract — Committee Service

This document is the authoritative reference for all messages the committee service sends to the fga-sync service, which writes and deletes [OpenFGA](https://openfga.dev/) relationship tuples to enforce access control.

The full OpenFGA type definitions (relations, schema) for all object types are defined in the [platform model](https://github.com/linuxfoundation/lfx-v2-helm/blob/main/charts/lfx-platform/templates/openfga/model.yaml).

**Update this document in the same PR as any change to FGA message construction.**

---

## Object Types

- [Committee](#committee)

---

## Message Format

All messages use the generic FGA message format on the following NATS subjects:

| Subject | Used for |
|---|---|
| `lfx.fga-sync.update_access` | Create and update operations |
| `lfx.fga-sync.delete_access` | Delete operations |
| `lfx.fga-sync.member_put` | Add or remove individual committee members |

Each message carries `object_type`, `operation`, and a `data` map. The sections below describe the `data` contents for each operation.

---

## Committee

**Source struct:** `internal/domain/model/` — `Committee` (base + settings)

**Synced on:** create, update of committee base, update of committee settings, delete of a committee. Committee member changes are synced separately via `member_put`.

### update_access

Published to `lfx.fga-sync.update_access` on committee create or update (base or settings).

#### Message Envelope

| Field | Value |
|---|---|
| `object_type` | `committee` |
| `operation` | `update_access` |

#### Data Fields

These fields are carried inside the message `data` object.

| Field | Value |
|---|---|
| `uid` | `CommitteeBase.UID` |
| `public` | `CommitteeBase.Public` (passed through directly) |

#### Relations

| Relation | Value | Condition |
|---|---|---|
| `writer` | Usernames from `CommitteeSettings.Writers` | Only when `Writers` is non-empty |
| `auditor` | Usernames from `CommitteeSettings.Auditors` | Only when `Auditors` is non-empty |

> Usernames are the `Username` field of each `CommitteeUser` entry (Auth0 `sub` values). Users with an empty `Username` are skipped.

#### References

| Reference | Value | Condition |
|---|---|---|
| `project` | `CommitteeBase.ProjectUID` | Always |

#### Exclude Relations

`exclude_relations: ["member"]` — always set. Individual committee members are managed via `member_put` and must not be overwritten by the `update_access` handler.

### member_put (Committee Member)

Published to `lfx.fga-sync.member_put` when a committee member is created, updated, or deleted and the member has a non-empty `Username`.

The object UID is the **committee UID** (`CommitteeBase.UID`), not the member UID.

#### Member Data

| Field | Value | Condition |
|---|---|---|
| `object_type` | `committee` | Always |
| `uid` | `CommitteeMember.CommitteeUID` (parent committee) | Always |
| `username` | `CommitteeMember.Username` (Auth0 `sub`) | Always (skipped if `Username` is empty) |
| `relations` | `["member"]` | When action is create or update |
| `relations` | `[]` (empty) | When action is delete |
| `mutually_exclusive_with` | `["member"]` | Only when action is delete |

### Delete

On delete, a `delete_access` message is sent to `lfx.fga-sync.delete_access` with only the committee `uid` — all FGA tuples for `committee:{uid}` are removed by the fga-sync service.

---

## Triggers

| Operation | Object Type | Subject | Notes |
|---|---|---|---|
| Create committee | `committee` | `lfx.fga-sync.update_access` | Always sent |
| Update committee base | `committee` | `lfx.fga-sync.update_access` | Always sent |
| Update committee settings | `committee` | `lfx.fga-sync.update_access` | Always sent |
| Delete committee | `committee` | `lfx.fga-sync.delete_access` | Always sent |
| Create committee member (with username) | `committee` | `lfx.fga-sync.member_put` | Skipped if `Username` is empty |
| Update committee member (with username) | `committee` | `lfx.fga-sync.member_put` | Skipped if `Username` is empty |
| Delete committee member (with username) | `committee` | `lfx.fga-sync.member_put` | Skipped if `Username` is empty; sends empty relations to remove |
