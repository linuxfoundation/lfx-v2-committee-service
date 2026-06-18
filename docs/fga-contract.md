# FGA Contract — Committee Service

This document is the authoritative reference for all messages the committee service sends to the fga-sync service, which writes and deletes [OpenFGA](https://openfga.dev/) relationship tuples to enforce access control.

The full OpenFGA type definitions (relations, schema) for all object types are defined in the [platform model](https://github.com/linuxfoundation/lfx-v2-helm/blob/main/charts/lfx-platform/templates/openfga/model.yaml).

**Update this document in the same PR as any change to FGA message construction.**

---

## Object Types

- [Committee](#committee)
- [Committee Invite](#committee-invite)

---

## Message Format

All messages use the generic FGA message format on the following NATS subjects:

| Subject | Used for |
|---|---|
| `lfx.fga-sync.update_access` | Create and update operations |
| `lfx.fga-sync.delete_access` | Delete operations |
| `lfx.fga-sync.member_put` | Add or update individual committee members |
| `lfx.fga-sync.member_remove` | Remove individual committee members |

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

> Usernames are the `Username` field of each `CommitteeUser` entry (LFX usernames). When only an email is provided, the service resolves the username via `lfx.auth-service.email_to_username` before publishing. Users with an empty `Username` are skipped.

#### References

| Reference | Value | Condition |
|---|---|---|
| `project` | `CommitteeBase.ProjectUID` | Always |

#### Exclude Relations

`exclude_relations: ["member"]` — always set. Individual committee members are managed via `member_put` and must not be overwritten by the `update_access` handler.

### member_put (Committee Member Create/Update)

Published to `lfx.fga-sync.member_put` when a committee member is created or updated and the member has a non-empty `Username`.

The object UID is the **committee UID** (`CommitteeBase.UID`), not the member UID.

#### Message Envelope

| Field | Value |
|---|---|
| `object_type` | `committee` |
| `operation` | `member_put` |

#### Data (`GenericMemberData`)

| Field | Value | Condition |
|---|---|---|
| `uid` | `CommitteeMember.CommitteeUID` (parent committee) | Always |
| `username` | `CommitteeMember.Username` (LFX username) | Always (skipped if `Username` is empty) |
| `relations` | `["member"]` | Always |

### member_remove (Committee Member Delete)

Published to `lfx.fga-sync.member_remove` when a committee member is deleted and the member has a non-empty `Username`. Sends an empty `relations` array, which instructs fga-sync to remove all tuples for that user on the committee object.

#### Message Envelope

| Field | Value |
|---|---|
| `object_type` | `committee` |
| `operation` | `member_remove` |

#### Data (`GenericMemberData`)

| Field | Value | Condition |
|---|---|---|
| `uid` | `CommitteeMember.CommitteeUID` (parent committee) | Always |
| `username` | `CommitteeMember.Username` (LFX username) | Always (skipped if `Username` is empty) |
| `relations` | `[]` (empty — remove all) | Always |

### Delete

On delete, a `delete_access` message is sent to `lfx.fga-sync.delete_access` with only the committee `uid` — all FGA tuples for `committee:{uid}` are removed by the fga-sync service.

---

## Committee Invite

**Source struct:** `internal/domain/model/CommitteeInvite`

**Synced on:** create/update of a committee invite (HTTP path), acceptance of a committee invite (HTTP path), and NATS `lfx.invite-service.invite_accepted` event (grants `invitee` relation to newly registered LFID users).

### update_access (Create / Accept / LFID Registration)

Published to `lfx.fga-sync.update_access` whenever a `committee_invite` object is created, updated, or when a previously-invited email address registers an LFID (via `HandleInviteAccepted`).

#### Message Envelope

| Field | Value |
|---|---|
| `object_type` | `committee_invite` |
| `operation` | `update_access` |

#### Data Fields

| Field | Value |
|---|---|
| `uid` | `CommitteeInvite.UID` |

#### Relations

| Relation | Value | Condition |
|---|---|---|
| `invitee` | LFID username resolved from `CommitteeInvite.InviteeEmail` | Only when email resolves to an LFID username |

> When the invite is created and the invitee has no LFID yet, the `invitee` relation is omitted. `exclude_relations: ["invitee"]` is set so fga-sync does not delete a previously-written tuple on a transient auth-service outage. The tuple is written retroactively on `lfx.invite-service.invite_accepted` once the invitee creates an LFID — covering all invites for that email regardless of status, so the user can see their full invite history.

#### References

| Reference | Value | Condition |
|---|---|---|
| `committee` | `CommitteeInvite.CommitteeUID` | Always |

### delete_access (Delete)

Published to `lfx.fga-sync.delete_access` when a committee invite is deleted. Removes all FGA tuples for the `committee_invite:{uid}` object.

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
| Delete committee member (with username) | `committee` | `lfx.fga-sync.member_remove` | Skipped if `Username` is empty; empty relations removes all tuples for the user |
| Create committee invite | `committee_invite` | `lfx.fga-sync.update_access` | `invitee` relation omitted when invitee has no LFID yet |
| Update committee invite | `committee_invite` | `lfx.fga-sync.update_access` | Same invitee-resolution logic as create |
| Accept committee invite (HTTP) | `committee_invite` | `lfx.fga-sync.update_access` | Re-publishes to ensure `invitee` tuple is present after acceptance |
| Delete committee invite | `committee_invite` | `lfx.fga-sync.delete_access` | Removes all tuples for the invite object |
| LFID registered (`lfx.invite-service.invite_accepted`) | `committee_invite` | `lfx.fga-sync.update_access` | Publishes `invitee` relation for every `committee_invite` (any status) whose `InviteeEmail` matches the accepted email — grants visibility to all invites, including already-accepted ones |
