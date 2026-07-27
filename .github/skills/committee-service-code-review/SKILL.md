---
name: committee-service-code-review
description: >
  How to judge the implementation of an lfx-v2-committee-service pull request:
  the general quality dimensions (correctness, error handling, tests,
  concurrency, readability, code truthfulness), how to hold the diff to the
  repo's documented standards for this Goa + NATS Go service, and the security
  anchors that make a diff security-relevant here. Use on every PR that changes
  code, however small; this is the reviewer's line-level lens.
---

<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Committee Service Code Review

Reviewer scope and the signal bar are owned by the `copilot-code-reviewer` skill
(`.github/skills/copilot-code-reviewer/SKILL.md`); this skill assumes those and
owns the line-level method. Read enough surrounding code to judge each hunk in
its real context — for a handler change, the design → presentation → service →
storage path it sits on; for a storage change, the writer and reader that call
it and the message publishes that follow the write.

A diff alone is not enough. For each non-trivial hunk, read the **whole changed
function**, not just the diff lines, and grep for **callers and sibling
implementations** of the same pattern to confirm the change matches how the repo
already does it. This service has many near-identical resources — committees,
members, invites, applications, links, folders, documents, weekly briefs — whose
implementations mirror each other, so the nearest sibling is usually one grep
away and is the fastest way to tell a deliberate deviation from an omission.
Convention drift is a finding even when the code "works".

## The house standards

The repo defines its own standards; hold the diff to them, and name the
documented source in any standards finding. Read the parts relevant to the diff
before judging, every run, because the standards belong to the repo and move
with it. They live in:

- **`CLAUDE.md`** — the repo's role and its boundaries with `lfx-v2-fga-sync`,
  `lfx-v2-indexer-service`, `lfx-v2-helm`, and `lfx-v2-argocd`; the list of
  authoritative repo docs; the common `make` targets.
- **`.claude/skills/committee-service-dev/SKILL.md`** and its `references/` —
  the repo-local development conventions: the generated-code boundary, logging
  via `pkg/log` and `log/slog`, the `pkg/errors` domain-error family and its Goa
  mapping, request-context propagation through `pkg/constants` keys, NATS
  subject / KV / Object Store rules, the test conventions, and the Goa design
  layout (`references/goa-patterns.md`) and messaging inventory
  (`references/nats-messaging.md`).
- **The contract docs under `docs/`** — `indexer-contract.md` and
  `fga-contract.md` for what this service emits, and
  `invite-application-flows.md` for the membership modes and the
  invite/application state machines. These are consumed outside this repo, and
  the repo's own rule names them as the contracts a behavior change updates in
  the same PR; a diff that changes an emitted message or a state transition
  without touching the matching doc is a finding. `nats-request-reply.md`
  describes the synchronous subjects other services call — reference material
  when judging a request/reply change, though no same-PR rule is attached to it.
- **`docs/reviews/knowledge-base/`** — the empirical patterns this repo's PRs
  have actually been bitten by, organized by area, with
  `known-false-positives.md` recording what the team has already rejected. Use
  the category files as a checklist of known shapes and the false-positive file
  as a floor: a finding that matches something the team has explicitly rejected
  does not get posted.

Enforcement runs in both directions: code that violates a documented standard is
a finding, and a documented standard the code has visibly outgrown is a finding
against the docs. If a documented convention is wrong for this specific change,
say so explicitly and explain the trade, rather than silently waiving or
silently enforcing it.

## Quality dimensions

Run these on the changed code, scaled to the size of the change:

- **Correctness**: does it do what it claims? Watch a `context.Context` dropped
  or replaced with `context.Background()` on a request path, an error swallowed
  and turned into a `nil` return, boundary conditions on paging and filtered key
  scans, and multi-step writes where a later failure leaves the earlier step
  committed.
- **Error handling**: use the typed domain errors in `pkg/errors` rather than a
  parallel sentinel family or a bare `fmt.Errorf`, wrap so `errors.Is` and
  `errors.As` keep working, and translate at the Goa boundary in
  `cmd/committee-api/service/` — a new domain error case the boundary mapper
  does not handle silently becomes a 500. Matching on error text instead of the
  typed error is a finding. Raw upstream NATS or HTTP errors must not reach the
  client.
- **Concurrency**: this service fans out with worker pools, errgroups, and
  durable JetStream consumers. Look for a pool sized from untrusted or unbounded
  input, a goroutine with no lifetime tied to the request context, a shared map
  or slice written from several goroutines, and consumer handlers that are not
  safe to run twice — JetStream delivery is at-least-once, so a handler that
  double-counts or double-creates on redelivery is a real defect.
- **Tests**: new or changed behavior has tests that assert real behavior, not
  that a mock was called. The repo's shape is table-driven tests co-located with
  the code, depending on the interfaces in `internal/domain/port/` and reusing
  the fakes in `internal/infrastructure/mock/` rather than adding parallel ones.
  Missing tests on contract-bearing, state-machine, or security-sensitive code is
  always worth flagging.
