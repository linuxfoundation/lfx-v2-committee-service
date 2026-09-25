# Invite / Application / Join-Leave Flows

Patterns in the membership-acquisition state machines (`docs/invite-application-flows.md`): terminal-step
idempotency, `join_mode` gating, invite ownership enforcement, and caller-identity resolution. These are
data-integrity / authorization patterns — cost-of-miss promotes them at a single occurrence.

**Read when:** `cmd/committee-api/service/committee_service.go` (invite/application/join/leave handlers),
`internal/domain/model/committee_invite.go`, `internal/domain/model/committee_application.go`,
`internal/service/message_handler.go` (invite-accepted handling),
`internal/infrastructure/nats/messaging_request.go` / `models.go` (auth-service reply mapping —
`auth-service-failure-not-validation` inspects these directly), or `docs/invite-application-flows.md`.

---

## `invite-application-flows/member-before-terminal-status` — Critical

**Pattern:** an invite is marked `accepted` (or an application `approved`) **before** the committee member
is created. If `CreateMember` then fails, the invite/application is stranded in a terminal state and the
user can never retry, leaving the system inconsistent (accepted invite, no member). The contract requires
member creation to run first; the status is marked terminal only after member creation succeeds.

**Detect:** in `AcceptInvite` / `ApproveApplication` handlers, verify `CreateMember` is called and its error
checked BEFORE the status is set to `accepted`/`approved` and persisted.

**The rule governs the transition path only.** An idempotent already-terminal branch that returns the
existing member (or a 409) *before* reaching `CreateMember` is correct and is **not** a finding — see
`committee_service.go:962-976`, added by PR #150. A literal reading of the ordering rule flags that branch;
don't.

**Empirical citation:** PR #64 `cmd/committee-api/service/committee_service.go:477` (Copilot) — "`AcceptInvite` updates the invite status to `accepted` before creating the committee member. If `CreateMember` fails ... the invite remains permanently accepted and the user can't retry, leaving the system inconsistent." Endorsed by `docs/invite-application-flows.md`: "Member creation runs first — if it fails, the invite stays unchanged so the invitee can safely retry."

**Revised 2026-07-30 — exemption added.** The ordering is upheld in both handlers at `main@bd39fe9`
(`AcceptInvite`: `CreateMember:1001` → error check → `Status = "accepted":1007`; `ApproveApplication`:
`:1221` → `:1227`, all in `cmd/committee-api/service/committee_service.go`). The exemption above was added
because PR #150 introduced an idempotent already-accepted branch that the original Detect wording flagged.
The PR #64 thread is retained as provenance.

**Corrected 2026-07-31.** Previously cited as `:1003`/`:1009` and `:1223`/`:1229` — `ec86a8f` numbers, while
the sentence claimed `bd39fe9`. At `bd39fe9` those lines are `return nil, wrapError(...)` and
`if err := s.storage.UpdateInvite(...)`, so a reviewer checking the claim would have found the wrong
statements and reasonably concluded the entry was wrong about the ordering.

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
is a subject identifier, not an email. Caller email must be resolved at request time through
`resolveCallerEmail(ctx)`.

**Identity resolution is two-phase** since PR #156, and both phases matter
(`committee_service.go:1409-1423`):

1. **Primary** — the auth-service `EmailsByAuthToken` lookup
   (`lfx.auth-service.user_emails.read`).
2. **Fallback** — the Heimdall JWT `email` claim (`constants.EmailContextID`), taken **only** when the
   primary fails with `errors.NotFound`. The gate is strict on purpose: any other error class must not open
   the fallback.

Two related facts a reviewer needs. The helper's signature is `resolveCallerEmail(ctx)` — it does not take
the principal as an argument. And PR #157 removed the `email_verified` gate from this service; that
guarantee now lives in the Heimdall pipeline, so its absence here is deliberate.

**Detect:** in join/leave/accept/submit handlers, flag any use of the principal/username context value as a
member `Email` or as the key to match invite/application records. Confirm `resolveCallerEmail(ctx)` is used
instead. On any change to the resolution path, confirm the JWT fallback is still reachable only from
`errors.NotFound`.

**Empirical citation:** PR #61 `cmd/committee-api/service/committee_service.go:689` (CodeRabbit) — "Principal used as Email is likely incorrect." Same PR Copilot `committee_service.go:722` — "JoinCommittee/LeaveCommittee treat the authenticated context value (PrincipalContextID) as a member email. However JWTAuth stores the Heimdall `principal` claim ... If principal is a user ID, JoinCommittee will create members with invalid email values and LeaveCommittee will never find the member." Resolved by PR #64/#65 introducing `resolveCallerEmail`.

**Revised 2026-07-30 — three details corrected.** The core invariant is upheld (no handler uses the principal
as an email). Corrected against `main@bd39fe9`: the signature is `resolveCallerEmail(ctx)`, not
`(ctx, principal)`; "return 400 if the lookup fails" is no longer true; and the entry did not describe the
two-phase resolution added by PR #156 or the `email_verified` removal in PR #157. The PR #61 threads are
retained as provenance.

**Security coupling — read with `auth-service-failure-not-validation`.** Because a `NotFound` is precisely
what opens the JWT-email fallback, anything that widens what counts as not-found widens who can be resolved
as another user. That is why the sibling entry's residual `success:true` + nil-`Data` → `NotFound` mapping is
security-relevant rather than cosmetic.

