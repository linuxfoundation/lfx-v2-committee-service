<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Known false positives — applied LAST

A finding that matches an entry below is dropped, whatever produced it. **The
floor wins even over a quotable pattern match.**

This file carries the false-positive decisions made in the local review cycle.
The legacy floor at `docs/reviews/knowledge-base/known-false-positives.md` is
frozen but **still applies** — read it too when a finding looks like it might
land on one of its seven entries (license headers, gofmt/lint nits, markdown
table nits, generic nil-check/add-a-test/rename, CodeRabbit script dumps,
Copilot CTA and PR-description drift, chart replica defaults). All seven were
re-audited in the last mining pass and all seven were confirmed worth keeping.

---

## "This will not compile" / "this symbol does not exist"

**Pattern matched:** a finding asserting that the patch cannot build — an
undefined symbol, a constant that does not exist, a type error.

**Why false:** compilability is not this reviewer's to decide. A reviewer here
has read-only tools and no shell, so it cannot build and cannot know — and the
repo owns the question mechanically: `/committee-service-preflight` runs
`make build` and `make build-cli` before any PR, and CI runs them after.

**Note the ordering.** The original finding below was raised at PR time against
green CI, so it contradicted evidence that already existed. A local pre-PR
review usually runs *before* any build has been attempted on the commit, so
there is no green build to point at. That does not make the claim reviewable —
it makes it premature. Drop it here; preflight answers it minutes later, and
either passes or names the exact compiler error. If preflight fails, fix the
build rather than re-litigating this entry.

**Evidence:** PR #139, thread `r3494956878`: *"the pointer-kind constant is
`reflect.Ptr` (there is no `reflect.Pointer` kind). As written this will not
compile."* The author declined, correctly — `reflect.Pointer` has been the
canonical constant since Go 1.18, and CI was green.

**Explicitly not covered by this entry:** *dependency-version consistency*
findings. The legacy floor's "dependency / toolchain version speculation" entry
covers point-in-time guesses about which release exists. It does **not** cover
two version findings that were valid and were fixed in this window — the
`go.mod` pseudo-version pins (PR #153, fixed in `fa3044e`) and the semconv
version drift between two importers (fixed in `ca05b3e`). Those are real, and
they belong to `/committee-service-pr-readiness` and
`/committee-service-preflight` respectively rather than to a review brain.

---

## Goa-generated OpenAPI naming and example nits under `gen/**`

**Pattern matched:** a finding about a deduplicated request-body schema name, a
generated operation id, or `example:` versus `default:` in the generated
OpenAPI document.

**Why false:** `gen/` is never hand-edited, so such a finding is either a
design-level change or nothing at all. Cosmetic naming in a generated spec is
not worth diverging the design for.

**Evidence:** PR #139, three threads — `r3499924649`, `r3499924693`,
`r3499924729`. The author declined the schema-naming half as inherent Goa
behaviour (*"Goa deduplicates the schema and re-uses one across endpoints"*) and
fixed only the example half, at the design level.

**Boundary:** a generated document that is *stale* — missing an endpoint, or
inconsistent with the design after a change — is not covered here. That is a
`repo_code` matter under the generated-code boundary rule.

---

## Carve-in: what "generic add-a-test" does NOT excuse

The legacy floor drops a bare *"add a unit test"* as generic advice, and it is
right to. But 13 Copilot findings in the last window were test-related and most
were substantive and acted on, so the entry must not be used to drop them.

**A test that cannot fail is a finding, not generic advice** — an assertion on
a value the test itself assigned, a mock that discards the argument under test,
a fake whose behaviour prevents the guarded branch from being reached, or an
assertion wrapped in a `if len(spy.calls) > 0` guard. Those have their own
entry at [`tests/assertion-cannot-fail`](tests.md), and the floor does not
reach them.

The same carve-in applies to [`tests/no-external-service-dependency`](tests.md):
"this test skips in CI" is a coverage defect with a mechanical detect rule, not
a request for more tests.

---

## Deliberately NOT an entry here — needs a human decision

The PR #161 reply asserting that *"`.claude/skills/` is internal Claude Code
skill infrastructure, not maintained documentation for this repo"* is **not**
recorded as a false positive.

Adding it would suppress a rule that `.claude/skills/committee-service-dev/SKILL.md`
line 156 states, and that the same maintainer's commit `ceab5a1` obeyed 21
minutes after the finding, citing the convention in its commit message. The
contradiction is real and unresolved; a floor entry would resolve it by
stealth, in the direction of whichever side wrote the reply.

Both brains therefore refuse to emit **or** suppress findings on that rule
until a human rules. See the README's quarantine section.

---

## Adding an entry

Record **Pattern matched**, **Why false**, and the **evidence** — the thread and
the team's actual decision. A false positive is a decision the team made, not a
shape a reviewer found annoying. If this file grows past a dozen entries, the
pattern files are too permissive; re-audit those instead.
