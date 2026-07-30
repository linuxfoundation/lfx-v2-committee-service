<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Local empirical review knowledge base — `lfx-v2-committee-service`

The pattern source for the `repo_learnings` role of `lfx-local-review/v1`. Every
entry here was extracted from a **real inline review thread on this repo**,
was **observably fixed by the developer**, and has been **re-verified against
current `main`**.

This KB is local to the author-side review cycle. It is read by
`../../SKILL.md` and by nothing else.

## Why this is separate from `docs/reviews/knowledge-base/`

The repo already has a knowledge base at `docs/reviews/knowledge-base/`. Since
PR #159 that path has **two** consumers: the local pre-PR reviewer *and* the
GitHub PR review surface — `.github/skills/committee-service-code-review/SKILL.md`
names it directly and treats its false-positive file as a posting floor.

Editing it therefore changes what the bot posts on every pull request, which is
outside the scope of the local-review pilot. So it is **frozen**: read for
continuity, never written here. All refreshed evidence and every new
false-positive decision live in this directory instead.

When a legacy entry and a local entry describe the same shape, **the local
entry wins** — it is the one carrying current-code verification.

## Provenance

- **Reviewer identity** verified on the threads themselves: every finding below
  comes from a thread whose first comment's author login is
  `copilot-pull-request-reviewer`. Nothing is sourced from a review body, a PR
  summary, or a plan.
- **Window:** PRs created or merged in the 30 days to 2026-07-29 — #137,
  #139–#163. 188 Copilot-initiated inline threads across 19 PRs, all read.
- **Promotion gate**, all four required: raised by the bot; fixed by the
  developer with an observable change; repo-specific and mechanically
  detectable; not already enforced by gofmt, lint, or CI. Plus at least one
  value signal — recurrence, cost-of-miss, or acted-on authority.
- **Evidence baseline:** `origin/main@ec86a8f`. Every entry was then
  **re-verified against `origin/main@bd39fe9`** (PRs #162 and #165 landed in
  between); line numbers in the entries are from `bd39fe9`.
- Mining method: the `empirical-review-kb-mining` playbook, which lives outside
  this repository with the agent that runs it.

## Categories

| File | Patterns | Read when |
|---|---|---|
| [`nats-storage-kv.md`](nats-storage-kv.md) | 1 | `internal/infrastructure/nats/**`, `*writer.go`/`*reader.go`, `pkg/constants/storage.go`, `internal/infrastructure/mock/**`, `cmd/committee-cli/commands/sync/**` |
| [`indexer-fga-contracts.md`](indexer-fga-contracts.md) | 2 | `*writer.go`, `messaging_publish.go`, `pkg/constants/subjects.go`, `docs/indexer-contract.md`, `docs/fga-contract.md`, `scripts/migrations/**` |
| [`chart-and-concurrency.md`](chart-and-concurrency.md) | 1 | `charts/lfx-v2-committee-service/**`, `pkg/constants/subjects.go`, `committee_handler.go`, `providers.go`, `cmd/committee-api/design/**` |
| [`goa-presentation.md`](goa-presentation.md) | 1 | `cmd/committee-api/design/**`, `cmd/committee-api/service/**`, `cmd/committee-api/http.go` |
| [`invite-application-flows.md`](invite-application-flows.md) | 1 | invite/application/join/leave handlers, invite/application models, `docs/invite-application-flows.md` |
| [`logging-errors-secrets.md`](logging-errors-secrets.md) | 1 | any `.go` that logs or builds an error |
| [`tests.md`](tests.md) | 2 | any `*_test.go`, or a fake under `internal/infrastructure/mock/` |
| [`known-false-positives.md`](known-false-positives.md) | 2 + a carve-in | always — applied LAST |

**9 patterns** across 7 category files, plus the local false-positive floor.
Deliberately small: this is the delta the last mining pass could prove, not a
restatement of the legacy KB.

## Legacy KB status — advisory, not applied

The 30 legacy entries were re-audited against `ec86a8f`. **None was obsolete;
none should be dropped.** Because that file set is frozen, the audit is recorded
here as advice for whoever unfreezes it, and as a caution for reviewers reading
legacy entries today:

