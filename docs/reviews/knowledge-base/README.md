# Committee Service Review Knowledge Base

Empirical review-pattern knowledge base for `lfx-v2-committee-service`. Each pattern entry was extracted
from a real review comment on a **merged** PR in this repo (CodeRabbit, Copilot, or a human maintainer) and
cleared the promotion gate in the service-KB research playbook (maintained outside this repo).

The KB is the *empirical* surface — patterns the bots and reviewers have actually flagged on this repo. It
does **not** duplicate generic correctness/security review, or the documented-rule-surface audit that the
repo's code reviewer performs. Generic findings without a quotable pattern entry are dropped.

**This is the single canonical KB for the repo.** There is deliberately no second copy under any skill tree:
a duplicated KB drifts, and the drifting copy is always the one a reviewer happens to read.

## How it's used

Two consumers read this directory, and both benefit from it being current:

- **Local pre-PR review** — the `repo_learnings` role of `/lfx-skills:lfx-local-review`, whose brain lives at
  `.claude/skills/committee-service-learnings-reviewer/SKILL.md`. It runs after every commit while the branch
  is pre-PR.
- **The GitHub PR review surface** — `.github/skills/committee-service-code-review/SKILL.md` names this
  directory directly and treats `known-false-positives.md` as its posting floor.

Either consumer reads the category files routed by changed-file path, matches each entry's `**Detect:**` rule
against the diff, and emits only findings it can quote — then applies `known-false-positives.md` as the floor.

The two consumers differ in **which revision they read the floor from**. Local review reads the pattern files
at the commit under review, but reads the floor at **two** revisions — the pre-change base and the commit
itself — and suppresses a finding only when **both** floors would suppress that exact finding. Each revision
alone has a hole, and requiring both closes each with the other:

| The reviewed range… | base floor | target floor | result |
| --- | --- | --- | --- |
| **adds** a waiver | does not suppress | suppresses | **not suppressed** — a change cannot approve itself |
| **removes** a waiver | suppresses | does not suppress | **not suppressed** — removal means "flag this again" |
| leaves it unchanged | suppresses | suppresses | **suppressed** |

Widening and narrowing behave the same way: they cannot hide a finding unless the unchanged overlap still
suppresses it at both revisions. Coverage is judged per finding, semantically — the two floors are never
diffed or byte-compared.

Which reviewed range a **newly added** waiver reaches depends on that range's base. It cannot suppress
anything in the commit that adds it. It *can* suppress in a later commit's review, whose parent already
carries it — that is correct, not a leak, since the finding belongs to a different change. And it never
suppresses anything in the cumulative pre-PR branch sweep, whose base is the merge-base with `main` and so
predates the whole branch. **To widen the floor for work in progress, land the widening on `main` first.**

Because the PR surface shares this KB, a change here changes what the PR bot posts. That is intended: one
path, one truth. It also means an entry whose `Detect` is too broad costs real reviewer noise on every PR, so
prefer narrowing a clause over leaving it aspirational.

## Methodology

This KB has been built in two passes. Both used the same promotion gate.

**Pass 1 — 2026-05-29, corpus-wide.**

- **Corpus:** 90 merged PRs (full available history at the time, PR #1–#102; numbers are non-contiguous due to
  closed/unmerged PRs). Enumerated via `gh pr list --state merged`.
- **Surfaces pulled per PR:** inline review threads (GraphQL `reviewThreads`, with `isResolved`/`isOutdated`),
  review bodies, and PR conversation comments.
- **Comment volume:** 822 inline review threads — 385 Copilot (`copilot-pull-request-reviewer`),
  285 CodeRabbit (`coderabbitai`), 152 human (jordane 61, andrest50 34, dealako 22, prabodhcs/bramwelt/
  mauriciozanettisalomao/others). 673 of 822 inline threads were resolved (acted-on signal).
- **Bots active:** CodeRabbit (`coderabbitai`) **on**, Copilot (`Copilot`) **on**.

