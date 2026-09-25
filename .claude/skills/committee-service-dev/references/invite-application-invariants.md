<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Invite & Application Invariants (committee-service)

Agent-actionable reference for the invite and application state machines. Use
this when implementing a new join mode, modifying a state transition, debugging
a stuck invite or application, or reviewing changes to `committee_service.go`.

The full flow narrative lives in `docs/invite-application-flows.md`. This file
encodes the invariants — what must be true after each transition.

---

## Join mode gate

Only the **entry-point** endpoints that create new committee membership (or
self-serve joins) check `join_mode` before touching storage. State-transition
endpoints for existing invites and applications (`accept`, `decline`, `revoke`,
`approve`, `reject`) do **not** gate on `join_mode` — they operate on an
already-created record regardless of the current mode.

| Endpoint | Mode check | Behaviour when wrong mode |
|----------|-----------|--------------------------|
| `POST /applications` | `JoinMode != "application"` → `403` | No state created |
| `POST /join` | `JoinMode != "open"` → `403` | No state created |
| `POST /invites` | None | Admins can always create invites |
| `POST /invites/{uid}/accept` | None | Operates on existing record |
| `POST /invites/{uid}/decline` | None | Operates on existing record |
| `DELETE /invites/{uid}` | None | Operates on existing record |
| `POST /applications/{uid}/approve` | None | Operates on existing record |
| `POST /applications/{uid}/reject` | None | Operates on existing record |
| `POST /members` | None — FGA enforces admin-only | |

When adding a new **entry-point** endpoint (one that creates a record from
scratch), add a join_mode guard before the first storage read or write.

---

## Invite state machine

```text
pending  ──accept──▶  accepted
pending  ──decline──▶ declined
pending  ──revoke──▶  revoked
declined ──accept──▶  accepted
declined ──revoke──▶  revoked
revoked  ──POST /invites (same email)──▶ pending   (reinstate)
```

### Invariants per transition

#### Create invite → `pending`

| What must happen | Notes |
|-----------------|-------|
| `CommitteeInvite` written to KV with `status: pending` | |
| `lfx.index.committee_invite` published (indexer) | |
| `lfx.fga-sync.update_access` published (`committee_invite` object, `invitee` relation if LFID exists) | Omit `invitee` when email has no LFID; set `exclude_relations: ["invitee"]` |
| `lfx.committee-api.committee_invite` published (domain event) | |
| `lfx.invite-service.send_invite` dispatched (best-effort) | Failure is logged, does not fail the API call |
| `inviter` and `expires_at` set on the record | 30-day expiry from creation |

**Conflict rule:** existing `pending`, `declined`, or `accepted` invite for the same email → `409`. Existing `revoked` invite → reinstate instead of creating a new record; refresh `inviter` and `expires_at`.

#### Accept invite (`pending` or `declined`) → `accepted`

| What must happen | Notes |
|-----------------|-------|
| Member created first; if member creation fails, invite status stays unchanged | Allows safe retry |
| `CommitteeInvite` status updated to `accepted` | |
| `lfx.index.committee_invite` published | |
| `lfx.fga-sync.update_access` published (`committee_invite`, ensuring `invitee` tuple present) | |
| `lfx.fga-sync.member_put` published (`committee`, `member` relation) | Only if `Username` non-empty |
| `lfx.index.committee_member` published | |

**Blocked when:** `status: revoked` → `409`. `status: accepted` (retry) → look up existing member by email and return it (`200`). Expired `expires_at` → `409`. Caller must be the invitee (email resolved from auth-service primary, fallback JWT claim).

#### Decline invite (`pending`) → `declined`

| What must happen | Notes |
|-----------------|-------|
| `CommitteeInvite` status updated to `declined` | |
| `lfx.index.committee_invite` published | |
| `lfx.fga-sync.update_access` published (`committee_invite`, retaining `invitee` relation) | Keeps the tuple live so the invitee can still accept later; does not revoke access |

**Caller:** invitee only. The `invitee` FGA relation is not removed — the `update_access` publish re-asserts it so the invitee can call `AcceptInvite` after declining.

#### Revoke invite (`pending` or `declined`) → `revoked`

| What must happen | Notes |
|-----------------|-------|
| `CommitteeInvite` status updated to `revoked` | |
| `lfx.index.committee_invite` published | |