**Legacy entries whose citations point at files that no longer exist** — the
invariant still holds, but the cited line will not resolve:
`indexer-fga-contracts/migration-must-use-envelope`,
`nats-storage-kv/missing-existence-guard`,
`goa-presentation/nil-nil-stub-or-deref`,
`logging-errors-secrets/no-raw-secret-or-url`,
`logging-errors-secrets/sentinel-not-text-match`, and the
`invite-application-flows/principal-is-not-email` signature description.

**Legacy `Detect:` clauses that now fire on correct-by-convention code** —
treat a match as a prompt to look, not as a finding:

- `indexer-fga-contracts/missing-indexing-config` requires `IndexingConfig` on
  *all* actions including delete, but `internal/domain/model/committee_message.go`
  says callers populate it "for create and update actions" and five delete paths
  deliberately send only the UID.
- `nats-storage-kv/orphaned-object-on-metadata-failure` fires on a documented
  accepted trade-off written into `internal/service/document_writer.go`.
- `invite-application-flows/member-before-terminal-status` fires on the
  idempotent already-accepted branch added by PR #150, which returns before the
  transition path the rule governs.
- `chart-and-concurrency/worker-pool-and-goroutine-hygiene` has a literal
  `NewWorkerPool(len(messages))` detect that currently yields five false
  positives and no true ones.

**Legacy entries with live violations in current code** — still earning their
place: `contract-doc-out-of-sync`, `conflict-mapping`,
`lookup-key-reservation-rollback`, `readonly-field-leak`,
`etag-if-match-required`, `silent-failure`,
`total-members-recount-correctness`.

## Deliberately excluded

Kept as context so a future pass does not re-litigate them. None is a pattern:

- **No observable fix.** All of PR #142 (closed unmerged — the fix never reached
  `main`); PR #145's open WIP finding; the PR #161 deferred set (CAS mapping,
  serial settings scan, FGA old-tuple revocation, `EachMember` partial-read
  swallowing, the A→B→A stale-index race, the `member_remove` contract-doc
  update); PR #150's silent-skip 409 and its `EqualFold`-without-`TrimSpace`
  thread; declined items on PR #154 and #161. Several of these *reinforce*
  legacy entries and are cited above as live violations — an agreement is not a
  fix, and only a fix promotes.
- **Not a code pattern.** All 19 PR #159 threads: they are findings about the
  `.github` review instructions themselves, fixed by prose edits. Valuable, but
  not committee-service code shapes.
- **Findings against deleted code**, e.g. the PR #148 threads on `pkg/orgid` and
  the m2m `b2b_org_resolver`, superseded in later commits.
- **Routed to the mechanical gates, not here.** `go.mod` pseudo-version pins
  (PR #153) belong in `/committee-service-pr-readiness`; dependency-version
  consistency such as the semconv drift, and `gen/` regenerated with
  `goa gen -o .` instead of `make apigen` (PR #143), belong in
  `/committee-service-preflight`.
- **Excluded by the mining source rule.** All 46 in-window `coderabbitai`
  threads and all human-reviewer threads. Not a judgement on their quality —
  this pass mined one reviewer so the provenance stays uniform.

## Two contradictions quarantined for a human decision

Neither is encoded as a rule, in either direction, and neither may produce a
finding until a human rules:

1. **The `committee-service-dev` layering rule contradicts itself and the
   code.** Its lines 74-76 put business logic in `internal/service/`, while
   line 168-169 of the same file names `cmd/committee-api/service/committee_service.go`
   as the file the invite/application flow doc must match — and that is where
   the state machine actually lives.
2. **Whether `.claude/skills/**` is maintained documentation.** `SKILL.md`
   line 156 requires `references/nats-messaging.md` to be updated alongside a
   subject or bucket change. A PR #161 thread reply asserted the opposite,
   21 minutes before the same maintainer's commit `ceab5a1` obeyed the rule and
   cited it in the commit message.

Recording a contradiction is not resolving it. `../../SKILL.md` and the
`repo_code` brain both refuse findings on these two.

## Maintenance

Re-mine after a batch of merged PRs. Promote only what clears all four gates,
and re-verify every retained entry against the current tree — an entry whose
citation has rotted is worse than no entry, because it teaches a reviewer to
trust a line that is not there.
