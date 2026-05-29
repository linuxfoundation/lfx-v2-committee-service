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
make fmt       # gofmt + goimports
```

Run `make apigen` after editing any file under `cmd/committee-api/design/`. Never hand-edit `gen/`.

## Work cycle — post-commit and pre-PR reviews

> **CRITICAL — while the branch is pre-PR, post-commit review is mandatory.** After every commit on the local branch, launch both `lfx-skills:lfx-general-code-reviewer` and `lfx-skills:lfx-committee-service-code-reviewer` subagents via the Agent tool (`run_in_background: true`) — then keep working while they run. If Claude displays plugin agents without the `lfx-skills:` namespace, use the equivalent displayed reviewer names. Before opening a PR, every running review must return clean (or remaining findings explicitly documented as trade-offs), the **full-branch sweep** must run clean if the branch has more than one commit (`branch` arg), AND `/committee-service-pr-readiness` must clear every Critical finding before `/committee-service-preflight` runs.
>
> **Once the PR is open, do NOT invoke these pre-PR reviewers on iteration commits.** CodeRabbit + Copilot auto-trigger on every push and own the audit surface from that point. The general and committee-service reviewers are pre-PR insurance only.

### Post-commit (pre-PR phase, after every commit, asynchronous)

1. **Commit your work.** `git commit -s -S`. Do not wait for any prior review to finish.
2. **Immediately launch both reviewer subagents in parallel.**
   - General reviewer: `subagent_type: lfx-skills:lfx-general-code-reviewer`, `run_in_background: true`.
   - Committee-service reviewer: `subagent_type: lfx-skills:lfx-committee-service-code-reviewer`, `run_in_background: true`.
3. **Post-commit mode prompt for both reviewers (exact):** `target repo: lfx-v2-committee-service\n\nReview the latest commit.` Append `extra: <focus>` on a new line only when there is a priority hint to add. Do NOT pass `branch` here. If this work cycle is launched from the LFX workspace parent, the `target repo:` line is required so each reviewer operates in this repo.
4. **Keep working.** Start the next commit while the reviewers run. Do not block on them.
5. **When reviews return:** roll every Critical finding and every reasonable Important finding from either reviewer into the next commit.

### Pre-PR (drain the queue, sweep cumulative state, then open)

When the work is done and no more code commits are planned:

1. **Wait for every running review to complete.**
2. **If any returned review flags Critical or reasonable Important:** add a fix commit, launch both reviewers again on the new state, wait, and loop until clean or explicitly documented as a trade-off.
3. **Full-branch sweep — only if the branch has more than one commit.** Launch both `lfx-skills:lfx-general-code-reviewer` and `lfx-skills:lfx-committee-service-code-reviewer` again with prompt **`target repo: lfx-v2-committee-service\nbranch\n\nReview the branch's diff against origin/main.`**. Address any new findings, then re-run the sweep until clean.
4. **Run `/committee-service-pr-readiness [base-branch]`** for branch, JIRA, conventional commits, rebase, DCO+GPG, diff size, and protected files.
5. **Run `/committee-service-preflight [base-branch]`** for working tree, license headers, formatting, lint, API/CLI builds, tests, protected files, commit verification, and PR change summary.
6. **Only then push and open the PR.**

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