**Blocked when:** `status: accepted` or `status: revoked`. **No FGA `delete_access`** — the `committee_invite` object stays in FGA so history queries work; the defensive delete branch is not used in production.

---

## Application state machine

```text
pending  ──approve──▶ approved
pending  ──reject──▶  rejected
rejected ──POST /applications (same email)──▶ pending  (reinstate)
```

### Invariants per transition

#### Submit application → `pending`

| What must happen | Notes |
|-----------------|-------|
| `CommitteeApplication` written to KV with `status: pending` | |
| `lfx.index.committee_application` published | |
| `lfx.committee-api.committee_application.submitted` published (domain event) | Consumed to notify LFID writers |

**Conflict rule:** existing `pending` or `approved` application for same email → `409`. Existing `rejected` application → reinstate to `pending`; clear `reviewer_notes`, update `message` and `organization` if provided.

#### Approve application (`pending`) → `approved`

| What must happen | Notes |
|-----------------|-------|
| Member created | |
| `CommitteeApplication` status updated to `approved` | |
| `lfx.index.committee_application` published | |
| `lfx.fga-sync.member_put` published (`committee`, `member` relation) | Only if `Username` non-empty |
| `lfx.index.committee_member` published | |
| `lfx.committee-api.committee_application.updated` published (domain event) | Consumed to send decision email to applicant |

#### Reject application (`pending`) → `rejected`

| What must happen | Notes |
|-----------------|-------|
| `CommitteeApplication` status updated to `rejected` | |
| `lfx.index.committee_application` published | |
| `lfx.committee-api.committee_application.updated` published | Consumed to send decision email to applicant |

No FGA change on rejection — no member exists.

---

## LFID registration path (`lfx.invite-service.invite_accepted`)

Triggered by the invite-service after a new LFID is created via self-serve.
This is **not** the same as the HTTP accept endpoint.

| What must happen | Notes |
|-----------------|-------|
| For every `CommitteeInvite` (any status) matching the accepted email: publish `lfx.fga-sync.update_access` (`committee_invite`, `invitee` relation) | Upfront FGA phase — runs before the enrichment scan so the user can call AcceptInvite immediately |
| For every email-only Writer/Auditor in settings matching the email: promote `username` via `UpdateSettings` | `UpdateSettings` fires its own FGA + indexer + `committee_settings.updated` event |
| For every email-only Member matching the email: promote `username` via `UpdateMember` | `UpdateMember` fires its own FGA + indexer messages |
| Service-identity bearer injected into write context | No inbound JWT on NATS handlers |

**Key invariant:** member creation is never triggered here — only `username` enrichment on already-existing email-only rows. If the invite was created via the HTTP API it was already accepted (member exists); if via `sendMemberInvite`, the member exists from the original create.

---

## Email notifications quick reference

| Trigger | Notification | Suppressible |
|---------|-------------|--------------|
| `committee_member.created` event | Invite email to new member (via invite-service) | Yes — `X-Skip-Notification: true` |
| `committee_member.deleted` event | Removal email to removed member | Yes — `X-Skip-Notification: true` |
| `committee_application.submitted` event | Email to all LFID writers (new application pending review) | No |
| `committee_application.updated` event | Decision email to applicant (approved or rejected) | No |
| `committee_settings.updated` event | Invite email to newly added Writers/Auditors | Suppressed when `wasInvitedInOldSettings` (already notified) |

`X-Skip-Notification` gates the member notification email only — it does not
suppress indexer or FGA messages, which are always published.

---

## Key invariants when modifying flows

1. **Member creation before invite/application status update.** If member
   creation fails, the invite or application stays unchanged so the caller can
   retry safely.
2. **FGA `member_put` only when `Username` is non-empty.** Email-only members
   get no FGA tuple until their LFID is resolved via `HandleInviteAccepted`.
3. **`exclude_relations: ["member"]` on committee `update_access`.** Individual
   members are managed via `member_put`/`member_remove`; the committee-level
   `update_access` must never overwrite them.
4. **`exclude_relations: ["invitee"]` on `committee_invite update_access` when
   invitee has no LFID.** Prevents fga-sync from deleting a tuple written by a
   prior successful resolution on a transient auth-service failure.
5. **Domain event before/after any state transition must match what the
   handler expects.** Both `submitted` and `updated` application events are
   consumed to fire emails — verify the handler in `message_handler.go` when
   adding new statuses.
