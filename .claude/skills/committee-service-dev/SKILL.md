---
# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT
name: committee-service-dev
description: Repo-local development conventions for the lfx-v2-committee-service. Auto-attaches on Go implementation paths (cmd/, internal/, pkg/, gen/), Goa design files under cmd/committee-api/design/, repo docs, the service-local Helm chart, Makefile, go.mod, and go.sum. Owns the generated-code boundary, slog logging via pkg/log, the pkg/errors domain-error family and its Goa mapping, request-context propagation through pkg/constants keys, NATS subject / KV / Object Store coding rules, committee-owned indexer and FGA contract docs, service chart wiring, table-driven tests with internal/infrastructure/mock fakes, gofmt and golangci-lint hygiene, and license headers. Central platform composition, V2 service classes, and cross-repo handoffs stay in lfx-skills:lfx-platform-architecture. Cross-repo topology and routing stay in lfx-skills:lfx. Committee-domain contracts (indexer-contract.md, fga-contract.md, invite-application-flows.md) are authoritative for what this service emits.
paths:
  - "**/*.go"
  - "go.mod"
  - "go.sum"
  - "Makefile"
  - "README.md"
  - "cmd/**"
  - "docs/**"
  - "charts/lfx-v2-committee-service/**"
  - "internal/**"
  - "pkg/**"
  - "gen/**"
  - ".claude/skills/committee-service-dev/**"
allowed-tools: Read, Glob, Grep, Edit, Write, Bash
---

# Development Conventions (lfx-v2-committee-service)

Repo-owned development conventions for this committee service. Central
architecture lives elsewhere (see "Central skills" below); this skill owns how
Go code, repo-owned contracts, and the service-local chart are maintained in
this repo.

## Central skills (do not duplicate)

- `lfx-skills:lfx` owns cross-repo topology, ownership routing, repo discovery,
  glossary, and missing-checkout handling.
- `lfx-skills:lfx-platform-architecture` owns V2 platform composition, service
  classes (native, wrapper, proxy, platform), write/read/access-check/index
  flows, NATS and KV ownership, and handoff points across Self Serve, Goa
  services, OpenFGA, fga-sync, indexer-service, query-service, access-check,
  Heimdall, Helm, and ArgoCD. It also owns the V2 service taxonomy.

Use them through `/lfx-skills:lfx` and `/lfx-skills:lfx-platform-architecture`
when the question is about cross-repo or platform composition. This skill
governs only repo-local implementation, contracts, and chart wiring.

## Repo layout (what this skill governs)

- `cmd/committee-api/` is the Goa service entry point: design files,
  presentation-layer service implementation, `main.go`, `http.go`.
- `cmd/committee-cli/` is the operational CLI (one binary, `command` +
  `subcommand` shape, see `cmd/committee-cli/README.md`).
- `gen/` is Goa-generated code. Do not edit.
- `internal/domain/` holds `model/` (entities) and `port/` (interfaces).
- `internal/service/` holds use-case orchestration (`*Writer`, `*Reader`,
  `MessageHandler`).
- `internal/infrastructure/` holds NATS storage (`nats/`), auth (`auth/`), and
  mocks (`mock/`).
- `internal/middleware/` holds HTTP middleware (request ID, authorization).
- `pkg/` holds reusable utilities: `constants`, `errors`, `log`, `redaction`,
  `fields`, `env`, `concurrent`, `utils`.
- `docs/` holds committee-owned contracts for emitted indexer/FGA messages and
  invite/application flow behavior. Keep these synced with code changes.
- `charts/lfx-v2-committee-service/` holds this service's Helm chart:
  Deployment env vars, Gateway HTTPRoute, Heimdall RuleSet, NATS KV buckets,
  Object Store, and JetStream stream resources.

Match the existing package boundary before adding a new one. Use-case logic
goes in `internal/service/`; storage adapters in `internal/infrastructure/nats/`;
interfaces in `internal/domain/port/`.

## Generated code boundary

