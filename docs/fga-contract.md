# FGA Contract — Committee Service

This document is the authoritative reference for all messages the committee service sends to the fga-sync service, which writes and deletes [OpenFGA](https://openfga.dev/) relationship tuples to enforce access control.

The full OpenFGA type definitions (relations, schema) for all object types are defined in the [platform model](https://github.com/linuxfoundation/lfx-v2-helm/blob/main/charts/lfx-platform/templates/openfga/model.yaml).

**Update this document in the same PR as any change to FGA message construction.**

---

## Object Types

- [Committee](#committee)
- [Committee Application](#committee-application)
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

### Delivery and `X-Sync`

`lfx.fga-sync.update_access`, `lfx.fga-sync.delete_access`, `lfx.fga-sync.member_put`, and `lfx.fga-sync.member_remove` are always sent with core NATS publish. Publication is asynchronous: `X-Sync: true` does not wait for fga-sync processing or OpenFGA convergence. Indexer messages are also always published fire-and-forget (see [Indexer Contract — Delivery](indexer-contract.md#delivery)); `X-Sync` no longer routes any publish through request/reply.

For HTTP committee and committee-invite writes, `update_access` publication remains best-effort. An immediate readiness, serialization, or NATS publish error is logged, but preserves the endpoint's existing response behavior after the resource operation succeeds.

Committee deletion preserves its stricter existing error behavior: storage deletion occurs first, then an immediate NATS readiness, serialization, or core-publish error is returned to the HTTP layer. Making `delete_access` asynchronous eliminates its FGA request/reply and reply-timeout errors; indexer publishes are also fire-and-forget (no request/reply path), so their reply-timeout and request/reply errors are also eliminated — but immediate readiness, serialization, and core-publish errors still surface synchronously from `Indexer`. The defensive committee-invite delete branch remains best-effort and logs publication failures.

A successful core publish means only that the NATS client accepted the message for delivery (no immediate client-side error); it is not a broker acknowledgement, and it does not mean that fga-sync or OpenFGA finished processing it.

`member_put` and `member_remove` publication follows the same asymmetry as committee writes: create and update publish errors are logged and best-effort, while `DeleteMember` still returns an immediate publish error after the member record is deleted. `AcceptInvite` no longer uses request/reply for `member_put`; access checks issued immediately after acceptance may not yet see the membership.

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

When enrichment runs, caller-supplied usernames are not trusted: the service uses its email-to-username lookup and the stored-identity fallback described below. Within one create or update request, a confirmed lookup result (account found or confirmed absent) for the member's current email is reused instead of asking auth-service again; a failed lookup is retried once in the writer and is never treated as a confirmed absence. On update, when no account is found or the lookup fails, the stored username is kept if the email did not change and cleared if it did, so a confirmed absence leaves an email-only member email-only and publishes no `member_put`. Requests that skip enrichment (NATS LFID invite-accepted reconciliation via `lfx.invite-service.invite_accepted`, user events, sync tooling, `X-Skip-Enrichment`) make no identity lookup in the writer and publish the supplied identity unchanged; user-event and sync-tooling callers may perform their own lookup before invoking the writer. HTTP `AcceptInvite` uses normal server-side enrichment and reuses the confirmed lookup result.

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

Published to `lfx.fga-sync.member_remove` when a committee member is deleted and the member has a non-empty `Username`. The service first verifies that the stored member belongs to the requested committee; a mismatch returns not found without deleting the member or publishing messages. Sends an empty `relations` array, which instructs fga-sync to remove all tuples for that user on the committee object.

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

## Committee Application

**Source struct:** `internal/domain/model/CommitteeApplication`

**Synced on:** submit (create or reapply), approve, and reject of a committee application, and cascade `delete_access` when the parent committee is deleted.

### update_access (Submit / Approve / Reject)

Published to `lfx.fga-sync.update_access` whenever a `committee_application` object is created or updated.

#### Message Envelope

| Field | Value |
|---|---|
| `object_type` | `committee_application` |
| `operation` | `update_access` |

#### Data Fields

| Field | Value |
|---|---|
| `uid` | `CommitteeApplication.UID` |

#### Relations

| Relation | Value | Condition |
|---|---|---|
| `applicant` | LFID username resolved from `CommitteeApplication.ApplicantEmail` | Only when email resolves to an LFID username |

> When the application is submitted and the applicant has no LFID yet, the `applicant` relation is omitted. `exclude_relations: ["applicant"]` is set so fga-sync does not delete a previously-written tuple on a transient auth-service outage.

#### References

| Reference | Value | Condition |
|---|---|---|
| `committee` | `CommitteeApplication.CommitteeUID` | Always |

### Delete

When a committee is deleted, `committeeWriterOrchestrator.Delete` lists all `committee_application` objects for that committee and for each one emits:

- An indexer `ActionDeleted` message to `lfx.index.committee_application` (removes the OpenSearch document).
- A `delete_access` message to `lfx.fga-sync.delete_access` with `object_type: committee_application` (removes all FGA tuples for that application).

Both messages are published fire-and-forget (`X-Sync: false`) as part of the same concurrent worker pool as the committee's own deletion messages. The application's KV record is also removed from storage in the same pool call. If `ListApplications` fails the cascade is skipped with a critical-priority warning; the committee delete itself still succeeds. There is no explicit withdraw or delete endpoint — cascade on committee delete is the only current callsite for `delete_access` on `committee_application`.

When the store removes a committee's children itself (the PostgreSQL store's `DeleteCommitteeCascade`), the applications
are the ones it reports as removed, `ListApplications` is not called and no per-application record delete is made; the
`delete_access` message per application is unchanged. Members are tuples on the committee, so the committee's
`delete_access` covers them and no `member_remove` is sent for the members a cascade removes. Invites do have an object
of their own (`committee_invite`), so each removed invite also gets a `delete_access` with `object_type: committee_invite`:
the defensive branch described under [delete_access (Delete)](#delete_access-delete), of which a cascade is the first
caller. A plain invite delete keeps the object so history queries work; after a cascade the invite and its index entry
are gone, so nothing could read those tuples. The application and invite messages are published after the committee's own, on a
context detached from the request, at most 32 at a time, and a failure of one does not skip the rest.

### Event Summary

| Event | Object Type | Subject | Notes |
|---|---|---|---|
| Submit committee application | `committee_application` | `lfx.fga-sync.update_access` | `applicant` relation omitted when applicant has no LFID yet |
| Reapply (reinstated from rejected) | `committee_application` | `lfx.fga-sync.update_access` | Same applicant-resolution logic |
| Approve committee application | `committee_application` | `lfx.fga-sync.update_access` | Tuple retained so applicant can still view their approved application |
| Reject committee application | `committee_application` | `lfx.fga-sync.update_access` | Tuple retained so applicant can still view their rejected application |
| Delete parent committee (cascade) | `committee_application` | `lfx.fga-sync.delete_access` + `lfx.index.committee_application` | One pair per application, plus KV record deletion (none when the store cascades); fire-and-forget |

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

The invite access helper retains a defensive `delete_access` branch that publishes to `lfx.fga-sync.delete_access` and removes all FGA tuples for the `committee_invite:{uid}` object. Only the committee cascade of a store that removes the committee's children itself (the PostgreSQL store) invokes
this branch; on a plain invite delete, and with the KV store, it has no callsite.

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
| Delete committee invite | `committee_invite` | `lfx.fga-sync.delete_access` | Defensive branch only; no callsite on a plain invite delete (see [delete_access](#delete_access-delete)) |
| Delete parent committee (cascade to invites) | `committee_invite` | `lfx.fga-sync.delete_access` | One message per invite, only when the store removes the committee's children itself (PostgreSQL); fire-and-forget |
| LFID registered (`lfx.invite-service.invite_accepted`) | `committee_invite` | `lfx.fga-sync.update_access` | Publishes `invitee` relation for every `committee_invite` (any status) whose `InviteeEmail` matches the accepted email — grants visibility to all invites, including already-accepted ones |
| Submit committee application | `committee_application` | `lfx.fga-sync.update_access` | `applicant` relation omitted when applicant has no LFID yet |
| Reapply (reinstated from rejected) | `committee_application` | `lfx.fga-sync.update_access` | Same applicant-resolution logic as submit |
| Approve committee application | `committee_application` | `lfx.fga-sync.update_access` | Tuple retained so applicant can view their approved application |
| Reject committee application | `committee_application` | `lfx.fga-sync.update_access` | Tuple retained so applicant can view their rejected application |
| Delete committee (cascade to applications) | `committee_application` | `lfx.fga-sync.delete_access` | One message per application plus KV record deletion; fire-and-forget; skipped if `ListApplications` fails. With a store that cascades, the applications come from the store and there is no `ListApplications` or KV record deletion |