- **Readability and structure**: the change reads like the surrounding code;
  it respects the layering (presentation adapts, `internal/service/` decides,
  `internal/infrastructure/` talks to NATS); names say what a thing is or does;
  duplicated logic that wants a shared helper is a finding when it traps the next
  editor.
- **Code truthfulness**: comments, doc-comments, and contract docs match what the
  code actually does. A stale comment on a constant, a contract doc describing a
  field the code no longer emits, or a TODO dressed as done is a finding. The PR
  description is not in scope here — "the description says X but the code does Y"
  is a class of comment the team has already rejected
  (`docs/reviews/knowledge-base/known-false-positives.md`).

## Committee-service specifics worth a second look

- **The generated-code boundary.** `gen/` is Goa output. A hand-edit there is a
  finding; the change belongs in `cmd/committee-api/design/` followed by
  `make apigen`, with the regenerated output committed alongside the design
  change. A design change with no regenerated output is a mismatch worth
  raising. The inverse needs a moment's thought first: regenerated output with
  no design change is expected when the pinned generator or runtime moves, so
  check whether the PR bumps them before treating it as a hand edit. What is a
  finding is a change under `gen/` that neither a design edit nor a tool bump
  explains.
- **Emitted contracts.** Indexer and FGA messages are how the rest of the
  platform learns about committee state. A new indexed resource that ships
  without the indexing configuration its siblings have is silently invisible to
  search; an FGA message that omits or mis-names a relation silently changes who
  can reach the resource. Compare a new emission against the nearest existing one
  and against the contract doc, in that order.
- **Subjects, buckets, and stores are named once.** Subject strings, KV bucket
  names, Object Store names, stream and consumer names live in `pkg/constants/`
  (the FGA envelope constants come from the `lfx-v2-fga-sync` module rather than
  being redefined here). A literal `"lfx.…"` subject or a bucket-name string at a
  call site is a finding — that is how a rename becomes a silent production
  break.
- **Optimistic locking.** KV-backed state is updated with revisions, and mutable
  resources expose that to clients as ETag / If-Match. A read-modify-write that
  drops the revision, a delete of the primary record that does not pass one, or a
  revision-conflict error that does not surface as a conflict to the caller are
  all last-writer-wins bugs that only show under concurrency. Best-effort cleanup
  of lookup keys, companion records, and stored blobs is a sanctioned pattern
  here and is not the same thing.
- **Multi-write ordering and rollback.** Several flows write more than one thing:
  a record plus its lookup keys and secondary indexes, an Object Store blob plus
  its metadata entry, a membership record plus the invite or application status
  that produced it. Ask what a failure between the two leaves behind — an orphan
  blob, a dangling lookup key, a member with no terminal invite status — and
  whether the code cleans up or is safe to retry. `invite-application-flows.md`
  documents the ordering for the membership flows — the member is created before
  the invite or application moves to its terminal status, so a failure leaves the
  record unchanged and the caller able to retry — and a diff that inverts it
  strands records.
- **Secondary indexes and key derivation.** Lookup keys are derived from values
  (a committee UID, an organization SFID, a hashed normalized email) with
  separator assumptions baked into the filter wildcards. When a diff changes how
  a key is built or normalized, check that existing keys still resolve and that
  the new input cannot contain the separator.
- **Chart and code move together.** The Helm chart under
  `charts/lfx-v2-committee-service/` declares this service's Heimdall RuleSet,
  its KV buckets, Object Stores, streams, and its deployment environment. A new
  endpoint whose route has no matching rule is unreachable or unauthorized in a
  deployed cluster; a new bucket, stream, or environment variable that the chart
  does not create is a runtime failure. This coupling is not visible in the Go
  diff, which is exactly why it gets missed.
- **Critical constants.** A changed constant is a behavior change even when the
  code compiles: timeouts, retry and backoff values, concurrency limits, page
  sizes, throttle counts, subject and bucket names, JWT audience and issuer
  defaults. When the diff moves one, ask whether the change is stated and
  intentional and what its blast radius is; an unexplained constant change is a
  finding.
- **The CLI and the migration scripts.** `cmd/committee-cli/` and `scripts/`
  publish the same messages and write the same buckets as the API, usually with
  a wider blast radius and no HTTP-level guard. Hold them to the same envelope,
  constant, and locking rules as the service, and check that a repair or backfill
  is idempotent and re-runnable.

## Security anchors

These are the boundaries that make a diff security-relevant in this service.
They describe its shape, not its current line-level guards; verify the concrete
mechanism in the code each time, and only report what you can trace. If you
cannot trace a path from attacker-controlled input to a sensitive sink, it is
not a reportable security finding.

- **Secrets in the diff.** A hardcoded credential, token, private key, or
  connection string is a finding anywhere it appears, including tests, fixtures,
  chart values, and workflow files, even when the code path that reads it is
  dead.
