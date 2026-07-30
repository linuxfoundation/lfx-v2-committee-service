<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Invite and application flow patterns

Local empirical patterns for the invite/application handlers and the
cross-service contracts they depend on.

**Read when** the patch touches the invite / application / join / leave
handlers, `internal/domain/model/committee_{invite,application}.go`, or
`docs/invite-application-flows.md`.

---

## `invite-application-flows/cross-service-claim-limit` — High

**Pattern:** a value is added to the `CustomClaims` map sent to the
invite-service without regard for that service's per-claim size limit. Because
invite dispatch is best-effort *after* persistence, an over-limit claim makes
the whole dispatch fail while the API still returns success. The record exists,
the invitee never receives an email, and nothing surfaces server-side.

**Detect:** a new entry in the invite-service `CustomClaims` map
(`cmd/committee-api/service/committee_service.go:868-871` is the current set)
must pass through `safeClaimValue` unless it is provably bounded — a UUID, a
fixed enum, a boolean. It must also be added to the `CustomClaims` table in
`docs/invite-application-flows.md:66`. A variable-length claim written directly
into the map is the finding.

Do not propose truncation as the fix; see below.

**Evidence:** `copilot-pull-request-reviewer`, PR #154, thread `r3597368249`,
on `cmd/committee-api/service/committee_service.go:867`: *"These values can
cause the entire invite dispatch to be rejected … invite-service v0.1.10
rejects any custom-claim value over 1024 bytes. Because dispatch is
best-effort after persistence, such a valid create request returns success
while no email is sent."*

Follow-up thread `r3597430149` rejected a truncating fix as worse — *"a valid
long URI can therefore be persisted with a different value"* — which is why the
landed fix omits rather than truncates.

Fixed and verified in `main@bd39fe9`: `safeClaimValue` at
`cmd/committee-api/service/committee_service.go:886-895` logs a warning and
returns `""` above 1024 bytes; it is applied to the four variable-length claims
at `:868-871`; `TestSafeClaimValue`
(`cmd/committee-api/service/committee_service_test.go:2876`) covers five cases
including a multibyte rune straddling byte 1024; and the six-row `CustomClaims`
table is documented at `docs/invite-application-flows.md:66`.

**Why it earns a place:** cost of miss is a silent failure with no server-side
error — a 201 and no email — and the fix landed across code, test, and contract
doc together, which is the strongest acted-on signal in the window.

**Failure message:** Variable-length value added to the invite-service
`CustomClaims` without the 1024-byte guard — an over-limit claim silently
prevents the invite email while the API still returns success.

**Fix:** wrap the value in `safeClaimValue(ctx, value, "<claim_key>", uid)`, and
add the claim to the `CustomClaims` table in `docs/invite-application-flows.md`.
Omit an over-limit value; do not truncate it.
