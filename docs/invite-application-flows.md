# Invite & Application Flows

This document describes how committee membership is acquired, including the full lifecycle of invites and applications, allowed state transitions, and edge cases.

---

## Overview

Committees support four membership modes, configured via the `join_mode` field on the committee (stored on `CommitteeBase`):

| `join_mode` | How members join |
|-------------|-----------------|
| `closed` | Admin creates members directly via `POST /committees/{uid}/members` |
| `invite` | Admin creates an invite; invitee accepts it |
| `application` | User submits an application; reviewer approves it |
| `open` | User self-joins via `POST /committees/{uid}/join` |

Only one mode is active at a time. Endpoints that don't match the active `join_mode` return `403 Forbidden`.

---

## Closed Mode

When `join_mode: closed`, membership is entirely admin-controlled. There is no self-service path for users.

**How it works:**
- Admin calls `POST /committees/{uid}/members` with the new member's details.
- The member is created immediately with `status: Active`.
- Invites and applications are not accepted (`403 Forbidden`).

---

## Invite Flow

### Statuses

```
pending  ──accept──▶  accepted
pending  ──decline──▶ declined
pending  ──revoke──▶  revoked
declined ──accept──▶  accepted
declined ──revoke──▶  revoked
revoked  ──re-invite──▶ pending  (reinstates existing record)
```

### Endpoints

| Method | Path | Actor | Description |
|--------|------|-------|-------------|
| `POST` | `/committees/{uid}/invites` | Admin | Create a new invite (or reinstate a revoked one) |
| `GET` | `/committees/{uid}/invites/{invite_uid}` | Admin | Retrieve an invite |
| `POST` | `/committees/{uid}/invites/{invite_uid}/accept` | Invitee | Accept a pending or declined invite |
| `POST` | `/committees/{uid}/invites/{invite_uid}/decline` | Invitee | Decline a pending invite |
| `DELETE` | `/committees/{uid}/invites/{invite_uid}` | Admin | Revoke a pending or declined invite |

### Rules

**Creating an invite** (`POST /committees/{uid}/invites`):
- Creates a new invite with `status: pending`.
- If an invite for the same email already exists in this committee:
  - `status: revoked` — the existing invite is reinstated to `pending` (no new record created); role is updated if provided.
  - Any other status (`pending`, `declined`, `accepted`) — returns `409 Conflict`.
- After the invite record is persisted (create or reinstate), the service dispatches a best-effort send-invite request to the invite service (`lfx.invite-service.send_invite`, `dispatchInviteEmail` in `cmd/committee-api/service/committee_service.go`) so the invitee receives an email. The request uses the invite-service permission vocabulary with `role: "Member"` (the committee role on the invite record is applied after acceptance). Dispatch failures are logged and do not fail the API call.

**Accepting an invite** (`POST .../accept`):
- Only the invitee (matched by their primary email from the auth-service) can accept their own invite.
- Allowed from: `pending`, `declined`.
- Blocked from: `accepted` (already done), `revoked` (invite was withdrawn).
- On success: creates a committee member and marks the invite `accepted`. Member creation runs first — if it fails, the invite stays unchanged so the invitee can safely retry.
- Returns the created committee member.

**Declining an invite** (`POST .../decline`):
- Only the invitee can decline.
- Allowed from: `pending` only.
- A declined invite can later be accepted or revoked.

**Revoking an invite** (`DELETE /committees/{uid}/invites/{invite_uid}`):
- Admin action.
- Allowed from: `pending`, `declined`.
- Blocked from: `accepted` (member already exists), `revoked` (already revoked).
- A revoked invite can be reinstated by issuing a new `POST /committees/{uid}/invites` for the same email.

---

## Application Flow

### Statuses

```
pending  ──approve──▶ approved
pending  ──reject──▶  rejected
rejected ──reapply──▶ pending  (reinstates existing record)
```

### Endpoints

