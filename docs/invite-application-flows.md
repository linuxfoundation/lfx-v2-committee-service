# Invite & Application Flows

This document describes how committee membership is acquired, including the full lifecycle of invites and applications, allowed state transitions, and edge cases.

---

## Overview

Committees support four membership modes, configured via the `join_mode` field on the committee (stored on `CommitteeBase`):

| `join_mode` | How members join |
|-------------|-----------------|
| `closed` | Admin creates members directly via `POST /committees/{uid}/members` |
| `invite_only` | Admin creates an invite; invitee accepts it |
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
- Optional body field `organization` (`id`, `name`, `website`) stores the invitee's organization on the invite record when provided.
- If an invite for the same email already exists in this committee:
  - `status: revoked` — the existing invite is reinstated to `pending` (no new record created); role and organization are updated if provided.
  - Any other status (`pending`, `declined`, `accepted`) — returns `409 Conflict`.
- After the invite record is persisted (create or reinstate), the service dispatches a best-effort send-invite request to the invite service (`lfx.invite-service.send_invite`, `dispatchInviteEmail` in `cmd/committee-api/service/committee_service.go`) so the invitee receives an email. The request uses the invite-service permission vocabulary with `role: "Member"` (the committee role on the invite record is applied after acceptance). Dispatch is best-effort: `inviteSender.SendInvite` failures are logged (`failed to dispatch committee invite email` with `error`, `committee_uid`, `invite_uid`) and do not fail the API call. There is no automatic retry of a failed send, and committee-service exposes no dedicated "resend" endpoint. Recovery is via the invite lifecycle: revoke the invite and re-invite the same email (`POST /committees/{uid}/invites`), which reinstates the record and re-triggers `dispatchInviteEmail`.

**Accepting an invite** (`POST .../accept`):
- Only the invitee (matched by their primary email from the auth-service) can accept their own invite.
- Optional body field `organization` replaces the stored invite organization when the payload includes an `id`; otherwise the invite record organization is used as-is (no field-level merging).
- Allowed from: `pending`, `declined`.
- Blocked from: `revoked` (invite was withdrawn) — returns `409 Conflict`.
- **Idempotent for `accepted` status:** if the invite is already `accepted` (e.g. when the caller retries a prior successful HTTP acceptance), the endpoint looks up the existing committee member by email and returns it as a success (`200 OK`). If no matching member record is found despite the accepted status (data inconsistency), returns `409 Conflict`.
- On success (from `pending`/`declined`): creates a committee member and marks the invite `accepted`. Member creation runs first — if it fails, the invite stays unchanged so the invitee can safely retry.
- Returns the created or existing committee member.

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

**Email notifications (opt-in via `notify` request field, default `false`):**
- **Submitted / reinstated** — when `SubmitApplication` is called with `notify: true` and the call succeeds (both fresh-create and rejected→pending reinstatement paths), a `lfx.committee-api.committee_application.submitted` event is published. The notification handler fans out an email to all committee writers who have an LFID and a known email address. If the committee has no eligible writers, it falls back to the project-level writers (settings keyed by `committee.ProjectUID`). Fan-out uses `errgroup` with a concurrency limit of 5; individual send failures are logged but do not fail the API call. The email includes the project name alongside the committee name, and links to the committee page (`buildCommitteeURL`, `/project/groups/{uid}`) — not an applications-specific deep link.
- **Approved** — when `ApproveApplication` is called with `notify: true` and succeeds, a `lfx.committee-api.committee_application.updated` event is published. The notification handler sends a single accepted email to the applicant's email address. The generic member-added role notification is suppressed for LFID applicants when `notify: true` (the application-accepted email covers the same intent); email-only applicants still receive the invite-service invite.
- **Rejected** — when `RejectApplication` is called with `notify: true` and succeeds, the same updated event is published. The handler sends a single rejected email to the applicant, including `reviewer_notes` if set.
- Writers without an LFID (no `Username`) or without a known email address are skipped — both are required for a direct email.
- All sends are best-effort: failures are logged with `"committee_uid"` and (for submitted) `"username"` (redacted) and never propagate to callers.

