<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Committee audit publisher (#4615)

## Status and owned contract

The publisher implements the principal/JWT attribution revision from
`linuxfoundation/lfx-self-serve-ops#1527`, using indexer SDK commit
`dd650b261397706082c0a40564f093884eee7bbd`. Indexer PR #93 and the #1526 cache
dependencies are merged. The generic wire contract is owned by
[`lfx-v2-indexer-service/docs/audit-v2-contract.md`](https://github.com/linuxfoundation/lfx-v2-indexer-service/blob/dd650b261397706082c0a40564f093884eee7bbd/docs/audit-v2-contract.md).
No temporary envelope fork or publisher identity cache is used.

**Capture and dispatch remain disabled by default.** Enablement still requires
PostgreSQL-primary storage (`postgres` or `dual-pg`), real JWT authentication,
approved publisher/schema policy, source bootstrap, private history permissions,
fresh service credentials and end-to-end deployment verification. This change
does not enable any environment or establish a production coverage start date.

## Attribution and evidence

HTTP writes retain `user:<principal>` using the exact verified Heimdall principal,
or `service:<client name>` for a `<client name>@clients` principal. The publisher
does not resolve principals to Auth0 IDs, look up actor email/name/avatar or
emit profiles, identity objects or verification flags. Those are indexer-owned.
Resource-owned names/contact fields remain governed by their separate schemas.

An ordinary request uses equal refs and captures its verified Heimdall token as
optional initiating/effective evidence. During impersonation, `principal` stays
the authorization target; the signed `act.username` supplies the original ref,
not `act.sub`. The target token is only effective-actor evidence. Without the
original user's own matching token, original evidence is absent and the indexer
will mark it unverified. A signed `act` claim is not a second user credential.

Trusted background changes identify `service:committee-service` explicitly;
they do not borrow a relay token to manufacture original-actor evidence.
Anonymous reads remain supported, but missing authentication never becomes a
system identity for a source write. Read-only HTTP calls do not capture evidence.

At dispatch the relay reloads and validates its own machine credential from
`COMMITTEE_AUDIT_TOKEN_FILE`; `headers.authorization` identifies the publisher.
Its verified expiry claim is required, and authorization is capped at 64 KiB
including the Bearer prefix. Capture and bootstrap preview reserve the same bound.
Invalid static dispatch configuration fails startup, but credential availability
is checked only at delivery. Missing/expired credentials and transient JWKS failures
pause dispatch without preventing API startup. Events remain retained and retry
with refreshed credentials; delivery failure logs and pending-event age must be
monitored. No unauthenticated publishing or user-token fallback is permitted.
No audited `x-on-behalf-of` is used. Dispatch credentials/raw header maps are
never stored. Optional actor JWTs are retained only in the protected source
outbox until retirement/pruning; the indexer strips them before fingerprinting,
history, receipts and downstream events. Tokens never belong in logs or traces.
Expired/missing actor evidence does not drop a committed event. The indexer owns
verification, retained username/email and first-append lookup/cache behavior;
query-service owns live display enrichment.

## Source transactions and revisions

`postgres.NewAuditedStore` captures before/after state, ownership scope, approved
snapshots and frozen outbox events in the same transaction as each source write.
Construction/outbox failure rolls back source changes too. Parent locks serialize
adopted child writes against committee deletion. Cascades capture every removed
member and settings row before deletion. Multi-step workflows may commit multiple
transactions; the events record those actual commits, not an imaginary atomic
whole request. Failed source transactions produce no success history.

Each serialized snapshot and current projection is limited to 192 KiB in audited
operation. This update-safe budget accommodates both before/after snapshots and
the projection instead of admitting oversized creates that cannot later be edited.
Creation and bootstrap also validate a hypothetical complete update with their
actual metadata and attribution, so metadata-heavy projections cannot bypass this.
The complete envelope must additionally fit the SDK's 1 MiB limit with the full
authorization allowance. Before cutover, run scoped bootstrap previews for every
committee. Oversized rows must be repaired in a controlled pre-adoption maintenance
window with normal writes paused and audit capture disabled, using existing native
source repair tools; re-run previews before
enablement. Do not truncate snapshots, omit required history, or rewrite frozen
outbox events to bypass the limit.

Row `revision` remains the optimistic-concurrency/ETag token and may advance on
derived writes. `committee_audit_revisions.resource_revision` separately tracks
the last authoritative business revision for each type/UID. It survives source
deletion, outbox pruning and rollback, so derived writes and replays do not reset
lineage. Business changes advance it transactionally; reindex events copy it
unchanged. Before cutover, advance the shared sequence above both migrated row
revisions and retained audit lineage. An out-of-order source revision fails closed.

## Coverage and non-mutation classification

| Source operation | Event treatment |
| --- | --- |
| Committee base/charter/governance changes | Mutation with before/after snapshots and scope. |
| Settings, review markers and role/grant changes | Mutation; raw webhook URL is excluded. |
| Social Listening access opt-in and category-transition resets | Settings mutations, including the persisted `members_can_view_social_listening` field. |
| Member create/update/delete and actual reassignment | Mutation; preserve pre-delete scope. Each committed reassignment/compensation is its own event. |
| Join/leave/invite acceptance/application approval | Their actual committed member/settings changes pass through audited storage. Invite/application resources are not adopted by these schemas. |
| Counts and mailing-list-derived flags | Reindex, no mutation content and no new authoritative audit revision. Row ETags still protect concurrent changes. |
| Committee/project metadata propagated into members | Reindex; already-synchronized records are skipped. |
| Pure account/profile shadow refreshes in user handlers | Reindex for the affected member fields. Actual settings grants or membership changes remain mutations. |
| Existing-seat invite username/name enrichment and CLI member-avatar/project/email-only refreshes | Reindex; business repairs and actual settings grants remain mutations. |
| CLI native source repairs and imports | Explicit service actor; business writes remain mutations. A machine actor or HTTP sync flag is not permission to suppress history. |
| `sync committee-fields --from=v2` republishes | Live republishes are blocked while capture is enabled; dry-run remains available. Real `--from=v1` business changes retain mutation auditing. |
| Source bootstrap/search repair | Projection-only backfill with the current authoritative audit revision. |

Only trusted internal paths select reindex. Storage checks its derived-field
allowlist: a projection cannot create/delete a resource or change member roles,
voting, seat ownership or other authoritative fields. Caller HTTP payloads and
headers cannot select this classification.

The three approved content representations remain sanitized full-resource
schemas: `committee.v1`, `committee_settings.v1` and `committee_member.v1`.
Their independent closed types are in `internal/domain/model/committee_audit.go`;
coverage tests compare source fields to the explicit inventory. Settings omit
`chat_webhook_url` and record only configured/unconfigured state. Public charter
projection data omits editor email. Never copy arbitrary directory profiles or
credential-bearing charter userinfo into retained snapshots. New or changed charter
URLs reject userinfo; an unchanged stored link sent back on update is kept, and
existing links are sanitized for both audit and projection output.
Never copy raw request bodies into snapshots. Expanding an adopted schema needs a new
version and review, not a permissive validator.

For mutations, the closed after-image and sanitized current projection must describe
the same shared state. The validator permits only documented representation differences,
such as charter editor email omitted from the public projection and parent project
scope added to settings snapshots. Size limits measure sanitized emitted projections.

Links/documents/briefs, invite/application resources and KV-primary storage are
not audit-adopted here. Other resource indexing and FGA/domain events keep their
contracts. Adopted types fence legacy index publication so it cannot overwrite
revision-guarded current projections.

## Delivery, bootstrap and replay

The separately enabled API relay uses acknowledged JetStream publishing, bounded
attempts, exponential retry/backoff and `FOR UPDATE SKIP LOCKED`. Delivery gets a
shorter deadline than its transaction, reserving time for retry bookkeeping and
commit so a poison event does not monopolize the queue. Relay/retention workers join API cancellation and
shutdown. PubAck means broker persistence, **not completed history persistence**.

Dispatch can drain retained events with capture disabled, provided PostgreSQL,
NATS, the explicit retention policy and publisher credential configuration remain
available. Pause normal source writes and keep legacy indexing fenced during this
drain; disabling capture is not authorization to overwrite adopted projections.

Each explicit bootstrap is a new projection operation with fresh event identity
and captured database occurrence time. Parent-dependent settings tags/scope are
captured with it, so a later parent-only edit does not reuse an immutable event ID.
Once stored, retries/replay preserve the exact original event. No bootstrap
creates historical mutation content or reconstructs v1 audit history.

Commands (PostgreSQL required):

```text
committee-cli audit status
committee-cli audit status --event-id <original-id>
committee-cli audit bootstrap --committee <uid> --dry-run
committee-cli audit bootstrap --committee <uid>
committee-cli audit replay --event-id <original-id> --dry-run
committee-cli audit replay --event-id <original-id>
committee-cli audit retire --event-id <original-id> --delivery-attempt <verified-attempt> --dry-run
committee-cli audit retire --event-id <original-id> --delivery-attempt <verified-attempt> --confirmed-persisted
committee-cli audit prune
```

Status exposes counts/age, not personal payloads. All audit CLI operations use
only PostgreSQL and remain usable without a NATS connection. Bootstrap dry-run
validates source eligibility without writing events or revision metadata. Replay requeues
the original bytes and cancels retirement; it never refreshes actor proof using
the relay identity. Indexer erasure fences and immutable-content checks still
apply. Legacy storage-changing, reindex/backfill and settings-index repair CLI paths remain blocked
while audited operation is enabled; complete cutover first.

## Retirement and retention

`COMMITTEE_AUDIT_OUTBOX_RETENTION` must be an explicitly configured positive Go
duration before capture can be enabled; the chart has no policy default.
Broker acknowledgment does not retire an event. After required indexer/history
processing is independently reconciled, an operator can confirm retirement using
`audit retire --delivery-attempt <verified-attempt> --confirmed-persisted`.
Obtain the broker-acknowledged attempt from `audit status --event-id`, reconcile
that delivery independently, then pass its attempt to retirement. Retirement is
idempotent for that generation; a replay/redelivery invalidates stale confirmation.
Dry-run checks the same acknowledgment/generation predicates without writing state.
It is not an assertion of processing made by the relay.

Only retired, broker-delivered rows older than the limit are pruned. Pending and
PubAck-only rows remain repair sources; replay clears retirement. The API's
minute-based maintenance also runs when dispatch is paused. Each prune call deletes
at most 1,000 eligible rows in a separately committed batch; later
deadlines cannot undo prior batches. Concurrent workers skip locked selections.
Repeat `audit prune` to process additional batches. Pruning does not
delete revision lineage or history. Coordinate credential-bearing outbox copies,
backups, access controls, erasure/export and durable failed-work ownership with
the approved retention policy. Monitor pending age and exhausted indexer delivery;
do not retire unprocessed events merely to make a TTL pass.

## Rollout and compatibility

1. Verify PostgreSQL cutover, actor forwarding and the issuer/audiences accepted
   for request evidence and publisher authentication. Do not retarget user tokens.
2. Deploy compatible indexers/readers, private mappings/roles and approved schemas
   and origins, including mutation, reindex and backfill for adopted types.
3. Pause legacy indexing and audited source writes; drain queued legacy work,
   initialize source projections and reconcile bootstrap before mutation dispatch.
4. Configure both opt-in flags, the explicit retention limit and refreshed service
   credential. Verify fresh-token, delayed/unverified, deletion and reindex cases
   end to end. Record actual coverage start times and known gaps.

The SDK retains schema version 2 as an owner-approved pre-adoption correction;
profile-bearing old inputs are rejected, not converted. If unexpected old frozen
rows or consumers exist, stop adoption and agree a compatibility/repair plan.
Do not strip profiles or relabel Auth0-ID refs under an existing event identity.
Rollback must pause dispatch, preserve repair data/lineage and never downgrade
pending audit work to legacy messages.