**Pass 2 — 2026-07-30, recent-window refresh and re-audit.**

- **Window:** PRs created or merged in the 30 days to 2026-07-29 — #137, #139–#163. 188
  Copilot-initiated inline threads across 19 PRs, all read.
- **Reviewer identity verified on the threads themselves:** every pass-2 finding comes from a thread whose
  first comment's author login is `copilot-pull-request-reviewer`. Nothing is sourced from a review body, a PR
  summary, or a plan. Copilot now runs roughly 4:1 over CodeRabbit by in-window thread volume.
- **Scope note:** the 46 in-window `coderabbitai` threads and all in-window human-reviewer threads were
  excluded from pass 2 so its provenance stays uniform. That is not a judgement on their quality, and pass-1
  entries sourced from those reviewers are unaffected.
- **Evidence baseline:** `origin/main@ec86a8f`, with every claim re-verified against `origin/main@bd39fe9`
  (PRs #162 and #165 landed in between); line numbers in pass-2 text are from `bd39fe9`.
- **Re-audit result:** all 30 pass-1 entries were re-checked against current code. **None was obsolete and
  none was dropped.** 16 were retained unchanged; 14 were revised in place and carry a dated
  `**Revised …**` block stating what changed and why. Revisions were of three kinds: citations that had rotted
  because the cited file was deleted or moved, `Detect` clauses that had started firing on
  correct-by-convention code, and entries that needed extending to cover a shape the window proved.

**Gate (both passes):** all hard gates (repo-specific, mechanically detectable+fixable, currently-relevant
against the tree, not already enforced by gofmt/lint/CI) + at least one value signal (recurrence ≥2 PRs,
cost-of-miss, or acted-on authority). Every entry carries a real `PR #N file:line` citation + quoted phrase.

## Categories

| File | Patterns | Read when |
| --- | --- | --- |
| [`indexer-fga-contracts.md`](indexer-fga-contracts.md) | 6 | indexer/FGA emission code, `Tags()`/`Build`, `pkg/constants/subjects.go`, `messaging_publish.go`, `docs/indexer-contract.md`, `docs/fga-contract.md`, migration scripts publishing to index/fga subjects |
| [`nats-storage-kv.md`](nats-storage-kv.md) | 7 | `internal/infrastructure/nats/**`, `*writer.go`/`*reader.go`, handler-level existence guards, `internal/infrastructure/mock/**`, `pkg/constants/storage.go`, `cmd/committee-cli/commands/sync/**` |
| [`invite-application-flows.md`](invite-application-flows.md) | 6 | invite/application/join/leave handlers, invite/application models, invite-accepted handling, caller-identity resolution, `docs/invite-application-flows.md` |
| [`goa-presentation.md`](goa-presentation.md) | 6 | `cmd/committee-api/service/**`, `cmd/committee-api/design/**`, `cmd/committee-api/http.go` |
| [`logging-errors-secrets.md`](logging-errors-secrets.md) | 5 | any `.go` that logs, returns/builds errors, or handles tokens — service, nats infra, presentation, CLI, migrations |
| [`chart-and-concurrency.md`](chart-and-concurrency.md) | 5 | `charts/lfx-v2-committee-service/**`, `pkg/constants/{storage,subjects}.go`, new endpoints, `providers.go` env vars and subscriptions, `committee_handler.go`, goroutine/consumer code |
| [`tests.md`](tests.md) | 2 | **always** — one shape triggers on a production guard with no test that can exercise it |
| [`known-false-positives.md`](known-false-positives.md) | 10 (floor filter) + a carve-in | always — applied LAST to drop findings |

**37 patterns** across 7 category files, plus the false-positive floor. Kept sharp rather than exhaustive.

## Highest-value patterns

- `indexer-fga-contracts/contract-doc-out-of-sync` — the most-flagged shape in the repo's history, with a
  live violation in current code (`member_remove` on username-clearing updates).
- `chart-and-concurrency/handler-registered-but-not-subscribed` and `/new-endpoint-needs-ruleset` —
  code/deployment lockstep. Miss either and the code compiles, the tests pass, and the event or endpoint is
  unreachable in every deployed environment (PR #61, #97, #98, #161).
- `nats-storage-kv/new-secondary-index-needs-backfill-and-cleanup` — a half-shipped index makes the
  user-deletion scrub a silent blind spot, permanently (PR #161).
- `invite-application-flows/auth-service-failure-not-validation` and `/principal-is-not-email` — coupled, and
  now an authorization concern: a wrong `NotFound` opens the JWT-email identity fallback (PR #61, #65, #156).
- `goa-presentation/url-scheme-allowlist` — stored-XSS vector via a persisted `javascript:` URI (PR #149).
- `nats-storage-kv/delete-must-use-revision` and `/conflict-mapping` — optimistic-locking discipline across
  every KV adapter (PR #19, #68, #71, #74, #92).
- `logging-errors-secrets/pii-in-logs` — recurs across member/invite/subscriber/notification flows, and now
  also catches a redaction being *removed* (PR #16, #44, #61, #91, #152, #156).
- `tests/assertion-cannot-fail` — four PRs in one window, and the current tree still contains an instance.

## Two contradictions quarantined for a human decision

Neither is encoded as a rule, in either direction, and neither may produce a finding until a human rules:

1. **The `committee-service-dev` layering rule contradicts itself and the code.** Its lines 74-76 put business
   logic in `internal/service/`, while lines 168-169 of the same file name
   `cmd/committee-api/service/committee_service.go` as the file the invite/application flow doc must match —
   and that is where the state machine actually lives.
2. **Whether `.claude/skills/**` is maintained documentation.** `committee-service-dev/SKILL.md` line 156
   requires `references/nats-messaging.md` to be updated alongside a subject or bucket change. A PR #161
   thread reply asserted the opposite, 21 minutes before the same maintainer's commit `ceab5a1` obeyed the
   rule and cited it in the commit message.

Recording a contradiction is not resolving it. Reviewers refuse findings on these two.

## Deliberately excluded

Kept as context so a future pass does not re-litigate them. None is a pattern:

- **No observable fix.** All of PR #142 (closed unmerged — the fix never reached `main`); PR #145's open WIP
  finding; the PR #161 deferred set (CAS mapping, serial settings scan, FGA old-tuple revocation,
  `EachMember` partial-read swallowing, the A→B→A stale-index race, the `member_remove` contract-doc update);
  PR #150's silent-skip 409 and its `EqualFold`-without-`TrimSpace` thread; declined items on PR #154 and
  #161. Several of these *reinforce* existing entries and are cited in them as live violations — an agreement
  is not a fix, and only a fix promotes.
- **Not a code pattern.** All 19 PR #159 threads: they are findings about the `.github` review instructions
  themselves, fixed by prose edits. Valuable, but not committee-service code shapes.
- **Findings against deleted code**, e.g. the PR #148 threads on `pkg/orgid` and the m2m `b2b_org_resolver`,
  superseded in later commits.
- **Routed to the mechanical gates, not here.** `go.mod` pseudo-version pins (PR #153) belong in
  `/committee-service-pr-readiness`; dependency-version consistency such as the semconv drift, and `gen/`
  regenerated with `goa gen -o .` instead of `make apigen` (PR #143), belong in
  `/committee-service-preflight`.

## Maintenance

Re-run the playbook research against newly merged PRs periodically. Promote a candidate only if it clears the
gate; demote bot nitpicks unless they recur or were acted on. Move team-rejected findings to
`known-false-positives.md` (and remove them from the category file).

**Re-verify retained entries against the current tree, not just newly promoted ones.** The 2026-07-30 pass
found 14 of 30 entries needing revision — mostly rotted citations and `Detect` clauses that had drifted into
firing on correct code. An entry whose citation has rotted is worse than no entry, because it teaches a
reviewer to trust a line that is not there; an entry that fires on correct-by-convention code trains everyone
to ignore the file.