---

## Open Mode

When `join_mode: open`, any authenticated user can join without an invite or approval.

**How it works:**
- User calls `POST /committees/{uid}/join`.
- A committee member is created immediately with `status: Active`.

---

## Identity Resolution

Endpoints that act on behalf of the caller (accept/decline invite, submit application, join, leave) need the caller's **email address** to match records or create members. Because the JWT issued by Heimdall contains only the user's `principal` (LFX username), the service maps that username to an Auth0 sub (`auth0|{userID}`) and resolves the email at request time via a NATS request/reply call to the auth-service:

- **Subject:** `lfx.auth-service.user_emails.read`
- **Request payload:** JSON `{"user":{"auth_token":"auth0|{userID}"}}` where `{userID}` is derived from the caller's LFX username via `pkg/auth.MapUsernameToAuthSub` (safe usernames are used directly; unsafe/legacy usernames are SHA-512 hashed and base58-encoded to ~88 characters). See `UserEmailsNATSRequest` in `internal/infrastructure/nats/models.go`.
- **Response:** JSON with `{ "success": true, "data": { "primary_email": "...", "alternate_emails": [...] } }`

The service uses `primary_email` from the response. Lookup failures map to HTTP status as follows: validation errors (missing principal, or no primary email in the response) → `400 Bad Request`; auth-service user not found (`success=false` from `user_emails.read`, or no email data returned) → `404 Not Found`; auth-service or NATS unavailable (transport failure) → `503 Service Unavailable`.

---

## Idempotency Notes

Both the invite and application flows are designed so transient failures during the terminal step do not leave records in an unrecoverable state:

- **Member creation happens before the invite/application is marked terminal.** If member creation succeeds but the status update fails, the invite/application remains in its pre-terminal state and the caller can safely retry. Duplicate member creation is handled by the member uniqueness check.
- **Revoked invites and rejected applications are reinstateable**, so admin workflows that revoke-then-reinvite, or reviewer workflows that reject-then-reconsider, do not require manual record cleanup.

---

## LFID Invite Flow (Email-only Users)

This is separate from the **committee invite API** above. It covers users who appear on committee resources **without an LFID** (`username` empty): Writers and Auditors in settings, and Members on the roster.

Pending invite state is owned by the **invite service** (and committee invite endpoints for API-managed invites), not embedded on committee resource rows.

### Overview

| Context | Trigger | No LFID (`username` empty) | Has LFID |
|---|---|---|---|
| Settings Writers / Auditors | `committee_settings.updated` diff | Send invite via invite service (`Manage` / `View` role) | Direct role-notification email |
| Committee Members | `committee_member.created` | Send invite via invite service (`Member` role) | Direct member notification email |

| User state | `username` field | Action |
|---|---|---|
| Has LFID | non-empty | Send a direct role-notification email via the email service |
| No LFID | empty | Send an invite request to the invite service |

The invite service delivers the invite email. When the user completes LFID signup and accepts, the invite service publishes an enriched acceptance event that committee-service consumes. The committee service does not persist any invite metadata on committee resource rows for settings invites — it only logs the invite UID returned by the invite service.

### Sending an Invite

**Settings (Writers / Auditors):** `HandleCommitteeSettingsUpdated` on `lfx.committee-api.committee_settings.updated` when the diff adds or changes email-only users.

**Members:** `HandleCommitteeMemberCreated` on `lfx.committee-api.committee_member.created` when the new member has no `username`.

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

**NATS subject:** `lfx.invite-service.send_invite` (`inviteapi.SendInviteSubject`, request/reply).

**On success**, the invite service returns an invite UID, which the committee service logs. The committee service does **not** write invite metadata onto committee resource rows and does **not** write a secondary KV mapping — the invite service owns the invite record, and acceptance is reconciled by email (below). The whole send is best-effort: a failure is logged inside `sendMemberInvite` / the settings invite path and does not block further processing.