- Never hand-edit files under `gen/` (Goa output).
- Change Goa design files under `cmd/committee-api/design/` first, then run
  `make apigen`. Commit generated output with the design change.
- `cmd/committee-api/service/*` (presentation layer) implements the Goa
  service interface and adapts to domain types; keep business logic in
  `internal/service/`, not in this layer.
- See `references/goa-patterns.md` for committee-service Goa specifics
  (base/settings split, per-sub-resource methods, ETag/If-Match handling).

## Logging

Use Go's `log/slog` together with this repo's `pkg/log` helpers. Do not use
`fmt.Println`, `fmt.Printf`, or `log.Print*` for runtime logging.

- Prefer the `*Context` variants (`slog.DebugContext`, `slog.InfoContext`,
  `slog.WarnContext`, `slog.ErrorContext`) so the `pkg/log` context handler can
  inject ambient attributes (`request_id`, etc.).
- Add ambient request fields via `log.AppendCtx(ctx, slog.String(...))` in
  middleware. Service code reads them implicitly through the context handler.
- Stable structured fields when available: `request_id`, `principal`,
  `object_type`, `object_id`, `operation`, `committee_uid`, `member_uid`,
  `project_uid`.
- Honor `LOG_LEVEL` and `LOG_ADD_SOURCE` (see `cmd/committee-api/README.md`).
- Never log raw JWTs, bearer headers, secrets, or full payloads that may
  contain PII. Use `redaction.Redact` from `pkg/redaction` for partial
  identifiers (emails, usernames) when they must appear in logs.

## Errors

This repo has a typed domain-error family in `pkg/errors`. Use it. Do not
introduce a parallel sentinel-error family.

- Constructors: `errors.NewValidation`, `errors.NewNotFound`,
  `errors.NewConflict`, `errors.NewForbidden`, `errors.NewServiceUnavailable`,
  `errors.NewUnexpected` (and matching types: `Validation`, `NotFound`,
  `Conflict`, `Forbidden`, `ServiceUnavailable`).
- Use `errors.Join` / wrap upstream errors so `errors.Is` and `errors.As`
  still work. The base type already joins via `errors.Join(err...)`.
- Translate domain errors at the Goa transport boundary in
  `cmd/committee-api/service/error.go` (the `wrapError` switch). Do not return
  raw upstream HTTP errors or NATS errors to clients.
- HTTP status mapping enforced by `wrapError`:
  - `errors.Validation` -> 400 `BadRequestError`
  - `errors.NotFound` -> 404 `NotFoundError`
  - `errors.Conflict` -> 409 `ConflictError`
  - `errors.Forbidden` -> 403 `ForbiddenError`
  - `errors.ServiceUnavailable` -> 503 `ServiceUnavailableError`
  - default -> 500 `InternalServerError`
- When you add a new domain error case, extend `wrapError` in the same change.

## Request context

- HTTP middleware owns request-context setup. Service-layer code must not read
  HTTP headers directly.
- Use the typed context keys from `pkg/constants`: `PrincipalContextID`,
  `EmailContextID`, `AuthorizationContextID`, `OnBehalfContextID`,
  `RequestIDHeader`. Do not introduce bare string context keys.
- `internal/middleware/request_id.go` calls
  `log.AppendCtx(ctx, slog.String(string(constants.RequestIDHeader), requestID))`;
  follow the same pattern when adding a new ambient field.
- Propagate `context.Context` down through all use-case and storage calls.
  Pass `ctx` first in any new function that does I/O, logging, or cancellation.

## NATS, subjects, KV, and Object Store

- All NATS subject strings and KV bucket names live in `pkg/constants/`
  (`subjects.go`, `storage.go`). Never hardcode a subject or bucket string at a
  call site.
- The committee service also owns the `committee-documents` NATS Object Store
  for uploaded file bytes. Metadata stays in the `committee-documents-metadata`
  KV bucket.
