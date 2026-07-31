# Service Chart Coupling & Concurrency

Two related repo-specific surfaces: (1) service-local Helm chart resources
(`charts/lfx-v2-committee-service/`) that must move in lockstep with code — Heimdall rulesets for new
endpoints, KV buckets for new buckets, env vars for new config — and (2) concurrency primitives used in
this service (worker pools, goroutines, JetStream consumers, RNG).

**Read when:** any file under `charts/lfx-v2-committee-service/**`, `pkg/constants/storage.go`,
`pkg/constants/subjects.go`, `cmd/committee-api/design/**` (new endpoints), `cmd/committee-api/service/providers.go`
(env vars and subscriptions), `cmd/committee-api/service/committee_handler.go` (inbound dispatch),
`internal/infrastructure/nats/client.go` / `stream_consumer.go`, `pkg/concurrent/**`, or any
file launching goroutines / using `errgroup`.

---

## `chart-and-concurrency/new-endpoint-needs-ruleset` — Critical

**Pattern:** a new HTTP endpoint is added to the Goa design without a matching rule in
`charts/lfx-v2-committee-service/templates/ruleset.yaml`. Without the Heimdall RuleSet entry the endpoint is
blocked (or, worse, a create/write route falls through to `allow_all` while OpenFGA is enabled). Rule IDs
follow `rule:lfx:lfx-v2-committee-service:<resource>:<action>`; self-action routes (join/leave/accept/
decline/submit) use `allow_all` + `oidc`, managed routes use `openfga_check` with the right relation.

**Detect:** when the diff adds a route under `cmd/committee-api/design/**`, check that
`charts/.../templates/ruleset.yaml` is also changed with a corresponding rule. Flag a create/write route
whose ruleset uses `allow_all` when an OpenFGA relation is expected.

**Empirical citation:** PR #61 `cmd/committee-api/design/committee.go:499` (andrest50) — "Every new endpoint also needs a ruleset entry in `charts/lfx-v2-committee-service/templates/ruleset.yaml` — this is what controls authentication and authorization at the gateway level via Heimdall. Without it, the endpoints will be blocked in any deployed environment." Recurs PR #97 `committee.go:1381` / PR #98 `committee.go:1429` (jordane, "This needs a corresponding update in the ruleset in the helm chart") and PR #11 `ruleset.yaml:31` (CodeRabbit, "Create route currently allows all even when OpenFGA is enabled — security risk").

**Failure message:** New endpoint added without a matching Heimdall RuleSet rule (or a write route left as `allow_all` under OpenFGA).

**Fix:** add a rule to `ruleset.yaml` with id `rule:lfx:lfx-v2-committee-service:<resource>:<action>`; use `openfga_check` + the correct relation for managed routes and `allow_all` + `oidc` only for genuine self-actions.

---

## `chart-and-concurrency/new-bucket-or-env-needs-chart` — Critical

**Pattern:** a new KV bucket / Object Store / stream constant (`pkg/constants/storage.go`) is added without
creating it in the chart (the template owning that resource type + `values.yaml`), or a new env var read in
`providers.go` is not declared in the chart `deployment.yaml`/`values.yaml`. The bucket won't exist at
runtime, or the env var won't be wired.

**Detect:** when the diff adds a storage constant, check the template that owns **that resource type**,
plus `values.yaml`. This chart has one template per type, so the mapping is exact:

| Constant added in `pkg/constants/storage.go` | Template that must also change |
| --- | --- |
| `KVBucketName*` | `templates/nats-kv-buckets.yaml` |
| `StreamName*` | `templates/nats-streams.yaml` |
| `ObjectStoreName*` | `templates/nats-object-stores.yaml` |

Do not report a missing `nats-kv-buckets.yaml` edit for an Object Store or stream constant — the resource
would be created correctly and the finding would be wrong. When
`providers.go` reads a new `env.*`/`os.Getenv` value, check that it is declared under **`values.yaml`
→ `app.environment`** — this chart declares env there and nowhere else, because `deployment.yaml` is a
generic range loop over that map. Pointing a reviewer at `deployment.yaml` produces a wrong finding.

**A lookup-key prefix inside an existing bucket owes no chart change.** PR #161's
`KVLookupMembersByUsername*` constants are prefixes within the existing `committee-members` bucket, so the
absence of a chart edit there was correct.