### Invite Acceptance

**Triggered by:** a `lfx.invite-service.invite_accepted` event (`inviteapi.InviteServiceAcceptedSubject`) published by the **invite service** after it processes the self-serve acceptance and updates its own KV record. (Self-serve publishes the raw `lfx.invite.accepted` event to the invite service; the committee service consumes only the enriched re-publish.)

**NATS subscription:** registered in `cmd/committee-api/service/committee_handler.go` and `providers.go` under `inviteapi.InviteServiceAcceptedSubject`.

**Message payload** (`inviteapi.InviteServiceAcceptedEvent` — embeds the full `Invite` record):

```json
{
  "uid": "<invite UID>",
  "accepted_by": "<new LFID username>",
  "role": "Member",
  "recipient": {
    "email": "user@example.com"
  }
}
```

The handler requires `uid` (invite UID), `accepted_by` (new LFID username), and `recipient.email`; events missing any of these are discarded. `role` is informational; enrichment does not branch on it.

**Handler:** `(*messageHandlerOrchestrator).HandleInviteAccepted`

**Processing steps:**

1. Validate the event (`uid`, `accepted_by`, `recipient.email`); discard if any are missing.
2. Normalize `recipient.email` (lowercase, trimmed).
3. **(Upfront FGA phase)** Fetch all `CommitteeInvite` records for the normalized email (`fetchInvitesByEmail` via `ListAllInvites`). For each committee that has a matching invite, publish an FGA `invitee` tuple for every invite record (`publishInviteeFGAForCommittee`). This runs before the enrichment scan so the invite is immediately visible in the self-serve UI and the user can call `AcceptInvite` without waiting for the full scan to complete. See [LFXV2-2238](https://linuxfoundation.atlassian.net/browse/LFXV2-2238) for the planned email-index that will replace the `ListAllInvites` full-bucket scan.
4. List **all** committee UIDs (`ListAllUIDs`; a full scan — see [LFXV2-2238](https://linuxfoundation.atlassian.net/browse/LFXV2-2238)).
5. Enrich up to **10 committees concurrently** (bounded errgroup). For each committee, `enrichInvitedUserInCommittee` reconciles every email-only record matching that email. The invite `role` is **ignored** — acceptance always reconciles all resource data for the recipient email:
   - **Settings:** all email-only Writers and Auditors (`username == ""`) — set `username` from `accepted_by` via `UpdateSettings`, retrying up to 3 times on revision conflicts. `UpdateSettings` fires FGA and indexer messages and publishes `committee_settings.updated`.
   - **Members:** email-only roster rows — set `username` from `accepted_by` via `UpdateMember` (revision-conflict retries), same as Writers/Auditors. No auth email lookup on this path.

Because NATS event handlers have no inbound JWT, a service-identity bearer (`Bearer lfx-v2-committee-service`) is injected into the write context so the downstream FGA/indexer calls carry a recognized token.

Invite metadata is **not** read or written on committee resource rows during enrichment (the `CommitteeUser` struct no longer carries an invite field, and the legacy `KVLookupSettingsInvitePrefix` constant is unused — kept only for backward reference).

A subsequent `committee_settings.updated` event (re-fired by `UpdateSettings`) skips duplicate notification emails for users promoted from email-only entries, detected by `wasInvitedInOldSettings`.

### Member vs. Settings Invite

This LFID invite flow reconciles both **settings roles** (Writers / Auditors) and **roster Members** for the accepted email. It is distinct from the **committee invite API** (above), which manages `CommitteeInvite` records and creates a `CommitteeMember` immediately on accept. Members added via `committee_member.created` who lack an LFID also receive an invite-service invite (`sendMemberInvite` in `internal/service/message_handler.go`); `HandleInviteAccepted` promotes those email-only `CommitteeMember` rows in the same scan as settings entries.

### Local testing with NATS CLI

Use this to exercise `HandleInviteAccepted` without the UI or invite-service acceptance path.

**Prerequisites**

- `committee-api` running locally (see [cmd/committee-api/README.md](../cmd/committee-api/README.md)).
- `NATS_URL` points at the same NATS instance as the running service.
- Test data exists for an email you control: at least one **email-only** Writer, Auditor, or Member (`username` empty) on some committee. Use that same email in the event payload.

**1. Publish invite accepted**

Member enrichment uses `accepted_by` from the event directly (no auth email lookup), same as Writers and Auditors.

```bash
export NATS_URL="${NATS_URL:-nats://localhost:4222}"
export TEST_EMAIL="you+invite-test@example.com"
export TEST_USERNAME="your-lfid-username"

nats pub --server "$NATS_URL" lfx.invite-service.invite_accepted \
  "{\"uid\":\"local-test-invite-001\",\"accepted_by\":\"$TEST_USERNAME\",\"role\":\"Member\",\"recipient\":{\"email\":\"$TEST_EMAIL\"}}"
```

**2. Verify**

- **Settings (Writers / Auditors):** `GET /committees/{uid}/settings?v=1` — matching entries should have `username` set.
- **Members:** `GET /committees/{uid}/members/{member_uid}?v=1` or NATS KV:

```bash
nats kv get --server "$NATS_URL" committee-members <member_uid>
```

- **Logs:** look for `invite accepted — enriched email-only` on the `lfx.invite-service.invite_accepted` subscription.

**Unit tests:** `go test ./internal/service/ -run TestHandleInviteAccepted` covers the handler without NATS.

### Committee Invite API vs LFID Invite Flow

| Mechanism | Records | Acceptance path |
|---|---|---|
| Committee invite API (`POST /committees/{uid}/invites`) | `CommitteeInvite` + member on accept | HTTP accept endpoint creates member immediately |
| LFID invite flow (this section) | Email-only Writers, Auditors, Members | `lfx.invite-service.invite_accepted` enriches matching rows with `username` |

Both can apply to the same person over time; enrichment matches by **email**, not invite UID on resource rows.

## Notification Suppression (skip_notification)

Both the member-create and member-delete endpoints accept an `X-Skip-Notification` header (`skip_notification` in the Goa payload) that suppresses outbound emails for that specific request. This is the mechanism used by V1/PCC-origin callers (e.g. `lfx-v1-sync-helper`) to prevent notification emails from firing on sync-driven writes.

### Member created (`POST /committees/{uid}/members`)

When `X-Skip-Notification: true` is set:
- `CommitteeMemberCreatedEventData.SkipNotification` is set to `true` in the `committee_member.created` NATS event payload.
- `HandleCommitteeMemberCreated` short-circuits before sending either the direct notification email or the invite-service invite.

### Member deleted (`DELETE /committees/{uid}/members/{member_uid}`)

When `X-Skip-Notification: true` is set:
- `CommitteeMemberDeletedEventData.SkipNotification` is set to `true` in the `committee_member.deleted` NATS event payload.
- `HandleCommitteeMemberDeleted` short-circuits before sending the removal notification email.

### NATS event payload shapes

`committee_member.created` data is a `CommitteeMemberCreatedEventData` (JSON-flattened `CommitteeMember` plus `"skip_notification": true|false`).

`committee_member.deleted` data is a `CommitteeMemberDeletedEventData` (same shape: JSON-flattened `CommitteeMember` plus `"skip_notification": true|false`). Consumers that previously decoded a bare `CommitteeMember` from deleted events continue to work because the struct embeds `*CommitteeMember` and the extra field is `omitempty`.

### Scope

`skip_notification` only gates the **notification email** for the affected member. It does not suppress:
- Indexer messages (`lfx.index.*`) — those are always published.
- FGA access-control messages — those are always published.
- Settings-change emails (`HandleCommitteeSettingsUpdated`) — those are not gated by this flag.