- Use queue groups for shared subscriptions. This service uses
  `lfx.committee-api.queue` for request/reply and fire-and-forget event
  handlers, plus durable JetStream consumers: one for total-member recounts
  (`committee-member-events` stream) and one for async weekly-brief generation
  (`weekly-brief-events` stream).
- Never write directly to another service's KV bucket. Cross-service writes go
  through that service's NATS RPC or its message contracts.
- Drain the NATS connection through the existing graceful-shutdown path in
  `internal/infrastructure/nats/`. Do not add a parallel shutdown.
- When subjects, queue groups, payloads, KV buckets, Object Stores, or streams
  change, update `references/nats-messaging.md` in the same change.
- Local subject and bucket inventory: see `references/nats-messaging.md`.

## Contracts and chart wiring

- `docs/indexer-contract.md` must match the `IndexingConfig`, tags, and data
  structs emitted by the writer/orchestrator code. Do not copy generic indexer
  examples forward without checking this repo's `internal/service/*writer.go`
  and `internal/domain/model/*`.
- `docs/fga-contract.md` must match access messages built by
  `internal/service/committee_writer.go` and
  `internal/service/committee_member_writer.go`.
- `docs/invite-application-flows.md` must match the status transitions in
  `cmd/committee-api/service/committee_service.go`.
- `cmd/committee-api/README.md` should reflect the actual Goa endpoints in
  `cmd/committee-api/design/committee.go`.
- Chart changes stay under `charts/lfx-v2-committee-service/`. Route shared
  chart conventions to `lfx-v2-helm/docs/service-chart-patterns.md`; do not
  duplicate shared Helm policy here.

## Tests

- Depend on interfaces from `internal/domain/port/` for repositories, message
  publishers, FGA clients, NATS clients, and upstream proxies.
- Place mocks in `internal/infrastructure/mock/`. Reuse the existing fakes
  before adding new ones.
- Table-driven tests for branching behavior. One `Test<Type>_<Method>` per
  exported method; add cases to its table rather than fanning out new
  functions.
- Co-locate `*_test.go` with the code under test. Use `package <pkg>_test` for
  black-box tests (see `internal/domain/model/committee_link_test.go`); use
  same-package tests when exercising unexported behavior.
- For typed-error assertions use the typed-error pattern in
  `internal/service/committee_writer_test.go`:

  ```go
  var conflictErr errs.Conflict
  if !errors.As(err, &conflictErr) { t.Fatalf(...) }
  ```

- Run `make test` before handing off. It already runs `go test` with `-race`;
  use targeted package tests only while iterating.

## Formatting, linting, headers

- Run `make fmt` (wraps `go fmt ./...` and `gofmt -s -w` on tracked Go files).
- Run `make lint` (golangci-lint pinned in the Makefile). Fix lint warnings;
  do not blanket-silence them.
- License header on every new `.go` file (Goa-generated files are excluded by
  the Makefile's `license-check`):

  ```go
  // Copyright The Linux Foundation and each contributor to LFX.
  // SPDX-License-Identifier: MIT
  ```

  Markdown files in `.claude/skills/.../references/` use the HTML-comment form
  (`<!-- Copyright ... -->`). Match the existing files in the directory.
- Document exported Go symbols when revive/golangci-lint requires it (see
  `revive.toml`). Add implementation comments only where the code is not
  self-explanatory.

## Companion files

- `docs/indexer-contract.md`, `docs/fga-contract.md`, and
  `docs/invite-application-flows.md` are the authoritative committee-domain
  contracts. Update them in the same PR as any behavior change.
- For generic FGA semantics, read
  `lfx-v2-fga-sync/docs/fga-sync-contract.md`. For generic indexer semantics,
  read `lfx-v2-indexer-service/docs/indexer-contract.md`. For shared chart
  conventions, read `lfx-v2-helm/docs/service-chart-patterns.md`.

## References

- `references/goa-patterns.md`: committee-service Goa design layout, base /
  settings split, ETag handling, per-sub-resource method structure.
- `references/nats-messaging.md`: subjects, queue groups, KV buckets, Object
  Store, and streams owned or consumed by this service.
