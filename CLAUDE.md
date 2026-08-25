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

## Go Toolchain Version

Always bump `go.mod`'s `go` directive to the latest available Go release,
including minor version bumps, using:

```bash
go get go@latest
```

Its output reports the change, e.g. `go: upgraded go 1.X.Y => 1.{X+1}.Z`. If
the minor component increased by any amount, read that release's notes
at https://go.dev/doc/devel/release for breaking changes relevant to this
repo before committing.

Run `go mod tidy` afterward if `go get` touched `go.sum`.

## Work cycle — post-commit and pre-PR reviews

> **CRITICAL — while the branch is pre-PR, post-commit review is mandatory.** After every commit on the local branch, run **`/lfx-skills:lfx-local-review`**. It runs three reviewers in parallel — the central `general` brain plus this repo's own `repo_code` and `repo_learnings` brains — on headless Pi when Pi is available, and on Claude subagents otherwise, and returns their ordinary Markdown reports. It reviews **`HEAD^..HEAD`** by default — the newest commit against its first parent, and nothing else; a caller may supply a direct base range instead. Before opening a PR, drain every report AND let `/committee-service-pr-readiness` clear every Critical finding before `/committee-service-preflight` runs.
>
> **Once the PR is open, do NOT run local review on iteration commits.** CodeRabbit + Copilot auto-trigger on every push and own the audit surface from that point. Local review is pre-PR insurance only, and it stops at PR-open.

**This repo owns two of the three review brains.** They live at `.claude/skills/committee-service-code-reviewer/SKILL.md` and `.claude/skills/committee-service-learnings-reviewer/SKILL.md`, and the host finds them through the `local-code-review` and `local-learnings-review` discovery aliases beside them. The `general` brain is central and carries no repo-specific rules.

**This repo also owns the Claude-fallback launch table**, at `.claude/skills/local-review-fallback/SKILL.md` (aliased as `.agents/skills/local-review-fallback`). When the host reports Pi unavailable, that skill launches the three reviewers as Claude subagents in one parallel batch, passing the host's pins through unchanged. It is a launch table only — every review criterion, severity and floor rule stays in the selected reviewer skills, so do not add review guidance there.

The learnings brain reads the repo's canonical empirical knowledge base at `docs/reviews/knowledge-base/` — the single KB for this repo, deliberately not duplicated under the skill tree. That directory is **also read by the GitHub PR review surface** (`.github/skills/committee-service-code-review/SKILL.md`, which treats its `known-false-positives.md` as a posting floor), so an edit there changes what the PR bot posts as well as what local review flags. That is intended: one path, one truth. Keep `Detect:` clauses narrow, and re-verify citations against current code when you touch an entry.

A change can never waive a finding about itself. The false-positive floor must suppress at both `base_sha` and `target_sha`, and a commit that adds waiver coverage does not carry it at its base. A waiver can apply to a later range whose supplied base already carries it.

### Post-commit (pre-PR phase, after every commit)

1. **Commit your work.** `git commit -s -S`.
2. **Run `/lfx-skills:lfx-local-review`** from inside this repo — exactly that, with no arguments. It reviews `HEAD^..HEAD`. Pass a direct base range only when you deliberately need a different one.
3. **Relay all three reports in full and unedited.** They are ordinary Markdown, one per role. If the run used the Claude Opus fallback, say so when you report it. It is not the intended Pi/GitHub-Copilot cross-model review.
4. **An incomplete cycle is not a pass, and one reviewer can spoil it.** If any reviewer's report starts `INCOMPLETE — <reason>`, or the host reports a failed or empty child, **the whole cycle is incomplete** — successful siblings do not rescue it. Resolve the cause and rerun the **complete trio under one harness**. Never rerun a single role, never mix Pi and Claude evidence in one cycle, and never render a failed child as "no findings".
5. **Address every real Critical and reasonable Important finding in this session**, then commit the fixes as their own signed conventional commits — `fix(<scope>): ...` or `fix: ...` — rather than amending. Reviewers report; you fix.
6. **Rerun the complete trio after each fix commit.**

### Pre-PR (drain, then open)

When the work is done and no more code commits are planned:

1. **Drain the post-commit reports.** If the last run had findings, fix them, commit, and rerun the complete trio on the new commit until the reports are clean or the remainder is explicitly documented as a trade-off. Local review looks at one commit at a time; there is no cumulative pass, so the way the whole branch gets covered is that every commit on it was reviewed when it landed.
2. **Run `/committee-service-pr-readiness [base-branch]`** for branch, JIRA, conventional commits, rebase, DCO+GPG, diff size, and protected files.

   **If that rebase changes previously reviewed content — you resolved a conflict, or the rebase otherwise altered what a commit contains — the resulting content is not covered by the earlier per-commit reviews.** Those reviews read the pre-rebase commits; the resolutions did not exist yet. Cover them with **one** local review run whose target is the current `HEAD` and whose `base_sha` you supply explicitly, chosen so the range spans the rebased result. It still reviews exactly `git diff <base_sha> <target_sha>` and nothing else.

   This is the ordinary caller-supplied range the command already accepts, used after a content-changing rebase. It is **not** a new mode, not an automatic `main`/`origin` lookup, not a fetch, not a merge-base derivation, and not a cumulative branch gate — you pick the base and pass it. A rebase that only replays commits unchanged needs no extra review.
3. **Run `/committee-service-preflight [base-branch]`** for working tree, license headers, formatting, lint, API/CLI builds, tests, protected files, commit verification, and PR change summary.
4. **Only then push and open the PR.** Reviewers may run useful builds, tests and linters, but only this session edits, commits, or cleans up anything they leave behind.

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

## Design Decisions

**Weekly brief access model (LFXV2-3046):** `group_weekly_brief` uses `access_check_relation: viewer`, matching the committee's own visibility — public committees' briefs are visible to anyone who can view the group; private committees' briefs are visible only to members and elevated users. This mirrors `GET /current`. `private_source_present` is a UI disclosure flag only.
