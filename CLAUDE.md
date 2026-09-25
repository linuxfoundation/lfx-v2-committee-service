# CLAUDE.md

This file provides guidance to Claude Code when working with the LFX v2 Committee Service.

> **Central LFX skills (always available, do not duplicate here):**
>
> - `/lfx-skills:lfx`: cross-repo topology, ownership routing, "where does X live", repo discovery, missing-checkout handling.
> - `/lfx-skills:lfx-platform-architecture`: V2 platform composition, service classes (native, wrapper, proxy, platform), write/read/access-check/index flows, NATS and KV ownership, and handoff points across Self Serve, Goa services, OpenFGA, fga-sync, indexer-service, query-service, access-check, Heimdall, Helm, and ArgoCD.
>
> **Repo-local skills (owned here, not in central `lfx-skills`):**
>
> - `/committee-service-dev` auto-attaches on Go, docs, and service-chart paths (`cmd/`, `internal/`, `pkg/`, `gen/`, `docs/`, `charts/lfx-v2-committee-service/`, `Makefile`, `go.mod`, `go.sum`, Goa design files) and owns generated-code boundary, logging via `pkg/log`, the `pkg/errors` family and its Goa mapping, request-context propagation via `pkg/constants`, NATS subject / KV / Object Store coding rules, committee-owned indexer and FGA contract docs, table-driven tests with `internal/infrastructure/mock` fakes, gofmt/golangci-lint hygiene, and license headers. See `.claude/skills/committee-service-dev/SKILL.md`.
> - `/committee-service-learnings-reviewer`: the review knowledge-base reviewer launched by the [Pre-PR review](#pre-pr-review) block below; not invoked by hand.
> - `/committee-service-pr-readiness` and `/committee-service-preflight`: PR-shape and mechanical pre-PR checks a developer may invoke by hand; they do not perform code review.
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
- `docs/reviews/knowledge-base/`: the empirical review knowledge base; the GitHub PR review surface also reads it, so read its `README.md` before editing an entry.

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

Freely bump `go.mod`'s `go` directive to the latest available *patch*
release (e.g. `1.X.Y` → `1.X.{Y+1}`) to pick up security fixes. Do **not**
bump the *minor* version (e.g. `1.X.x` → `1.{X+1}.x`) unless the user
explicitly asks for it, **and** you've validated it against the Go version
MegaLinter itself bundles -- MegaLinter's `golangci-lint` and `govulncheck`
(pulled in via its `osv-scanner` check) are guaranteed to lag behind the
latest Go release by some amount (their binaries are built against
whatever Go version was current when that MegaLinter flavor tag was cut),
and a `go.mod` directive newer than what they were built with breaks them
outright. This is a hard ceiling with no environment-variable workaround --
`GOTOOLCHAIN: auto` only affects invocations of the `go` command itself
and does nothing for these precompiled binaries' own internal version
checks (confirmed empirically: setting it in both the workflow and
`.mega-linter.yml` still failed).

To find MegaLinter's bundled Go version:

```bash
# 1. Find the MegaLinter flavor and pinned version tag used in CI.
grep -A1 'oxsecurity/megalinter' .github/workflows/*.yml
# e.g. "uses: oxsecurity/megalinter/flavors/<flavor>@<sha>  # <tag>"

# 2. Fetch that flavor's Dockerfile and read its GO_ALPINE_VERSION build
#    arg -- this is what the final image installs as `go`, not
#    GO_IMAGE_VERSION (which only applies to an intermediate builder
#    stage).
curl -s "https://raw.githubusercontent.com/oxsecurity/megalinter/<tag>/flavors/<flavor>/Dockerfile" \
  | grep -i 'GO_ALPINE_VERSION'
```

`go.mod`'s `go` directive must never exceed that bundled version. Staying
one minor version behind it (rather than matching its minor *and* patch
exactly) leaves room to always take the latest patch release for security
fixes without ever being blocked by MegaLinter's own bundled patch version
lagging a newly disclosed vulnerability.

There's no built-in `go` subcommand to look up the latest patch release for
a given minor version -- query the official `go.dev/dl` JSON feed instead:

```bash
# Find the latest patch release for the minor version pinned in go.mod.
MINOR=$(grep '^go ' go.mod | awk '{print $2}' | cut -d. -f1,2)
curl -s "https://go.dev/dl/?mode=json&include=all" \
  | jq -r --arg m "go${MINOR}." '.[].version | select(startswith($m))' \
  | sort -V | tail -1
```

## Pre-PR review

Run **one** local review of the whole branch before opening the PR — never
after individual commits, and never again once the PR exists.

1. When the implementation is complete and committed, run `git fetch origin`
   and pin the range: `base_sha=$(git merge-base origin/main HEAD)`,
   `target_sha=$(git rev-parse HEAD)`.
2. Launch **two** independent background subagents **in parallel**, one per
   skill, each with `subagent_type: general-purpose`, `model: opus` (Opus 5.5),
   `run_in_background: true`. Tell each to load exactly one skill with the
   Skill tool and follow it: one loads `/lfx-skills:lfx-general-code-review`
   (general quality plus this repo's written conventions, style and rules);
   the other loads `/committee-service-learnings-reviewer` (this repo's review knowledge base). Give each
   the full 40-character `base_sha` and `target_sha`, the instruction to review
   exactly `git diff <base_sha> <target_sha>`, and the report-only rule: they
   never edit, commit, push or write GitHub state.
3. Wait for both reports. A failed, empty or `INCOMPLETE` report is **not** a
   clean review: fix the cause and relaunch that reviewer once; if it fails
   again, stop and tell the developer.
4. Verify every finding against the code. Address every Critical and every
   reasonable Important finding in **EXACTLY ONE fix commit** (signed and
   DCO-signed-off). No fix commit if there is nothing to fix. Never one commit
   per finding.
5. Run `make check && make build && make build-cli && make test`. If it fails, fold the remedy into the fix commit with
   `git commit --amend` (re-sign and re-sign-off); if review found nothing and
   there is no fix commit yet, this remedy becomes the one fix commit. Rerun
   the checks — but **do not rerun the reviewers**. The branch gains **at most one**
   commit after the implementation — the single fix commit, or none at all —
   never more.
6. Open the PR.

**Hard rules.** No local review runs after any individual commit. The
reviewers are **never** rerun on the fix commit. From the moment the PR is
open, **no local reviews of any kind**: iterate only on the PR's bot and human
review feedback, still running tests and checks, and batch each round of fixes
into as few commits as possible.

## Boundaries

- Committee storage and committee-specific NATS handlers stay in this repo.
- `lfx-v2-fga-sync` owns OpenFGA tuple write mechanics and the generic FGA message envelope.
- `lfx-v2-indexer-service` owns indexing infrastructure behavior and the `IndexerMessageEnvelope` contract.
- `lfx-v2-helm` owns cross-service chart conventions; this repo only owns its own chart under `charts/lfx-v2-committee-service/`.
- `lfx-v2-argocd` owns deployed values, image tags, and environment promotion.

## Test Data Conventions

Always use **obviously fake** placeholder identities in tests, fixture structs, and example values — never names that could belong to a real person.

| Field | Use this |
| --------- | ------------------------------------------ |
| name | `First Last` |
| username | `first-last` |
| email | `first.last@example.com` |
| avatar | `https://example.com/avatar.png` |

Do **not** use names like "Alice Admin", "Bob Smith", "Alex Lee", or any combination that reads as a realistic person. `First Last` / `first-last` / `first.last@example.com` is the canonical placeholder for new and modified test data in this repo.

## Design Decisions

**Weekly brief access model (LFXV2-3046):** `group_weekly_brief` uses `access_check_relation: viewer`, matching the committee's own visibility — public committees' briefs are visible to anyone who can view the group; private committees' briefs are visible only to members and elevated users. This mirrors `GET /current`. `private_source_present` is a UI disclosure flag only.
