# Invite / Application / Join-Leave Flows

Patterns in the membership-acquisition state machines (`docs/invite-application-flows.md`): terminal-step
idempotency, `join_mode` gating, invite ownership enforcement, and caller-identity resolution. These are
data-integrity / authorization patterns — cost-of-miss promotes them at a single occurrence.

**Read when:** `cmd/committee-api/service/committee_service.go` (invite/application/join/leave handlers),
`internal/domain/model/committee_invite.go`, `internal/domain/model/committee_application.go`,
`internal/service/message_handler.go` (invite-accepted handling), or `docs/invite-application-flows.md`.

---

## `invite-application-flows/member-before-terminal-status` — Critical

**Pattern:** an invite is marked `accepted` (or an application `approved`) **before** the committee member
is created. If `CreateMember` then fails, the invite/application is stranded in a terminal state and the
user can never retry, leaving the system inconsistent (accepted invite, no member). The contract requires
member creation to run first; the status is marked terminal only after member creation succeeds.

**Detect:** in `AcceptInvite` / `ApproveApplication` handlers, verify `CreateMember` is called and its error
checked BEFORE the status is set to `accepted`/`approved` and persisted.

**Empirical citation:** PR #64 `cmd/committee-api/service/committee_service.go:477` (Copilot) — "`AcceptInvite` updates the invite status to `accepted` before creating the committee member. If `CreateMember` fails ... the invite remains permanently accepted and the user can't retry, leaving the system inconsistent." Endorsed by `docs/invite-application-flows.md`: "Member creation runs first — if it fails, the invite stays unchanged so the invitee can safely retry."

**Failure message:** Invite/application marked terminal before the committee member is created — a member-create failure strands the record unrecoverably.

**Fix:** create the member first, check the error, and only mark the invite `accepted` / application `approved` after success (or add a compensation path that restores the prior status and republishes the indexer message).

---

## `invite-application-flows/join-mode-gate` — Critical

**Pattern:** a self-service action (submit application, join) is allowed when `base.JoinMode` is empty
(`""`) because the check is `JoinMode != "closed"` rather than the positive `JoinMode == "application"` /
`== "open"`. Existing committees with an unset `join_mode` then effectively accept applications/joins they
should not. Endpoints that don't match the active `join_mode` must return `403 Forbidden`.

**Detect:** in `SubmitApplication` / `JoinCommittee`, verify the guard is a positive equality
(`JoinMode == "application"` / `"open"`) and that an empty/unknown `join_mode` is treated as not-allowed
(returns `Forbidden`).

**Empirical citation:** PR #61 `cmd/committee-api/service/committee_service.go:503` (CodeRabbit) — "Empty `JoinMode` defaults to allowing applications." Same PR Copilot `committee_service.go:550` — "SubmitApplication allows applications when settings.JoinMode is empty (''), even though the API default is invite_only ... only allowing submissions when join_mode == 'application'."

**Failure message:** Self-service join/application gate allows an empty/unknown `join_mode` — should require the exact active mode and otherwise return 403.

**Fix:** gate on the positive `join_mode` value (`== "application"` / `== "open"`); treat empty/unknown as not-allowed and return `errors.NewForbidden`.

---

## `invite-application-flows/enforce-invite-ownership` — Critical

**Pattern:** accept/decline invite handlers do not verify that the caller is the invitee (matched by their
resolved email) before mutating the invite. Because these are `allow_all` self-action routes in Heimdall,
the service layer is the only place that enforces "you can only accept your own invite."

**Detect:** in `AcceptInvite` / `DeclineInvite`, verify the resolved caller email is compared against
`invite.InviteeEmail` and a mismatch returns `Forbidden` before any status change.

**Empirical citation:** PR #61 `cmd/committee-api/service/committee_service.go:461` (CodeRabbit) — "Enforce invite ownership before accepting or declining." Endorsed by `docs/invite-application-flows.md`: "Only the invitee (matched by their primary email from the auth-service) can accept their own invite."

**Failure message:** Accept/decline invite handler does not enforce invitee ownership — any authenticated user could act on another user's invite.

**Fix:** resolve the caller's email and compare to `invite.InviteeEmail`; return `errors.NewForbidden` on mismatch before mutating state.

---

## `invite-application-flows/principal-is-not-email` — Critical

**Pattern:** a self-service handler treats the `PrincipalContextID` (the Heimdall `principal`/Auth0 sub)
as the caller's email — e.g., using it directly as a member `Email` or to match an invite. The principal
is a subject identifier, not an email. Caller email must be resolved at request time via the auth-service
(`lfx.auth-service.user_emails.read`); a failed lookup returns `400`.

**Detect:** in join/leave/accept/submit handlers, flag any use of the principal/username context value as a
member `Email` or as the key to match invite/application records. Confirm `resolveCallerEmail`
(auth-service lookup) is used instead.

**Empirical citation:** PR #61 `cmd/committee-api/service/committee_service.go:689` (CodeRabbit) — "Principal used as Email is likely incorrect." Same PR Copilot `committee_service.go:722` — "JoinCommittee/LeaveCommittee treat the authenticated context value (PrincipalContextID) as a member email. However JWTAuth stores the Heimdall `principal` claim ... If principal is a user ID, JoinCommittee will create members with invalid email values and LeaveCommittee will never find the member." Resolved by PR #64/#65 introducing `resolveCallerEmail`.

**Failure message:** Self-service handler uses the principal/username as an email — resolve the caller email via the auth-service instead.

**Fix:** call `resolveCallerEmail(ctx, principal)` (auth-service `user_emails.read`) and use the resolved primary email for member creation / invite matching; return `400` if the lookup fails.

---

## `invite-application-flows/auth-service-failure-not-validation` — Important

**Pattern:** an auth-service identity lookup failure (transport error, `success:false` for non-not-found
reasons, or `success:true` with nil data) is downgraded to a `Validation` (400) error, or all `success:false`
responses are mapped to `NotFound`. This misclassifies upstream/integration failures as client errors and
hides auth-service problems.

**Detect:** in `resolveCallerEmail` and `internal/infrastructure/nats/messaging_request.go` / `models.go`
(`CheckError`), verify only genuine not-found responses map to `NotFound`; other failures map to
`Unexpected`/`ServiceUnavailable`. Verify nil `userReader` is guarded (mock mode) rather than panicking.

**Empirical citation:** PR #65 `cmd/committee-api/service/committee_service.go:877` (CodeRabbit) — "Don't downgrade auth-service failures to validation errors." Same PR Copilot `messaging_request.go:91/94` — "If auth-service returns `success: false` for reasons other than 'not found', this currently maps everything to `NotFound` ... map to `NotFound` only when the error indicates not-found, otherwise return `Unexpected`/`ServiceUnavailable`" and `committee_service.go:872` ("`resolveCallerEmail` assumes `s.userReader` is non-nil and will panic if it isn't").

**Failure message:** Auth-service lookup failure mapped to Validation/NotFound rather than Unexpected/ServiceUnavailable, or `userReader` not nil-guarded.

**Fix:** map only true not-found to `NotFound`; map other auth-service failures to `Unexpected`/`ServiceUnavailable` and preserve the underlying error; guard a nil `userReader` (mock mode) with a service-unavailable error instead of dereferencing it.
