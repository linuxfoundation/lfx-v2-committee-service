# Known false positives — applied LAST in every review pass

Findings that match any pattern below MUST be dropped, regardless of which source (rule file, checklist,
pattern file) originally produced them. This list is the floor — even a quotable pattern doesn't survive if
it matches a known false positive.

Consumed by two surfaces. The repo-owned local learnings brain
(`.claude/skills/committee-service-learnings-reviewer/SKILL.md`) applies this file as its floor, reading it
at **both** the reviewed commit and its pre-change base and suppressing only where the two agree — so a
waiver added by the change under review cannot excuse it, and a waiver the change removes stops applying
immediately. The GitHub PR review surface
(`.github/skills/committee-service-code-review/SKILL.md`) also consumes this directory and treats this
file as a posting floor, by human-approved design. A change here therefore changes what both surfaces
report.

**"A change cannot approve itself" is a local-review property only — do not read it as a property of this
file.** The GitHub PR surface has no base/target intersection step: it applies this floor as it stands at the
PR head, so **an entry added by a PR takes effect against that same PR** and can suppress findings about the
change that introduced it. Reviewers of a PR that edits this file should therefore judge the new entry on its
own merits and not assume a mechanism blocked its self-application. Closing that hole would require a change
under `.github/**`, which is outside this directory's ownership; it is recorded here as a known limitation
rather than silently implied to be handled.

The repo-owned **code** brain does not read this file. It is gated by a different rule — every finding must
quote a verbatim rule from the repo's written surface — so it has no floor step, and this file makes no
promise about what it emits.

---

## Already-enforced-by-tooling

### License-header complaints on a file that has one

**Pattern matched:** finding states a `.go` file is missing the MIT license header.

**Why false:** the Makefile `license-check` target and the `github-license-compliance` bot already enforce headers; Goa-generated files under `gen/` are intentionally excluded. If CI is green the header is present (or correctly exempt). Bots occasionally misread it.

**Source:** `Makefile` `license-check`; SKILL.md "License header on every new `.go` file (Goa-generated files are excluded by the Makefile's `license-check`)".

### gofmt / goimports / golangci-lint style nits