**Failure message:** Self-service handler uses the principal/username as an email, or changes the caller-resolution path so the JWT fallback is reachable from an error class other than `NotFound`.

**Fix:** call `resolveCallerEmail(ctx)` and use the resolved primary email for member creation / invite matching; keep the JWT-claim fallback gated strictly on `errors.NotFound` and let other failure classes surface as themselves.

---

## `invite-application-flows/auth-service-failure-not-validation` — Critical

**Pattern:** an auth-service identity lookup failure (transport error, `success:false` for non-not-found
reasons, or `success:true` with nil data) is downgraded to a `Validation` (400) error, or all `success:false`
responses are mapped to `NotFound`. This misclassifies upstream/integration failures as client errors and
hides auth-service problems.

**Detect:** in `resolveCallerEmail` and `internal/infrastructure/nats/messaging_request.go` / `models.go`
(`CheckError`), verify only genuine not-found responses map to `NotFound`; other failures map to
`Unexpected`/`ServiceUnavailable`. Verify nil `userReader` is guarded (mock mode) rather than panicking.

**Empirical citation:** PR #65 `cmd/committee-api/service/committee_service.go:877` (CodeRabbit) — "Don't downgrade auth-service failures to validation errors." Same PR Copilot `messaging_request.go:91/94` — "If auth-service returns `success: false` for reasons other than 'not found', this currently maps everything to `NotFound` ... map to `NotFound` only when the error indicates not-found, otherwise return `Unexpected`/`ServiceUnavailable`" and `committee_service.go:872` ("`resolveCallerEmail` assumes `s.userReader` is non-nil and will panic if it isn't").

**Revised 2026-07-30 — severity raised to Critical, and one residual is still live.** The classifier was
tightened exactly as this entry asked: `internal/infrastructure/nats/messaging_request.go:119-131` now maps
only "not found"/"does not exist" to `NotFound`, and an empty error to `Unexpected`, with the rationale
written into the code.

**Still live at `main@bd39fe9`:** `messaging_request.go:134-135` maps `success:true` with `Data == nil` to
`NotFound` — the exact case this entry names as one that must not be downgraded. The severity is now
Critical rather than Important because a `NotFound` is what opens the Heimdall JWT-email fallback in
`resolveCallerEmail` (see `principal-is-not-email`), so a malformed success envelope silently takes the
fallback identity path instead of failing. This is an authorization concern, not hygiene.

**Failure message:** Auth-service lookup failure mapped to Validation/NotFound rather than Unexpected/ServiceUnavailable, or `userReader` not nil-guarded — and a wrong `NotFound` here silently opens the JWT-email fallback.

**Fix:** map only true not-found to `NotFound`; map other auth-service failures to `Unexpected`/`ServiceUnavailable` and preserve the underlying error; guard a nil `userReader` (mock mode) with a service-unavailable error instead of dereferencing it.

---

## `invite-application-flows/cross-service-claim-limit` — Important

**Pattern:** a value is added to the `CustomClaims` map sent to the invite-service without regard for that
service's per-claim size limit. Because invite dispatch is best-effort *after* persistence, an over-limit
claim makes the whole dispatch fail while the API still returns success. The record exists, the invitee never
receives an email, and nothing surfaces server-side.

**Detect:** a new entry in the invite-service `CustomClaims` map
(`cmd/committee-api/service/committee_service.go:868-871` is the current set) must pass through
`safeClaimValue` unless it is provably bounded — a UUID, a fixed enum, a boolean. It must also be added to
the `CustomClaims` table in `docs/invite-application-flows.md:66`. A variable-length claim written directly
into the map is the finding.

**Do not propose truncation as the fix.** Follow-up thread `r3597430149` rejected a truncating fix as worse —
"a valid long URI can therefore be persisted with a different value" — which is why the landed fix omits
rather than truncates.

**Empirical citation:** PR #154 `cmd/committee-api/service/committee_service.go:867`
(`copilot-pull-request-reviewer`, thread `r3597368249`) — "These values can cause the entire invite dispatch
to be rejected … invite-service v0.1.10 rejects any custom-claim value over 1024 bytes. Because dispatch is
best-effort after persistence, such a valid create request returns success while no email is sent."

Fixed and verified at `main@bd39fe9`: `safeClaimValue`
(`cmd/committee-api/service/committee_service.go:886-895`) logs a warning and returns `""` above 1024 bytes;
it is applied to the four variable-length claims at `:868-871`; `TestSafeClaimValue`
(`committee_service_test.go:2876`) covers five cases including a multibyte rune straddling byte 1024; and the
six-row `CustomClaims` table is documented at `docs/invite-application-flows.md:66`.

**Why it earns a place:** cost of miss is a silent failure with no server-side error — a 201 and no email —
and the fix landed across code, test, and contract doc together, which is the strongest acted-on signal in
the window.

**Failure message:** Variable-length value added to the invite-service `CustomClaims` without the 1024-byte guard — an over-limit claim silently prevents the invite email while the API still returns success.

**Fix:** wrap the value in `safeClaimValue(ctx, value, "<claim_key>", uid)`, and add the claim to the `CustomClaims` table in `docs/invite-application-flows.md`. Omit an over-limit value; do not truncate it.
