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
> - `/committee-service-pr-readiness` and `/committee-service-preflight`: PR-shape and mechanical pre-PR checks, run in that order as the Preflight value of the [Pre-PR review](#pre-pr-review) block below and also invocable by hand; they do not perform code review.
>
> If the plugin is missing, install with `/plugin marketplace add linuxfoundation/lfx-skills` then `/plugin install lfx-skills@lfx-skills`.

## Repo Role

This repo owns committee resources, committee members, committee settings, invite/application flows, the committee CLI, and the service's indexer and FGA event contracts. Classified as a **native V2 service** (owns its NATS KV state; publishes FGA tuples and indexer messages; consumes from project-service).

## Authoritative repo docs

- `README.md`: file structure, key features, release process, contributor flow.
- `docs/indexer-contract.md`: what this service emits on `lfx.index.*` subjects.
- `docs/fga-contract.md`: what this service emits on `lfx.fga-sync.*` subjects.
- `docs/invite-application-flows.md`: committee membership modes, invite/application state machines, edge cases.
- `docs/postgres-schema.md`: the PostgreSQL schema (used only when `STORAGE_MODE` selects a PostgreSQL mode; the default `kv` never touches it), its mapping to the NATS KV buckets and lookup keys, the migration rules, and how a storage mode is selected, rolled out and rolled back.
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

## Go version

This repo targets Go 1.27.x (currently 1.27.4, the latest patch). This
supersedes the Go version policy in the `lfx-megalinter` skill, which targets
the latest stable minor minus one: don't move `go.mod` back to Go 1.26. Take
every 1.27.x patch release, and update the `go` directive in `go.mod` and
`GO_VERSION` in the `Makefile` together. Release images are built with the
`go.mod` version (`ko-build-tag.yaml` runs `setup-go` with
`go-version-file: go.mod`), so a patch bump is how a Go security fix reaches
a release.

MegaLinter (v10.2.0) runs Go 1.26.10 in its image, so
`.github/workflows/mega-linter.yml` sets `GOTOOLCHAIN: auto` to let its Go
linters download the `go.mod` toolchain. Its bundled golangci-lint 2.15.0 is
built with Go 1.27.0 and lints this module. osv-scanner's govulncheck call
analysis is built with Go 1.26 and cannot load it, so osv-scanner falls back
to matching vulnerabilities by version: any known vulnerability in a
dependency version fails the check, reachable or not.

Moving to a new Go minor needs a MegaLinter release whose bundled
golangci-lint is built with that minor or newer; `GOTOOLCHAIN: auto` does not
help a precompiled linter read a newer Go than it was built with.

To find the latest patch release for the minor in `go.mod`:

```bash
MINOR=$(grep '^go ' go.mod | awk '{print $2}' | cut -d. -f1,2)
curl -s "https://go.dev/dl/?mode=json&include=all" \
  | jq -r --arg m "go${MINOR}." '.[].version | select(startswith($m))' \
  | sort -V | tail -1
```

## Pre-PR review

> **IMPORTANT — follow this exactly.** When the implementation is complete
> and committed and you are about to open a PR:
>
> 1. **Review once.** Load `/lfx-skills:lfx-pre-pr-review` with the Skill
>    tool and follow it: it tells you how to launch the reviewers. You run
>    **one** review round of the whole branch and land **all accepted
>    findings in exactly one fix commit** (none if there is nothing to fix).
>    Do not work from memory: **load the skill before launching the
>    reviewers**.
> 2. **Preflight.** Run the `Preflight` value below and make it pass. It is
>    deterministic checks, not a review: fix what it reports in its own
>    commit(s), as many as it takes, and rerun it — never the reviewers.
> 3. **Open the PR.** From then on there are **no local reviews of any
>    kind** — iterate only on the PR's bot and human feedback, still running
>    tests and checks. Where a `PR driver` is named below, load that skill and
>    follow it for that iteration instead of `/lfx-skills:lfx-pr-resolve`.

- KB review skill: `/committee-service-learnings-reviewer`
- Preflight: `/committee-service-pr-readiness origin/main`, then `/committee-service-preflight origin/main --report-only`

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

This access decision covers the synthesized `brief_text` narrative, not raw restricted source artifacts. The generator persists only `kind` and `id` for restricted source references: private meetings, non-public AI meeting summaries, private mailing-list threads, all surveys, and all project memberships. Their raw titles and excerpts (including survey statistics and purchase details) are never copied into viewer-readable `source_refs`. Public source references may retain titles and excerpts; member-roster references continue to honor `member_visibility`.
