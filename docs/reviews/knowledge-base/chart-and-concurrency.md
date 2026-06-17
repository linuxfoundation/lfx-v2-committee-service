# Service Chart Coupling & Concurrency

Two related repo-specific surfaces: (1) service-local Helm chart resources
(`charts/lfx-v2-committee-service/`) that must move in lockstep with code — Heimdall rulesets for new
endpoints, KV buckets for new buckets, env vars for new config — and (2) concurrency primitives used in
this service (worker pools, goroutines, JetStream consumers, RNG).

**Read when:** any file under `charts/lfx-v2-committee-service/**`, `pkg/constants/storage.go`,
`pkg/constants/subjects.go`, `cmd/committee-api/design/**` (new endpoints), `cmd/committee-api/service/providers.go`
(env vars), `internal/infrastructure/nats/client.go` / `stream_consumer.go`, `pkg/concurrent/**`, or any
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
creating it in the chart (`templates/nats-kv-buckets.yaml` + `values.yaml`), or a new env var read in
`providers.go` is not declared in the chart `deployment.yaml`/`values.yaml`. The bucket won't exist at
runtime, or the env var won't be wired.

**Detect:** when the diff adds a `KVBucketName*`/`StreamName*`/Object-Store constant, check
`templates/nats-kv-buckets.yaml` (and `nats-streams.yaml`) + `values.yaml` are also changed. When
`providers.go` reads a new `env.*`/`os.Getenv` value, check `deployment.yaml`/`values.yaml` declare it.

**Empirical citation:** PR #97 `pkg/constants/storage.go:79` (jordane) — "This nats kv bucket needs to be created via the helm chart, so this PR needs to add that as well" (recurs :83, :89). Endorsed by andrest50 PR #61 `client.go:152` ("These two new buckets must be created in Kubernetes via Helm"). Env-var coupling PR #98 `providers.go:273/514/574` (jordane, "These env vars should be called out in the helm chart as well ... need to be added to the helm chart values.yaml").

**Failure message:** New KV bucket / stream / env var added in code but not wired in the service Helm chart.

**Fix:** add the bucket/stream to `templates/nats-kv-buckets.yaml`/`nats-streams.yaml` + `values.yaml`, and any new env var to `deployment.yaml`/`values.yaml`; prefer an env-list pattern over scattered keys and `valueFrom` over inline secrets.

---

## `chart-and-concurrency/worker-pool-and-goroutine-hygiene` — Important

**Pattern:** concurrency primitives are misused: a worker pool / `errgroup.SetLimit` sized to the number of
messages (or used where only one goroutine is ever spawned — pure overhead), a goroutine spawned without
panic recovery or lifecycle tracking, a JetStream consumer stop-func discarded (no graceful shutdown), a
loop variable captured by reference in a closure, or `math/rand` used for retry jitter without seeding (and
shadowing the builtin `cap`).

**Detect:** in changed Go, flag `NewWorkerPool(len(messages))` / pool sized to input length;
`errgroup` + `SetLimit` where only one `g.Go` runs; `go func(){...}()` without `recover()` on a
fire-and-forget path; a returned stop/cancel func discarded with `_`; `subject := subject` / loop-var
capture issues; `math/rand` jitter without a seeded/`math/rand/v2` source.

**Empirical citation:** PR #4 `internal/service/committee_writer.go:324` (Copilot) — "The worker pool is created with a size equal to the number of messages ... Consider using a fixed, reasonable pool size." Recurs PR #91 `message_handler.go:506` (dealako, "`errgroup` + `SetLimit(5)` machinery is pure overhead here" for one goroutine), PR #19 `committee_member_writer.go:267` (Copilot, cleanup goroutine with no panic recovery), PR #85 `providers.go:444` (Copilot, consumer stop func discarded), PR #85 `stream_consumer.go:12` (Copilot, `math/rand` jitter never seeded), PR #4 `committee_writer.go:313` (loop-var capture).

**Failure message:** Concurrency primitive misused — pool sized to input, overhead errgroup, unrecovered goroutine, discarded consumer stop-func, loop-var capture, or unseeded jitter RNG.

**Fix:** size pools to a fixed bound; drop `errgroup` for a single goroutine; add `recover()` to fire-and-forget goroutines; store and invoke the consumer stop-func on shutdown; rebind loop variables (or use Go 1.22+ semantics); use `math/rand/v2` (or a seeded source) for jitter and rename any `cap` variable.

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
