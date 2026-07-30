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

> **CRITICAL — while the branch is pre-PR, post-commit review is mandatory.** After every commit on the local branch, run **`/lfx-skills:lfx-local-review`**. It runs three reviewers in parallel against a snapshot of the commit — the central `general` brain plus this repo's own `repo_code` and `repo_learnings` brains — on headless Pi (GPT-5.6 Sol) when Pi is available, and on Claude subagents otherwise. Before opening a PR, every run must end `COMPLETE_NO_FINDINGS` (or its findings explicitly documented as trade-offs), the **full-branch sweep** must end the same way if the branch has more than one commit (`--mode branch`), AND `/committee-service-pr-readiness` must clear every Critical finding before `/committee-service-preflight` runs.
>
> **Once the PR is open, do NOT run local review on iteration commits.** CodeRabbit + Copilot auto-trigger on every push and own the audit surface from that point. Local review is pre-PR insurance only, and it stops at PR-open.

**This repo owns its two review brains.** They live at `.claude/skills/committee-service-code-reviewer/SKILL.md` and `.claude/skills/committee-service-learnings-reviewer/SKILL.md`, and the launcher finds them through the `local-code-review` and `local-learnings-review` discovery aliases beside them. The learnings brain reads the repo's canonical empirical knowledge base at `docs/reviews/knowledge-base/` — the single KB for this repo, deliberately not duplicated under the skill tree. That directory is **also read by the GitHub PR review surface** (`.github/skills/committee-service-code-review/SKILL.md`, which treats its `known-false-positives.md` as a posting floor), so an edit there changes what the PR bot posts as well as what local review flags. That is intended: one path, one truth. Keep `Detect:` clauses narrow, and re-verify citations against current code when you touch an entry.

### Post-commit (pre-PR phase, after every commit)

1. **Commit your work.** `git commit -s -S`.
2. **Run `/lfx-skills:lfx-local-review`** — post-commit mode, which reviews `HEAD^..HEAD`. No arguments needed.
3. **Keep working if you want.** The launcher snapshots the target commit into a temporary detached worktree before any reviewer starts, so further edits to your tree cannot change what is under review. The run itself is in-session: it produces one `lfx-local-review/v1` summary and nothing is written to disk for later.
4. **Report the rendered summary in full and unedited.** It carries the aggregate state, the harness and model actually used, and the resolved path and digest of all three brains. On a fallback run it leads with a skipped-Pi notice — leave that at the top. A run where Pi was skipped is honest evidence, but it is not *cross-model* evidence.
5. **`INCOMPLETE` is not a pass.** Any incomplete role makes the whole run incomplete. Recovery is a complete rerun of the **same** harness — never a partial rerun, never a switch to the other harness, and never coaching a reviewer into a valid payload.
6. **Roll every `critical` and `high` finding, and every reasonable `should-fix`, into the next commit.**

### Pre-PR (sweep cumulative state, then open)

When the work is done and no more code commits are planned:

1. **If the last post-commit run had findings:** add a fix commit and run local review again on the new state, until it comes back `COMPLETE_NO_FINDINGS` or the remainder is documented as a trade-off.
2. **Full-branch sweep — only if the branch has more than one commit.** Run **`/lfx-skills:lfx-local-review` in branch mode** (`--mode branch`, base `origin/main`; pass `--base <ref>` for a different base). Note that the launcher never fetches — refresh `origin/main` yourself first, or the sweep silently describes a stale base. Address any new findings, then re-run the sweep until clean.
3. **Run `/committee-service-pr-readiness [base-branch]`** for branch, JIRA, conventional commits, rebase, DCO+GPG, diff size, and protected files.
4. **Run `/committee-service-preflight [base-branch]`** for working tree, license headers, formatting, lint, API/CLI builds, tests, protected files, commit verification, and PR change summary.
5. **Only then push and open the PR.**

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