- **The two halves of authorization.** Coarse-grained access is enforced by the
  Heimdall rules in this repo's chart; fine-grained rules — invite ownership,
  join-mode gating, entitlement checks on org-scoped reads — are enforced in the
  handlers. A new or widened route needs both, and a change that relaxes either
  one silently changes who can reach the resource. Flag a route added to the
  design with no corresponding chart rule, a rule loosened to allow anonymous or
  unauthenticated access, and an in-handler check that a refactor moved off the
  path the request actually takes.
- **Identity: the principal is not an email.** The service receives a principal
  (an LFX username) and, when available, an email claim, and resolves the
  caller's authoritative email through the documented resolution path (see
  `docs/invite-application-flows.md`) rather than assuming the principal is one.
  Membership, invites, and applications key on email. Using the principal as an
  email, or skipping the resolution and trusting a client-supplied address, lets
  one user act as another — the invite and application flows are exactly where
  this bites.
- **Ownership checks on self-service flows.** Accept, decline, join, leave, and
  application submission are called by ordinary users, not admins. Each needs the
  resolved caller identity compared against the record it is acting on; flag a
  flow that trusts a UID from the path or body as proof of ownership. The
  membership-mode gate is a separate and narrower check: it belongs on the entry
  paths the mode actually governs — `SubmitApplication` (`join_mode ==
  "application"`) and `JoinCommittee` (`join_mode == "open"`) — where a gate
  written as a negative or partial condition instead of a positive equality
  check lets an empty or unknown `join_mode` through. Accepting or declining an
  invite, and leaving a committee, act on records that already exist and are
  gated by ownership alone; do not flag them for a missing mode check, and treat
  a mode gate added to `LeaveCommittee` as a finding in its own right, since it
  would trap members in a committee whose mode later changed.
- **Local-development bypasses.** The auth layer supports a mock principal for
  local runs. Any change that lets such a bypass be reachable in a deployed
  configuration, or that widens what it grants, is a finding.
- **PII in logs and errors.** Member and invitee emails, names, and usernames are
  PII, and this service handles them constantly. Identifiers that must appear in
  a log line go through `pkg/redaction`; tokens, authorization headers, and full
  payloads do not belong in logs at all. Flag a new log or error that emits a raw
  email, name, or credential — and note that error strings returned to clients
  count, since an error that echoes an address leaks it just as effectively.
- **What the response exposes.** When the diff adds or changes a field on a
  response, ask whose data it is and which check gates it. Server-derived and
  event-maintained fields should not be client-writable, and a newer or less
  travelled read path that returns the same data behind a weaker gate than its
  sibling path is the highest-value finding in this area.
- **Untrusted content reaching a model.** The weekly-brief generator feeds
  gathered source material — meeting, mailing-list, and vote content that users
  wrote — into an LLM prompt. Treat that material as untrusted: flag a change
  that interpolates it without the established boundary handling, that lets
  prompt-internal markers reach user-visible output, or that lets model output
  flow into a privileged action rather than into text.
- **Cross-service trust.** Replies from other services' NATS subjects are
  untrusted input too. A reply parsed without checking the error shape, or an
  upstream failure mapped to a validation error so it reads to the caller as
  their fault, hides real outages and can invert an authorization outcome.

## What not to flag

- Anything the deterministic pipeline owns: formatting, gofmt, lint nits, import
  ordering, license-header complaints, anything the compiler catches.
- Cosmetic Markdown and table-rendering nits on docs.
- Denial of service, resource exhaustion, or "add rate limiting" raised in the
  abstract, and race or timing issues you cannot trace to a concrete path. A
  traced defect — a pool sized from untrusted input, a shared map written from
  several goroutines — belongs under the concurrency dimension above.
- Outdated third-party dependencies; a *new* dependency's risk belongs to the
  architecture lens instead.
- Advice that could be pasted into any review with no defect behind it — a bare
  "add a nil check", "add a test", "rename this", or "extract a helper".
- Unguessability as authorization, in either direction: an authorization finding
  rests on a missing server-side check, never on whether a UID can be guessed —
  but validating an identifier's format against the contract that defines it
  remains a legitimate correctness concern.

## Judgment calls

- **Point at the working pattern.** When the diff violates a pattern, cite the
  sibling resource in this repo that does it correctly rather than describing an
  abstract ideal.
- **Do not propose rewrites of a sound approach**, and do not suggest change for
  its own sake; working, readable code needs no improvement.
- **Know your limits.** Distinguish "this is wrong" from "this might be a problem
  depending on context"; only the first is worth an author's attention. When a
  judgment depends on something you cannot see — the OpenFGA model, the generic
  fga-sync or indexer envelope, a deployed chart value, another service's
  contract — you cannot confirm it, so say nothing: do not assert the defect, and
  do not ask the author to verify it for you.