| Method | Path | Actor | Description |
|--------|------|-------|-------------|
| `POST` | `/committees/{uid}/applications` | Applicant | Submit an application (or reinstate a rejected one) |
| `GET` | `/committees/{uid}/applications/{application_uid}` | Admin / Applicant | Retrieve an application |
| `POST` | `/committees/{uid}/applications/{application_uid}/approve` | Reviewer | Approve a pending application |
| `POST` | `/committees/{uid}/applications/{application_uid}/reject` | Reviewer | Reject a pending application |

### Rules

**Submitting an application** (`POST /committees/{uid}/applications`):
- Only available when `join_mode: application`.
- The applicant's identity is resolved via the auth-service (see [Identity Resolution](#identity-resolution)).
- Creates a new application with `status: pending`.
- If an application for the same email already exists in this committee:
  - `status: rejected` — the existing application is reinstated to `pending` (no new record created); `reviewer_notes` are cleared and `message` is updated if provided.
  - Any other status (`pending`, `approved`) — returns `409 Conflict`.

**Approving an application** (`POST .../approve`):
- Only allowed when `status: pending`.
- On success: creates a committee member and marks the application `approved`. Member creation runs first — if it fails, the application stays `pending` so the reviewer can safely retry.
- Returns the created committee member.

**Rejecting an application** (`POST .../reject`):
- Only allowed when `status: pending`.
- Optionally accepts `reviewer_notes`.
- A rejected application can be resubmitted by the applicant (see above).

---

## Open Mode

When `join_mode: open`, any authenticated user can join without an invite or approval.

**How it works:**
- User calls `POST /committees/{uid}/join`.
- A committee member is created immediately with `status: Active`.

---

## Identity Resolution

Endpoints that act on behalf of the caller (accept/decline invite, submit application, join, leave) need the caller's **email address** to match records or create members. Because the JWT issued by Heimdall contains only the user's `principal` (subject identifier), the service resolves the email at request time via a NATS request/reply call to the auth-service:

- **Subject:** `lfx.auth-service.user_emails.read`
- **Request payload:** JSON — `{ "user": { "auth_token": "<principal>" } }` (see `UserEmailsNATSRequest` in `internal/infrastructure/nats/models.go`)
- **Response:** JSON with `{ "success": true, "data": { "primary_email": "...", "alternate_emails": [...] } }`

The service uses `primary_email` from the response. If the lookup fails (auth-service unavailable, principal unknown), the request is rejected with `400 Bad Request`.

---

## Idempotency Notes

Both the invite and application flows are designed so transient failures during the terminal step do not leave records in an unrecoverable state:

- **Member creation happens before the invite/application is marked terminal.** If member creation succeeds but the status update fails, the invite/application remains in its pre-terminal state and the caller can safely retry. Duplicate member creation is handled by the member uniqueness check.
- **Revoked invites and rejected applications are reinstateable**, so admin workflows that revoke-then-reinvite, or reviewer workflows that reject-then-reconsider, do not require manual record cleanup.

---

## LFID Invite Flow (Settings Writers / Auditors)

This is a separate flow from the committee invite API above. It handles users added to the committee's **settings** roles (Writers, Auditors) who do not yet have an LF ID (LFID) account.

### Overview

When a user is added to `writers` or `auditors` in committee settings via `PUT /committees/{uid}/settings`, the service branches on whether the user has an LFID:

| User state | `username` field | Action |
|---|---|---|
| Has LFID | non-empty | Send a direct role-notification email via the email service |
| No LFID | empty | Send an invite request to the invite service |

The invite service handles rendering and delivering the invite email, owns the invite record, and later publishes an enriched acceptance event that the committee service consumes to promote the user. The committee service does not persist any invite metadata for settings invites — it only logs the invite UID returned by the invite service.

### Sending an Invite

**Triggered by:** `HandleCommitteeSettingsUpdated` — called when a `lfx.committee-api.committee_settings.updated` event arrives and the diff contains added (or role-changed) non-LFID users.

