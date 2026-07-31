# CLAUDE.md

This file provides guidance to Claude Code when working with the LFX v2 Committee Service.

> **Central LFX skills (always available, do not duplicate here):**
>
> - `lfx-skills:lfx`: cross-repo topology, ownership routing, "where does X live", repo discovery, missing-checkout handling.
> - `lfx-skills:lfx-platform-architecture`: V2 platform composition, service classes (native, wrapper, proxy, platform), write/read/access-check/index flows, NATS and KV ownership, and handoff points across Self Serve, Goa services, OpenFGA, fga-sync, indexer-service, query-service, access-check, Heimdall, Helm, and ArgoCD.
>
> **Repo-local skills (owned here, not in central `lfx-skills`):**
>
> - `committee-service-dev` auto-attaches on Go, docs, and service-chart paths (`cmd/`, `internal/`, `pkg/`, `gen/`, `docs/`, `charts/lfx-v2-committee-service/`, `Makefile`, `go.mod`, `go.sum`, Goa design files) and owns generated-code boundary, logging via `pkg/log`, the `pkg/errors` family and its Goa mapping, request-context propagation via `pkg/constants`, NATS subject / KV / Object Store coding rules, committee-owned indexer and FGA contract docs, table-driven tests with `internal/infrastructure/mock` fakes, gofmt/golangci-lint hygiene, and license headers. See `.claude/skills/committee-service-dev/SKILL.md`.
> - `committee-service-pr-readiness` is the before-PR shape check for branch/JIRA/conventional commits/rebase/DCO+GPG/diff size/protected files. It does not audit code. See `.claude/skills/committee-service-pr-readiness/SKILL.md`.
> - `committee-service-preflight` is the before-PR Go mechanical preflight for working tree state, license headers, formatting, lint, API/CLI builds, tests, protected files, commit verification, and PR change summary. See `.claude/skills/committee-service-preflight/SKILL.md`.
>
> If the plugin is missing, install with `/plugin marketplace add linuxfoundation/lfx-skills` then `/plugin install lfx-skills@lfx-skills`.

## Repo Role

This repo owns committee resources, committee members, committee settings, invite/application flows, the committee CLI, and the service's indexer and FGA event contracts. Classified as a **native V2 service** (owns its NATS KV state; publishes FGA tuples and indexer messages; consumes from project-service).

## Authoritative repo docs

- `README.md`: file structure, key features, release process, contributor flow.
- `docs/indexer-contract.md`: what this service emits on `lfx.index.*` subjects.
- `docs/fga-contract.md`: what this service emits on `lfx.fga-sync.*` subjects.
- `docs/invite-application-flows.md`: committee membership modes, invite/application state machines, edge cases.
- `charts/lfx-v2-committee-service/`: service-local Helm templates and values.

Read the relevant contract before changing emitted events, permissions, invite state, or application state. Update the contract in the same PR as any behavior change.

## Consumed Cross-Repo Contracts

- Generic FGA envelope: `lfx-v2-fga-sync/docs/fga-sync-contract.md`
- Generic indexer event contract: `lfx-v2-indexer-service/docs/indexer-contract.md`
- OpenFGA model: `lfx-v2-helm/charts/lfx-platform/templates/openfga/model.yaml`
- Service chart conventions: `lfx-v2-helm/docs/service-chart-patterns.md`

Use `/lfx-skills:lfx` if an owner repo is missing locally, the path has moved,
or the task needs additional peer repos.

## Common Commands

```bash
make deps      # install Goa toolchain pinned in Makefile
make apigen    # regenerate gen/ from Goa design (cmd/committee-api/design/)
make build     # build the API binary
make build-cli # build the committee CLI binary
make test      # run unit tests
make lint      # run golangci-lint pinned in Makefile
make fmt       # go fmt + gofmt -s -w
```

Run `make apigen` after editing any file under `cmd/committee-api/design/`. Never hand-edit `gen/`.

## Work cycle — post-commit and pre-PR reviews

> **CRITICAL — while the branch is pre-PR, post-commit review is mandatory.** After every commit on the local branch, run **`/lfx-skills:lfx-local-review`**. It runs three reviewers in parallel — the central `general` brain plus this repo's own `repo_code` and `repo_learnings` brains — on headless Pi when Pi is available, and on Claude subagents otherwise, and returns their ordinary Markdown reports. Before opening a PR, drain every report, run the **full-branch sweep** if the branch has more than one commit, AND let `/committee-service-pr-readiness` clear every Critical finding before `/committee-service-preflight` runs.
>
> **Once the PR is open, do NOT run local review on iteration commits.** CodeRabbit + Copilot auto-trigger on every push and own the audit surface from that point. Local review is pre-PR insurance only, and it stops at PR-open.

