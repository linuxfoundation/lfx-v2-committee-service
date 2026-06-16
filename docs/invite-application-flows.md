# Invite & Application Flows

This document describes how committee membership is acquired, including the full lifecycle of invites and applications, allowed state transitions, and edge cases.

---

## Overview

Committees support four membership modes, configured via the `join_mode` setting in committee settings:

| `join_mode` | How members join |
|-------------|-----------------|
| `closed` | Admin creates members directly via `POST /committees/{uid}/members` |
| `invite` | Admin creates an invite; invitee accepts it |
| `application` | User submits an application; reviewer approves it |
| `open` | User self-joins via `POST /committees/{uid}/members/join` |

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
| `POST` | `/committees/{uid}/invites/{invite_uid}/revoke` | Admin | Revoke a pending or declined invite |

### Rules

**Creating an invite** (`POST /committees/{uid}/invites`):
- Creates a new invite with `status: pending`.
- If an invite for the same email already exists in this committee:
  - `status: revoked` — the existing invite is reinstated to `pending` (no new record created); role is updated if provided.
  - Any other status (`pending`, `declined`, `accepted`) — returns `409 Conflict`.

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

**Revoking an invite** (`POST .../revoke`):
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
- User calls `POST /committees/{uid}/members/join`.
- A committee member is created immediately with `status: Active`.

---

## Identity Resolution

Endpoints that act on behalf of the caller (accept/decline invite, submit application, join, leave) need the caller's **email address** to match records or create members. Because the JWT issued by Heimdall contains only the user's `principal` (LFX username), the service maps that username to an Auth0 sub (`auth0|{userID}`) and resolves the email at request time via a NATS request/reply call to the auth-service:

- **Subject:** `lfx.auth-service.user_emails.read`
- **Request payload:** JSON `{"user":{"auth_token":"auth0|{userID}"}}` where `{userID}` is derived from the caller's LFX username (safe usernames are used directly; unsafe usernames are SHA-512 hashed and base58-encoded)
- **Response:** JSON with `{ "success": true, "data": { "primary_email": "...", "alternate_emails": [...] } }`

The service uses `primary_email` from the response. Lookup failures map to HTTP status as follows: validation errors (missing principal) → `400 Bad Request`; auth-service user not found (`success=false` from `user_emails.read`) → `404 Not Found`; auth-service or NATS unavailable → `503 Service Unavailable`.

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

The invite service delivers the invite email. When the user completes LFID signup and accepts, the invite service publishes an enriched acceptance event that committee-service consumes.

### Sending an Invite

**Settings (Writers / Auditors):** `HandleCommitteeSettingsUpdated` on `lfx.committee-api.committee_settings.updated` when the diff adds or changes email-only users.

**Members:** `HandleCommitteeMemberCreated` on `lfx.committee-api.committee_member.created` when the new member has no `username`.

**NATS subject:** `lfx.invite-service.send_invite` (request/reply)

Invite requests use `inviteapi.SendInviteRequest` with resource type `group`, committee UID/name, recipient email, and role `Manage`, `View`, or `Member`.

Failures are logged inside `sendMemberInvite` / the settings invite path; callers do not duplicate those warnings.

### Invite Acceptance

**Published by:** invite service after processing acceptance.

**NATS subject:** `lfx.invite-service.invite_accepted` (`inviteapi.InviteServiceAcceptedSubject`)

**Subscription:** `cmd/committee-api/service/committee_handler.go` and `providers.go`

**Message payload** (`inviteapi.InviteServiceAcceptedEvent` — embeds full `Invite`):

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

Required fields for committee-service: `uid`, `accepted_by`, and `recipient.email`. `role` is informational; enrichment does not branch on it.

**Handler:** `(*messageHandlerOrchestrator).HandleInviteAccepted`

**Processing steps:**

1. Normalize `recipient.email` (lowercase, trimmed).
2. List all committee UIDs (full scan today; see [LFXV2-2238](https://linuxfoundation.atlassian.net/browse/LFXV2-2238) for planned email-index lookup).
3. For each committee, enrich email-only records matching that email:
   - **Settings:** Writers and Auditors — set `username` from `accepted_by` (revision-conflict retries on `UpdateSettings`).
   - **Members:** email-only roster rows — set `username` from `accepted_by` via `UpdateMember` (revision-conflict retries), same as Writers/Auditors. No auth email lookup on this path.

Invite metadata is **not** read or written on committee resource rows during enrichment.

A subsequent `committee_settings.updated` event skips duplicate notification emails for users promoted from email-only entries (`wasInvitedInOldSettings`).

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
