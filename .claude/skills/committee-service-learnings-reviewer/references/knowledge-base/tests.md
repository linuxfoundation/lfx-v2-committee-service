<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Test-shape patterns

Local empirical patterns about tests that *exist* but cannot fail. This is not
"add a test" advice — a bare request for coverage is a known false positive
here. These are shapes where the suite stays green while the behaviour under
test is broken.

**Read always** — including for patches that touch no test file at all.

That is deliberate. Shape 3 below triggers on a **production** change: a new
guard or early return whose test drives it through a fake that cannot produce
the input the guard rejects. There, the defect is the *absence* of a capable
test, and routing this file on "the patch touches a `*_test.go`" would scope it
out of exactly the diffs it is written for. Shapes 1, 2 and 4 do need a test or
fake in the patch; read them past when there is none.

---

## `tests/assertion-cannot-fail` — High

**Pattern:** the test asserts something that is true by construction, so the
suite stays green when the behaviour regresses.

**Detect:** each shape has its own trigger. Evaluate all four; they do not
share a precondition.

1. **Read-back assertion** — trigger: any new or changed assertion. Flag one
   that reads a value **back off the same object the test set it on**, with no
   code under test standing between the write and the read —
   `c := Claims{Email: "a@b.c"}; assert.Equal(t, "a@b.c", c.Email)`. A normal
   table-driven expectation (`tt.want`) is **not** this shape: there the value
   travels through the code under test, which is free to get it wrong. The
   question is whether the production code could make the assertion fail, not
   whether the test authored the expected value.
2. **Dropped argument** — trigger: the diff adds a parameter, claim, or field to
   a call that a mock or spy records. Require the fake to **capture** the new
   value and a test to **assert** it; a fake that receives it and drops it means
   nothing can assert it.
3. **Fake blocks the branch** — trigger: the diff adds or changes a guard,
   branch, or early return. Read the fake the test drives it with and ask
   whether that fake can even produce the input the guard exists to reject. This
   is the PR #161 shape below: a fake repository that scans current records
   could never return the stale record, so the new guard was never executed.
4. **Call-count guard** — trigger: any assertion wrapped in
   `if len(spy.calls) > 0 { ... }`. Vacuous when the call never happens, which
   is exactly the regression it should catch. **Not a finding** when something
   earlier in the same test already fails if the call is missing — a
   `require.Len(t, spy.calls, 1)`, a `require.NoError` on a path that cannot
   succeed without the call, or an unconditional assertion on the same spy. The
   defect is an assertion that *silently* skips, not the `if` itself.

Shapes 1 and 4 are read off the test alone; shapes 2 and 3 need the fake read
alongside it.

**Evidence:** `copilot-pull-request-reviewer`, four PRs, acted on every time.

- PR #156, thread `r3632379453`, `internal/infrastructure/auth/jwt_test.go:305`:
  *"This assertion only reads back a field that the test assigned directly, so
  it does not test the behavior introduced here … an incorrect JSON tag … would
  leave the entire suite green while production always gets an empty email."*
  Fixed by `TestHeimdallClaimsEmailJSONDecoding`, which round-trips
  representative claims through `json.Unmarshal`; verified in `main@bd39fe9` at
  `internal/infrastructure/auth/jwt_test.go:318`.
- PR #163, thread `r3669465058`: *"`mockCommitteeWriterOrchestrator.CreateMember`
  records only the member and discards the `sync` argument."* Fixed in
  `bfbc5a1`; verified at
  `cmd/committee-api/service/committee_service_test.go:121` (the
  `createMemberSyncArgs []bool` field), `:152` (capture), `:1434` (assertion).
- PR #154, thread `r3597368284`: *"indexing the map directly makes the three
  empty-value assertions pass when a key is missing"* — fixed by adding
  `assert.Contains` key-presence checks before the value assertions.
- PR #161, thread `r3659931848`,
  `internal/service/message_handler_test.go:2729`:
  *"`MockRepository.ListMembersByUsername` scans the current records, so a
  member already set to `otheruser` is never returned and … the guard at line
  618 is not reached … the PR's central reassignment protection remains
  untested."* Fixed in `71916b1` with a `staleIndexCommitteeReader` fake and
  `TestHandleUserDeleted_StaleIndexReuse`; verified at
  `internal/service/message_handler_test.go:3037`.

**Live illustration in current code:** the PR #163 fix reintroduced the shape
it was fixing. `cmd/committee-api/service/committee_service_test.go:1433-1434`
wraps the new assertion in `if len(mockOrch.createMemberSyncArgs) > 0 { ... }`,
so a regression that stops calling `CreateMember` on that path passes silently.
Quote this when the guard shape appears in a patch.

**Why it earns a place:** recurrence across four PRs in one window, acted on
each time, and repo-specific — it keys on this repo's own fake layer
(`internal/infrastructure/mock/**` and the `mock*Orchestrator` spies in
`cmd/committee-api/service/committee_service_test.go`), not on generic testing
advice.

**Failure message:** This assertion cannot fail — it reads back a value the test
set, checks an argument the mock discards, sits behind a branch the fake
prevents reaching, or hides inside a call-count guard.

**Fix:** capture the new argument in the fake, assert it unconditionally, and
assert the call count separately so a missing call fails rather than skips.

---

## `tests/no-external-service-dependency` — High

**Pattern:** a test dials a real service and skips itself when the service is
absent. Locally it passes because a broker happens to be running; in CI it
skips, reports green, and provides no coverage at all — including for the error
paths the change was written to add.

**Detect:** a `*_test.go` that dials a real endpoint — `nats.DefaultURL`, a
hardcoded `localhost:<port>`, a live URL — or that calls `t.Skip`/`t.Skipf` on
a connection failure. Both are findings. The repo's in-process helper is
`startTestNATSServer(t)` at
`internal/infrastructure/nats/messaging_request_test.go:21`; HTTP upstreams use
`httptest.Server`.

**Evidence:** `copilot-pull-request-reviewer`, PR #148, threads `r3547194732`
and `r3547274788`, on
`internal/infrastructure/nats/b2b_org_resolver_test.go`: *"These three new
resolver tests connect to an external NATS server at `nats.DefaultURL` and
`t.Skipf` when it is unavailable, so in CI (no broker on `localhost:4222`) they
are silently skipped and provide no coverage — including for the error-mapping
logic that was specifically added."*

Fixed in `65b90f1` (in-process `startTestNATSServer(t)` plus a
`setupB2BOrgResolverTest` helper) and `24f5df4` (`httptest.Server` for the
OpenSearch path).

**Current-code sweep at `main@bd39fe9`:** `git grep "nats.DefaultURL" -- '*.go'`
returns 0 hits and `git grep -E '\bt\.Skipf?\(' -- '*_test.go'` returns 0 hits.
The repo is clean, so this entry is pure prevention with a zero false-positive
floor today.

**Why it earns a place:** cost of miss — a test that always skips reads as
coverage on the dashboard and is worse than no test, because it stops anyone
adding a real one. Recurrence is the weakest axis here: one PR, two threads.
Treat a match as High, not Critical.

**Failure message:** This test depends on an external service and skips when it
is unavailable, so it contributes no coverage in CI.

**Fix:** use `startTestNATSServer(t)` for NATS or `httptest.Server` for HTTP
upstreams, and remove the connection-failure `t.Skip` so a broken dependency
fails the test rather than hiding it.