**This repo owns two of the three review brains.** They live at `.claude/skills/committee-service-code-reviewer/SKILL.md` and `.claude/skills/committee-service-learnings-reviewer/SKILL.md`, and the host finds them through the `local-code-review` and `local-learnings-review` discovery aliases beside them. The `general` brain is central and carries no repo-specific rules.

The learnings brain reads the repo's canonical empirical knowledge base at `docs/reviews/knowledge-base/` — the single KB for this repo, deliberately not duplicated under the skill tree. That directory is **also read by the GitHub PR review surface** (`.github/skills/committee-service-code-review/SKILL.md`, which treats its `known-false-positives.md` as a posting floor), so an edit there changes what the PR bot posts as well as what local review flags. That is intended: one path, one truth. Keep `Detect:` clauses narrow, and re-verify citations against current code when you touch an entry.

One consequence worth knowing before you argue with a finding: **the false-positive floor is read at two revisions — the pre-change base and your commit — and it suppresses only when both agree.** A waiver you add in the same change cannot suppress a finding about that change, because the base does not carry it yet; a waiver you *remove* stops suppressing at once, because removing it means "start flagging this again". Ordinary pattern files are unaffected — those are read at your commit as usual.

Which reviewed range a new waiver reaches depends on that range's base, so be precise rather than general. Post-commit mode bases each commit on its parent, so a waiver added in an earlier commit **does** apply to a later commit's review — both ends of that delta carry it, and it is suppressing a finding about some other change. The pre-PR **branch sweep** is the one that cannot be talked round: its base is the merge-base with `origin/main`, which predates every commit on the branch, so a waiver added anywhere on the branch never suppresses anything in the cumulative range. **To widen the floor for your own work, land the widening on `main` first.** Otherwise the sweep will keep reporting the finding, and the honest options are to fix it or to document it as a trade-off — not to add a waiver the sweep cannot see.

### Post-commit (pre-PR phase, after every commit)

1. **Commit your work.** `git commit -s -S`.
2. **Run `/lfx-skills:lfx-local-review`** — post-commit mode, which reviews the new commit against its first parent. No arguments needed. Run it from inside the repo, or pass a resolved `--repo <path>`; never a bare repo name.
3. **Relay all three reports in full and unedited.** They are ordinary Markdown, one per role. On a fallback run, say plainly that Pi was skipped and this was a same-model review — honest evidence, but not the *cross-model* check Pi provides.
4. **An incomplete cycle is not a pass, and one reviewer can spoil it.** If any reviewer's report starts `INCOMPLETE — <reason>`, or the host reports a failed or empty child, **the whole cycle is incomplete** — successful siblings do not rescue it. Resolve the cause and rerun the **complete trio under one harness**. Never rerun a single role, never mix Pi and Claude evidence in one cycle, and never render a failed child as "no findings".
5. **Address every real Critical and reasonable Important finding in this session**, then commit the fixes as their own signed conventional commits — `fix(<scope>): ...` or `fix: ...` — rather than amending. Reviewers report; you fix.
6. **Rerun the complete trio after each fix commit.**

### Pre-PR (sweep cumulative state, then open)

When the work is done and no more code commits are planned:

1. **Drain the post-commit reports.** If the last run had findings, fix them, commit, and rerun the trio until the reports are clean or the remainder is explicitly documented as a trade-off.
2. **Full-branch sweep — only if the branch has more than one commit.** Run **`/lfx-skills:lfx-local-review branch`**. The host fetches `origin` once and pins the merge-base with `origin/main` before any reviewer starts, so branch mode needs network while post-commit mode does not. Address any new findings with further signed fix commits, then re-run the sweep until clean.
3. **Run `/committee-service-pr-readiness [base-branch]`** for branch, JIRA, conventional commits, rebase, DCO+GPG, diff size, and protected files.
4. **Run `/committee-service-preflight [base-branch]`** for working tree, license headers, formatting, lint, API/CLI builds, tests, protected files, commit verification, and PR change summary.
5. **Only then push and open the PR.** Reviewers may run useful builds, tests and linters, but only this session edits, commits, or cleans up anything they leave behind.

### Post-PR iteration (responding to bot feedback on an open PR)

1. Wait for CodeRabbit + Copilot to comment after each push.
2. Triage every Critical and reasonable Important finding against current code.
3. Roll fixes into a `fix(review): ...` commit.
4. Push. Repeat until clean.

## Boundaries

- Committee storage and committee-specific NATS handlers stay in this repo.
- `lfx-v2-fga-sync` owns OpenFGA tuple write mechanics and the generic FGA message envelope.
- `lfx-v2-indexer-service` owns indexing infrastructure behavior and the `IndexerMessageEnvelope` contract.
- `lfx-v2-helm` owns cross-service chart conventions; this repo only owns its own chart under `charts/lfx-v2-committee-service/`.
- `lfx-v2-argocd` owns deployed values, image tags, and environment promotion.
