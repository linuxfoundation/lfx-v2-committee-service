# Committee Service Review Knowledge Base

Empirical review-pattern knowledge base for `lfx-v2-committee-service`. Each pattern entry was extracted
from a real review comment on a **merged** PR in this repo (CodeRabbit, Copilot, or a human maintainer) and
cleared the promotion gate in the service-KB research playbook (maintained outside this repo).

The KB is the *empirical* surface — patterns the bots and reviewers have actually flagged on this repo. It
does **not** duplicate `lfx-skills:lfx-general-code-reviewer` (generic correctness/security) or
`lfx-skills:lfx-committee-service-code-reviewer` (documented rule-surface audit). Generic findings without a
quotable pattern entry are dropped.

## How it's used

The `lfx-skills:lfx-committee-service-learnings-reviewer` subagent reads the category files routed by
changed-file path, matches each entry's `**Detect:**` rule against the diff, and emits only findings it can
quote — then applies `known-false-positives.md` as the floor. Run it in parallel with
`lfx-skills:lfx-committee-service-code-reviewer` after every pre-PR commit (see `CLAUDE.md` work cycle).

## Methodology

- **Corpus:** 90 merged PRs (full available history, PR #1–#102; numbers are non-contiguous due to
  closed/unmerged PRs). Enumerated via `gh pr list --state merged`.
- **Surfaces pulled per PR:** inline review threads (GraphQL `reviewThreads`, with `isResolved`/`isOutdated`),
  review bodies, and PR conversation comments.
- **Comment volume:** 822 inline review threads — 385 Copilot (`copilot-pull-request-reviewer`),
  285 CodeRabbit (`coderabbitai`), 152 human (jordane 61, andrest50 34, dealako 22, prabodhcs/bramwelt/
  mauriciozanettisalomao/others). 673 of 822 inline threads were resolved (acted-on signal). CodeRabbit also
  posts a walkthrough as an issue comment on every PR; Copilot posts inline + a review summary.
- **Bots active:** CodeRabbit (`coderabbitai`) **on**, Copilot (`Copilot`) **on**.
- **Gate:** all hard gates (repo-specific, mechanically detectable+fixable, currently-relevant against the
  tree, not already enforced by gofmt/lint/CI) + at least one value signal (recurrence ≥2 PRs, cost-of-miss,
  or acted-on authority). Every entry carries a real `PR #N file:line` citation + quoted phrase.
- **Date:** 2026-05-29.

## Categories

| File | Patterns | Read when |
| --- | --- | --- |
| [`indexer-fga-contracts.md`](indexer-fga-contracts.md) | 5 | indexer/FGA emission code, `Tags()`/`Build`, `pkg/constants/subjects.go`, `docs/indexer-contract.md`, `docs/fga-contract.md`, migration scripts publishing to index/fga subjects |
| [`nats-storage-kv.md`](nats-storage-kv.md) | 6 | `internal/infrastructure/nats/**`, `*writer.go`/`*reader.go`, handler-level existence guards, `internal/infrastructure/mock/**` |
| [`invite-application-flows.md`](invite-application-flows.md) | 5 | invite/application/join/leave handlers, invite/application models, invite-accepted handling, `docs/invite-application-flows.md` |
| [`goa-presentation.md`](goa-presentation.md) | 5 | `cmd/committee-api/service/**`, `cmd/committee-api/design/**`, `cmd/committee-api/http.go` |
| [`logging-errors-secrets.md`](logging-errors-secrets.md) | 5 | any `.go` that logs, returns/builds errors, or handles tokens — service, nats infra, presentation, CLI, migrations |
| [`chart-and-concurrency.md`](chart-and-concurrency.md) | 4 | `charts/lfx-v2-committee-service/**`, `pkg/constants/{storage,subjects}.go`, new endpoints, `providers.go` env vars, goroutine/consumer/RNG code |
| [`known-false-positives.md`](known-false-positives.md) | 7 (floor filter) | always — applied LAST to drop findings |

**30 patterns** across 6 category files, plus the false-positive floor. Scaled to a 90-PR corpus (self-serve
landed 77 over ~68 PRs); kept sharp rather than exhaustive.

## Highest-value patterns

- `indexer-fga-contracts/missing-indexing-config` — new sub-resources have no indexer enricher; a missing
  `IndexingConfig` silently drops the document (PR #61, #68).
- `invite-application-flows/member-before-terminal-status` and `/principal-is-not-email` — strand records or
  create members with invalid emails (PR #61, #64).
- `chart-and-concurrency/new-endpoint-needs-ruleset` and `/new-bucket-or-env-needs-chart` — code/chart
  lockstep; miss it and the endpoint is blocked or the bucket doesn't exist (PR #61, #97, #98).
- `nats-storage-kv/delete-must-use-revision` and `/conflict-mapping` — optimistic-locking discipline across
  every KV adapter (PR #19, #68, #71, #74, #92).
- `logging-errors-secrets/pii-in-logs` — recurs across member/invite/subscriber/notification flows (PR #16,
  #44, #61, #91).

## Maintenance

Re-run the playbook research against newly merged PRs periodically. Promote a candidate only if it clears the
gate; demote bot nitpicks unless they recur or were acted on. Move team-rejected findings to
`known-false-positives.md` (and remove them from the category file).