**NATS subject used:** `lfx.invite-service.send_invite` (request/reply, 5-second timeout)

**Request payload** (`inviteapi.SendInviteRequest`, structured fields):

| Field | Value |
|---|---|
| `recipient.email` | User's email address |
| `recipient.name` | User's display name (falls back to username, then email) |
| `inviter.name` | Actor's display name; falls back to `"A committee administrator"` |
| `resource.uid` | Committee UID |
| `resource.name` | Committee name |
| `resource.type` | `"group"` |
| `role` | `"Manage"` (Writer) or `"View"` (Auditor); for a user with multiple new roles, the highest-privilege role wins (`mapRoleToInviteRole(highestRole(...))`) |
| `return_url` | Deep link to the committee page |

A re-invite is skipped when the user's effective access is unchanged (e.g. gaining Auditor on top of Writer).

**On success**, the invite service returns an invite UID, which the committee service logs. The committee service does **not** write invite metadata onto the settings user entry and does **not** write a secondary KV mapping — the invite service owns the invite record, and acceptance is reconciled by email (below). The whole send is best-effort: a failure is logged and does not block further processing.

### Invite Acceptance

**Triggered by:** a `lfx.invite-service.invite_accepted` event (`inviteapi.InviteServiceAcceptedSubject`) published by the **invite service** after it processes the self-serve acceptance and updates its own KV record. (Self-serve publishes the raw `lfx.invite.accepted` event to the invite service; the committee service consumes only the enriched re-publish.)

**NATS subscription:** registered in `cmd/committee-api/service/committee_handler.go` and `providers.go` under `inviteapi.InviteServiceAcceptedSubject`.

**Message payload** (`inviteapi.InviteServiceAcceptedEvent` — the full invite record): the handler requires `uid` (invite UID), `accepted_by` (new LFID username), and `recipient.email`; events missing any of these are discarded. `role` selects which settings slices to promote.

**Handler:** `(*messageHandlerOrchestrator).HandleInviteAccepted`

**Processing steps:**

1. Validate the event (`uid`, `accepted_by`, `recipient.email`).
2. List **all** committee UIDs (`ListAllUIDs`) and, for each committee, load `CommitteeSettings` with revision. (A full scan; replacing it with an email → committee index is tracked as a TODO in the handler.)
3. In each settings record, find email-only entries (`username == ""`) whose normalized email matches `recipient.email`, scoped by the invite `role`: `Manage` → `Writers`, `View` → `Auditors`, empty/unknown → both slices.
4. Set `username = accepted_by` and clear any legacy `invite` field on the matched entries.
5. Call `UpdateSettings` (fires FGA and indexer messages; publishes `committee_settings.updated`), retrying up to 3 times on revision conflicts. Because NATS handlers have no inbound JWT, a service-identity bearer is injected into the write context.

This reconciles every committee the accepted user was invited to, regardless of which resource triggered the original invite. The `committee_settings.updated` event fired by step 5 passes through `HandleCommitteeSettingsUpdated` again. Users who were present in old settings as an email-only invited entry are detected by `wasInvitedInOldSettings` and skipped, preventing a duplicate notification email.

> **Note:** earlier versions stored invite metadata on settings user entries and a `lookup/committee-settings-invite/{invite_uid}` KV mapping to route acceptance events. Neither is written anymore; the `KVLookupSettingsInvitePrefix` constant remains only for legacy reference, and any `invite` object still present on a settings entry is legacy data that is cleared on promotion.

### Member vs. Settings Invite

This LFID invite flow applies only to **settings roles** (Writers / Auditors). It is distinct from the **committee invite API** (above), which manages `CommitteeInvite` records and controls membership in `CommitteeMember`. Members added via `committee_member.created` who lack an LFID also receive an invite-service invite (`sendMemberInvite` in `internal/service/message_handler.go`, with the returned invite metadata stored on the member record), but promoting those `CommitteeMember` records on LFID invite acceptance is still tracked as future work — `HandleInviteAccepted` only promotes settings entries.
