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

Endpoints that act on behalf of the caller (accept/decline invite, submit application, join, leave) need the caller's **email address** to match records or create members. Because the JWT issued by Heimdall contains only the user's `principal` (subject identifier), the service resolves the email at request time via a NATS request/reply call to the auth-service:

- **Subject:** `lfx.auth-service.user_emails.read`
- **Request payload:** the caller's principal (raw bytes, no JSON wrapping)
- **Response:** JSON with `{ "success": true, "data": { "primary_email": "...", "alternate_emails": [...] } }`

The service uses `primary_email` from the response. If the lookup fails (auth-service unavailable, principal unknown), the request is rejected with `400 Bad Request`.

---

## Idempotency Notes

Both the invite and application flows are designed so transient failures during the terminal step do not leave records in an unrecoverable state:

- **Member creation happens before the invite/application is marked terminal.** If member creation succeeds but the status update fails, the invite/application remains in its pre-terminal state and the caller can safely retry. Duplicate member creation is handled by the member uniqueness check.
- **Revoked invites and rejected applications are reinstateable**, so admin workflows that revoke-then-reinvite, or reviewer workflows that reject-then-reconsider, do not require manual record cleanup.