**Empirical citation:** PR #97 `pkg/constants/storage.go:79` (jordane) — "This nats kv bucket needs to be created via the helm chart, so this PR needs to add that as well" (recurs :83, :89). Endorsed by andrest50 PR #61 `client.go:152` ("These two new buckets must be created in Kubernetes via Helm"). Env-var coupling PR #98 `providers.go:273/514/574` (jordane, "These env vars should be called out in the helm chart as well ... need to be added to the helm chart values.yaml").

**Revised 2026-07-30 — bucket half upheld, env half violated, Detect corrected.** All 14
bucket/stream/object-store constants map 1:1 to `values.yaml` + template, and the lookup-prefix carve-out
above was added in the same pass. The env half is broadly violated: **8 env vars read in `providers.go` are
undeclared in the chart** — `AUTH_SOURCE`, `LFX_ENVIRONMENT`, `LFX_SELF_SERVE_BASE_URL`, `MESSAGING_SOURCE`,
`NATS_MAX_RECONNECT`, `NATS_RECONNECT_WAIT`, `NATS_TIMEOUT`, `REPOSITORY_SOURCE`. The PR #97/#61/#98 threads
are retained as provenance.

**Failure message:** New KV bucket / stream / env var added in code but not wired in the service Helm chart.

**Fix:** add the resource to the template that owns its type — `nats-kv-buckets.yaml`, `nats-streams.yaml`, or `nats-object-stores.yaml` per the Detect table — plus its `values.yaml` entry; and any new env var to `values.yaml` → `app.environment`; prefer `valueFrom` over inline secrets.

---

## `chart-and-concurrency/worker-pool-and-goroutine-hygiene` — Important

**Pattern:** concurrency primitives are misused: a worker pool / `errgroup.SetLimit` sized by the length of a
**caller- or input-controlled** slice (or used where only one goroutine is ever spawned — pure overhead), a
goroutine spawned without panic recovery or lifecycle tracking, or a JetStream consumer stop-func discarded
(no graceful shutdown).

**Detect:** in changed Go, flag a pool or `SetLimit` sized by `len()` of a slice whose length the caller or
an inbound message controls; `errgroup` + `SetLimit` where only one `g.Go` runs; `go func(){...}()` without
`recover()` on a fire-and-forget path; a returned stop/cancel func discarded with `_`.

**Two clauses this entry used to carry are now noise — do not flag them:**

- **Literal `NewWorkerPool(len(messages))`.** As a literal Detect this yields **5 false positives and 0 true
  ones** at `main@bd39fe9`: all five slices are statically bounded 2-4 element literals. Only an
  input-controlled length is a finding, which is why the clause is reworded above.
- **RNG seeding and loop-variable capture.** Go 1.20+ auto-seeds the global source, the `cap`-shadowing half
  was fixed (`stream_consumer.go:167`), and loop-var capture is moot at `go 1.25.0`. Prefer `math/rand/v2` as
  style, not a finding.

**Two clauses are live violations at `main@bd39fe9`** — quote these:

- fire-and-forget goroutines with no `recover()` at `internal/service/committee_writer.go:581` and
  `internal/service/committee_member_writer.go:382`. The nearby `recover()`s are on the parent stack and do
  not protect the child;
- discarded consumer stop-funcs at `cmd/committee-api/service/providers.go:933` and `:944`, despite
  `internal/infrastructure/nats/stream_consumer.go:29-30` documenting that the caller must stop them.

**Empirical citation:** PR #4 `internal/service/committee_writer.go:324` (Copilot) — "The worker pool is created with a size equal to the number of messages ... Consider using a fixed, reasonable pool size." Recurs PR #91 `message_handler.go:506` (dealako, "`errgroup` + `SetLimit(5)` machinery is pure overhead here" for one goroutine), PR #19 `committee_member_writer.go:267` (Copilot, cleanup goroutine with no panic recovery), PR #85 `providers.go:444` (Copilot, consumer stop func discarded), PR #85 `stream_consumer.go:12` (Copilot, `math/rand` jitter never seeded), PR #4 `committee_writer.go:313` (loop-var capture).

**Revised 2026-07-30 — two clauses retired, two confirmed live.** See the retired/live breakdown above. The
PR #4/#91/#19/#85 threads are retained as this entry's provenance even where the specific clause they
motivated has been narrowed.