**Pattern matched:** import-block blank lines, gofmt spacing, unchecked-by-the-team style lints, "run gofmt" suggestions (e.g., PR #57 `otel_test.go:8` extra blank line in imports).

**Why false:** `make fmt` (`go fmt` + `gofmt -s -w`) and `make lint` (golangci-lint pinned in the Makefile) run in preflight/CI and own this. Surfacing a formatting-only nit is duplicate signal.

**Source:** `Makefile` `fmt`/`lint`; SKILL.md "Formatting, linting, headers".

### Doc table / markdown rendering nits (`||` leading double-pipe, fenced-block language)

**Pattern matched:** "tables start with `||` and render an extra empty column" or "add a language identifier to the fenced code block" on `docs/**` / `README.md` / `.md`.

**Why false:** these are cosmetic markdown-lint nits the team accepts; they recur on every docs PR (#70, #75, #87, #64) and are not acted on as review blockers. They do not change emitted behavior. (Substantive contract-accuracy findings on the same docs — wrong optionality, missing object type — are NOT false positives; those route to `indexer-fga-contracts/contract-doc-out-of-sync`.)

---

## Generic correctness owned by the general reviewer

### Generic nil-check / add-a-test / rename / comment-wording without a repo contract

**Pattern matched:** a bare "add a nil check", "add a unit test", "this comment should be capitalized / end with a period", "rename this variable", or "extract a helper" finding that does not tie to a committee-service contract, the `pkg/errors`/`pkg/redaction`/`pkg/constants` conventions, or a flow/chart-coupling rule.

**Why false:** generic senior-review intuition is owned by `lfx-skills:lfx-general-code-reviewer`. The learnings KB only ships findings that quote a repo-specific pattern entry. (Note: a nil-deref that panics on a Goa payload pointer IS in the KB — `goa-presentation/nil-nil-stub-or-deref` — so quote that entry when it applies; a generic nil-check elsewhere is not.)

**Source:** the committee-service code-reviewer / general-reviewer scope split; playbook §2 hard gate "Repo-specific, not generic."

**Carve-in — what "generic add-a-test" does NOT excuse.** Added 2026-07-30, because 13 Copilot findings in
the 2026-07 window were test-related and most were substantive and acted on. This entry must not be used to
drop them.

**A test that cannot fail is a finding, not generic advice** — an assertion on a value the test itself
assigned, a mock that discards the argument under test, a fake whose behaviour prevents the guarded branch
from being reached, or an assertion wrapped in an `if len(spy.calls) > 0` guard. Those have their own entry at
[`tests/assertion-cannot-fail`](tests.md), and this floor does not reach them. The same carve-in applies to
[`tests/no-external-service-dependency`](tests.md): "this test skips in CI" is a coverage defect with a
mechanical detect rule, not a request for more tests.

---

## Review-automation quirks

### CodeRabbit `🏁 Script executed:` reconnaissance dumps and `> ‼️ IMPORTANT` banners

**Pattern matched:** any text quoting a CodeRabbit `🏁 Script executed:` block, a `> ‼️ IMPORTANT` collapsed banner, or its internal `wc`/`grep` verification output.

**Why false:** this is CodeRabbit's internal reasoning, not a finding. Surfacing it is noise.

### Copilot "Add custom instructions" / promotional CTA and PR-description-vs-design "drift"

**Pattern matched:** the trailing "Improve your code reviews — add custom instructions" CTA; or "PR title/description says X but the code does Y" scope-mismatch comments (e.g., PR #75/#76/#78/#84 "update the PR description").

**Why false:** the CTA is promotional. PR-description-scope comments are process notes for the author at PR time, not code defects the learnings reviewer should re-raise on a local diff — there is no PR description in a pre-PR commit review. (If the *contract doc* is out of sync with the code, that IS a finding — route to `indexer-fga-contracts/contract-doc-out-of-sync`.)

### Dependency / toolchain version speculation

**Pattern matched:** "Go version 1.24/1.25 does not exist", "verify this OpenTelemetry/golangci-lint version exists", "the latest released version is vX.Y.Z".

**Why false:** these are point-in-time bot guesses (e.g., PR #1 "Go 1.24 does not exist", PR #55 OTel version notes) that go stale immediately and are governed by `go.mod` + the Makefile-pinned toolchain, which CI validates. Not a durable review pattern.

**Narrowed 2026-07-30 — what this entry does NOT cover.** *Dependency-version consistency* findings are real
and are not dropped here. Two were valid in the 2026-07 window and were fixed: the `go.mod` pseudo-version
pins (PR #153, fixed in `fa3044e`) and the semconv version drift between two importers (fixed in `ca05b3e`).
This entry covers point-in-time guesses about which release *exists*, not inconsistency between versions the
repo actually declares. Both of those are active entries in
[`dependencies-and-build.md`](dependencies-and-build.md) — `pseudo-version-pins-unreleased-commit` and
`versioned-import-path-drift`.

**Corrected 2026-07-31.** This block previously routed those two findings to
`/committee-service-pr-readiness` and `/committee-service-preflight`. That was false: readiness is a PR-shape
check that treats `go.mod` only as a protected path and, per `CLAUDE.md`, does not audit code, while preflight
builds, formats and lints but never compares declared versions across importers. Naming a gate that does not
perform the check dropped both findings while making this file look like it had routed them.

### Unsupported toolchain / stdlib / dependency API-existence speculation

**Pattern matched:** a claim that a **toolchain, stdlib, or third-party dependency** API does not exist —
an absent symbol, constant, kind, or method attributed to that external surface — asserted **without
proof quoted from a named frozen source in the snapshot** (`go.mod`, `go.sum`, the module or vendored
source, the Makefile-pinned toolchain version).

**Explicitly NOT floored — these survive and must be reported.** A **source-cited static contradiction**
is a finding, not speculation: an undefined symbol, a declaration the patch never adds, an
argument/signature mismatch, or an internal type contradiction that you can demonstrate by quoting the
two conflicting locations from the patch and the snapshot. Mechanical build ownership does not erase a
statically provable local-review finding. Cite both locations and report it.

**Why false:** the originating case was an error of exactly this kind. PR #139 thread `r3494956878`
asserted "the pointer-kind constant is `reflect.Ptr` (there is no `reflect.Pointer` kind). As written
this will not compile." The author declined, correctly — `reflect.Pointer` has been the canonical
constant since Go 1.18. The claim was speculation about an external API's existence, offered without
quoting the pinned toolchain that would have settled it. And the build stage owns the question for the
exact target: `/committee-service-preflight` runs `make build` and `make build-cli` on the commit under
review before any PR, and CI runs them after.

**Evidence discipline.** Do not settle an existence question by network lookup — no module proxy,
registry, or documentation fetch. Establish it from a named frozen source in the snapshot. And any appeal
to a green build must name the **exact target** it passed on: a green build on another commit, another
branch, or a different make target is not evidence about this one.

**Note the ordering.** A local pre-PR review often runs *before* any build has been attempted on the
commit, so there may be no green build for this target to point at. That does not make external-API
speculation reviewable — it makes it premature. Drop it here; the build stage answers it minutes later,
and either passes or names the exact compiler error. If it fails, fix the build rather than re-litigating
this entry.

---

## Chart-replica defaults

### KV bucket / deployment `replicas` default flagged as wrong for local clusters

**Pattern matched:** "defaulting KV bucket `replicas` to 3 prevents creation on single-node/local clusters" or "replicaCount changed from 3 to 1 affects all environments".

**Why false (conditional):** the team intentionally sets HA-oriented defaults (3) in the committed chart and overrides to 1 locally via `values.local.yaml`; deployed values live in `lfx-v2-argocd`. The bots raise this on most chart PRs (#63, #64) and the resolution is environment override, not a code change. Only valid if a PR demonstrably lowers the committed production default without an override path — author's call.

---

## Generated code

### Goa-generated schema and operation-id naming under `gen/**`

**Pattern matched:** a finding about a **deduplicated request-body schema name** or a **generated operation
id** in the generated OpenAPI document. Those two shapes only.

**Why false:** these names are Goa's own output, not authored text. `gen/` is never hand-edited, and the
naming is inherent to how Goa deduplicates and re-uses a schema across endpoints — so the finding asks either
for a design change that buys nothing or for an edit to a generated file. Cosmetic naming in a generated spec
is not worth diverging the design for.

**Evidence:** PR #139, three threads — `r3499924649`, `r3499924693`, `r3499924729`. The author declined the
schema-naming half as inherent Goa behaviour ("Goa deduplicates the schema and re-uses one across endpoints").

**Narrowed 2026-07-31 — `example:` / `default:` is NO LONGER floored.** This entry previously also suppressed
`example:` versus `default:` inconsistencies in the generated document. That was wrong, and this entry's own
evidence said so: of the three PR #139 threads, the author **accepted and fixed the example half at the design
level**. A finding that was accepted and fixed is not a false positive, and flooring it hid a real
documentation defect — a generated spec whose `example:` contradicts its `default:` misleads every API
consumer that reads it.

**So, mechanically:** an `example:`/`default:` inconsistency **is a valid finding** and must be reported. Do
not suppress it here. Report it against the **Goa design source** under `cmd/committee-api/design/`, which is
where the accepted fix landed — never as an edit to the generated document, which regeneration would discard.
The generated file is evidence of the defect, not its location.

**Boundary:** a generated document that is *stale* — missing an endpoint, or inconsistent with the design after
a change — is **not** covered here either. That is a code-reviewer matter under the generated-code boundary rule.

---

## Deliberately NOT an entry here — needs a human decision

The PR #161 reply asserting that "`.claude/skills/` is internal Claude Code skill infrastructure, not
maintained documentation for this repo" is **not** recorded as a false positive.

Adding it would suppress a rule that `.claude/skills/committee-service-dev/SKILL.md` line 156 states, and that
the same maintainer's commit `ceab5a1` obeyed 21 minutes after the finding, citing the convention in its commit
message. The contradiction is real and unresolved; a floor entry would resolve it by stealth, in the direction
of whichever side wrote the reply.

Reviewers therefore refuse to emit **or** suppress findings on that rule until a human rules. See the
README's quarantine section.

---

## How to add a new entry

When the bots (CodeRabbit `coderabbitai`, Copilot login `Copilot`/`copilot-pull-request-reviewer`) or a
human reviewer surface a finding the team has explicitly decided is not relevant for this repo:

1. Add an entry here with **Pattern matched**, **Why false**, and (where applicable) **Source**.
2. If the pattern was previously in a category `<file>.md`, remove it there — don't keep a pattern in both.
3. Permanent bot quirks (CTA text, script dumps) are durable entries; one-off misreads need no entry.

This file should accumulate slowly. If it grows past ~40 entries, that's a signal the KB is too permissive —
re-audit.