**Failure message:** Concurrency primitive misused — pool sized by an input-controlled length, overhead errgroup, unrecovered fire-and-forget goroutine, or discarded consumer stop-func.

**Fix:** size pools to a fixed bound; drop `errgroup` for a single goroutine; add `recover()` inside the fire-and-forget goroutine itself; store and invoke the consumer stop-func on shutdown.

---

## `chart-and-concurrency/total-members-recount-correctness` — Important

**Pattern:** the `total_members` denormalization (driven by the `committee-member-events` JetStream stream /
`committee-service-total-members` consumer) drifts: the recount counts all member records without filtering
by `Status` (over-counts inactive members), only reacts to created/deleted and misses status-change updates,
or runs the full committee update workflow (project/user lookups) for what should be a narrow counter update.

**Detect:** in `internal/service/message_handler.go` (`HandleCommitteeTotalMembersSync` / recount path),
check the count filters to active members, that relevant `committee_member.updated` status changes are
handled (or explicitly documented as not affecting the count), and that the update path is narrow (not the
full `Update` orchestration).

**Empirical citation:** PR #85 `internal/service/message_handler.go:372` (Copilot) — "`actualCount := len(members)` counts every stored member record returned by `ListMembers` without filtering by `CommitteeMember.Status` ... this can overcount if inactive members are retained." Recurs same PR :331 (misses `committee_member.updated` status transitions) and :400 ("Using `committeeWriterOrchestrator.Update(...)` here will run the full committee update workflow ... Consider a narrower path").

**Failure message:** `total_members` recount over-counts inactive members, misses status-change events, or uses the full update workflow for a counter bump.

**Fix:** filter the count to active members (or document inclusion of inactive); handle status-changing `committee_member.updated` events (or document the exclusion); use a narrow `UpdateBase` + indexer publish path instead of the full update orchestration.

---

## `chart-and-concurrency/handler-registered-but-not-subscribed` — Critical

**Pattern:** a new inbound NATS subject gets a constant and a dispatch case in the message handler, but is
never added to the subscription map. The handler is reachable in unit tests, which call it directly, and
unreachable in every deployed environment, which only ever delivers what the service subscribed to.

**Detect:** the diff adds an inbound subject constant to `pkg/constants/subjects.go` **and** a dispatch case
in `cmd/committee-api/service/committee_handler.go`. Require that same subject to appear in the
`QueueSubscriptions` map in `cmd/committee-api/service/providers.go` (see the block around `:915`, where every
live inbound subject is mapped to a handler). A dispatch case with no subscription entry is the finding.

Do **not** extend this to the `references/nats-messaging.md` inventory update. That obligation is quarantined
pending a human decision — see the README.

**Empirical citation:** PR #161 `cmd/committee-api/service/committee_handler.go:43`
(`copilot-pull-request-reviewer`, thread `r3659193428`, 2026-07-27T16:49:00Z) — "Registering the dispatcher
here is insufficient: `QueueSubscriptions` only subscribes to the subjects listed in
`cmd/committee-api/service/providers.go:899-913`, and this new subject is absent there. Consequently, deployed
instances never receive these deletion events."

Fixed in `ceab5a1` at 17:10:19Z — 21 minutes after the finding — which added
`constants.V1SyncHelperUserDeletedSubject: messageHandlerService.HandleMessage` to the map. Verified at
`main@bd39fe9` `cmd/committee-api/service/providers.go:915`.

**Provenance caveat, recorded deliberately:** the developer's thread reply at 18:29Z read "No longer
applicable — registered in providers.go:913". That was true only because his own review-response commit had
just added it. The finding was real and was acted on; the reply's framing is not supported by the history.
A reply is not fix evidence in either direction — check the commit.

**Why it earns a place:** cost of miss is total and silent. The code compiles, the unit tests pass because
they invoke the handler directly, and the event is never delivered anywhere it matters. No test in this
repo's current shape can catch it.

**Failure message:** New inbound subject dispatched in the handler but absent from `QueueSubscriptions` — deployed instances will never receive these events.

**Fix:** add the subject to the `QueueSubscriptions` map in `cmd/committee-api/service/providers.go` in the same change as the dispatch case.
