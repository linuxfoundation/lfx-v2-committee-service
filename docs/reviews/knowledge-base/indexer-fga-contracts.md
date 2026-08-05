# Indexer & FGA Contracts

Patterns where indexer (`lfx.index.*`) or FGA (`lfx.fga-sync.*`) emission code drifts from
the committee-owned contracts (`docs/indexer-contract.md`, `docs/fga-contract.md`) or from the
generic indexer/fga-sync envelopes. These are contract-violation patterns: a single miss ships a
message the indexer or fga-sync silently drops or mis-processes, so cost-of-miss promotes them
even at one occurrence.

**Read when:** any file under `internal/service/*writer.go`, `internal/service/message_handler.go`,
`internal/domain/model/committee_*.go` (Tags/Build), `internal/infrastructure/nats/messaging_publish.go`,
`pkg/constants/subjects.go`, `cmd/committee-api/service/error.go` and `pkg/errors/**` (the documented HTTP
status ↔ error-type `wrapError` mapping that `contract-doc-out-of-sync` checks), `docs/indexer-contract.md`,
`docs/fga-contract.md`, or migration scripts under `scripts/migrations/**` that publish to index/fga subjects.

**Also read on any changed `.go` file, regardless of path.** `subject-literal-must-use-constant` greps every
changed Go file for hardcoded `lfx.*` subject or bucket-name literals, so no path list can gate it. Route
this file whenever the diff contains Go, and evaluate that one entry even when nothing above matches.

---

## `indexer-fga-contracts/missing-indexing-config` — Critical

**Pattern:** a new indexer message is published (new sub-resource: invite, application, link, folder,
document, group_weekly_brief) without setting `IndexingConfig`. Unlike `committee`/`committee_member`,
there is no server-side enricher registered for the newer sub-resources, so the indexer cannot process
a message that lacks a client-supplied `IndexingConfig` (with `ObjectID`, `AccessCheckObject/Relation`,
`HistoryCheckObject/Relation`, `ParentRefs`, `Public`). `IndexingConfig` must be set on **create and
update** actions.

**Delete is excluded.** This repo's convention is that delete messages carry the UID only.
`internal/domain/model/committee_message.go:66` says callers populate `IndexingConfig` "for create and
update actions", and five delete paths deliberately send only the UID with an explanatory comment:
`committee_member_writer.go:1187-1189`, `document_writer.go:189-190`, `link_writer.go:221-222` and
`:272-273`, `committee_writer.go:1007-1009`. A missing `IndexingConfig` on a delete publish is **not** a
finding.

**Detect:** for each `model.CommitteeIndexerMessage{...}` or `*IndexerMessage` built in a new **create or
update** publish path, grep the surrounding function for `IndexingConfig`. Compare against the existing
`buildCommitteeIndexingConfig` in `internal/service/committee_writer.go`. Flag if such a path for a
non-`committee`/non-`committee_member` object omits it. Skip delete paths entirely.

**Empirical citation:** PR #61 `cmd/committee-api/service/committee_service.go:764` (andrest50) — "there is no server-side enricher registered in the indexer service for `committee_invite` or `committee_application`, so the indexer will fail to process these messages without a client-supplied `IndexingConfig`." Recurs PR #68 `internal/service/link_writer.go:188` (mauriciozanettisalomao) — "you have to use the IndexConfig structure to send what you need to be indexed, mainly around the access check."

**Revised 2026-07-30** (re-audit at `origin/main@ec86a8f`, re-verified at `bd39fe9`). The create/update
half is upheld at 11 sites and unchanged. The original "must be set for **all** actions including delete"
clause was contradicted by the code *and* by the repo's own model doc, and fired on five
correct-by-convention delete sites; it has been scoped to create/update. The original PR #61/#68 citations
above are retained as the entry's provenance.

**Failure message:** Indexer message for a new sub-resource is built without `IndexingConfig` on a
create/update publish — the indexer will drop it (no enricher exists for this object type).

**Fix:** populate `IndexingConfig` (ObjectID, AccessCheckObject/Relation pointing at the parent committee, HistoryCheckObject/Relation, ParentRefs, Public, Tags) on create and update, mirroring `buildCommitteeIndexingConfig`. Leave delete publishes carrying the UID only.

---

## `indexer-fga-contracts/contract-doc-out-of-sync` — Important

**Pattern:** code changes the indexed payload, tag set, FGA `data` fields, or invite/application status
transitions, but the matching contract doc (`docs/indexer-contract.md`, `docs/fga-contract.md`,
`docs/invite-application-flows.md`) is not updated in the same PR. Includes: new `Tags()` entries, new
indexed fields, new object types, `omitempty`/optional fields documented as required, and HTTP-status
mismatches between docs and `wrapError`.

**Two further shapes survive a same-PR doc update** and are part of this entry:

- **Internally inconsistent doc.** One section of a doc is updated while another section of the *same* doc
  still states what the change falsified — prose against a trigger table, one flow's description against
  another's.
- **Overstated delivery guarantee.** A doc claims a core-NATS publish is acknowledged, confirmed, or
  delivered. `nats.Conn.PublishMsg` returns after enqueuing to the client buffer; without `Flush` a nil
  return is not a broker acknowledgement.

**Detect:** if the diff touches a `Tags()` method, an indexed struct (`internal/domain/model/committee_*.go`),
a `build*IndexingConfig`, an FGA message builder, or invite/application status strings, check whether the
corresponding `docs/*.md` is also in the diff. Flag mismatches in field-optionality, tag presence, object
types listed, or documented HTTP status vs `pkg/errors` type → `wrapError` mapping. Additionally: when the
patch edits a section of `docs/fga-contract.md`, `docs/indexer-contract.md`, or
`docs/invite-application-flows.md`, sweep the rest of that same file for statements the edit made untrue,
and flag any sentence claiming a core-NATS publish was acknowledged.

**Empirical citation:** PR #76 `docs/indexer-contract.md:157` (Copilot) — "The committee member indexer contract doesn't mention the newly added `project_uid`/`project_slug` fields or the new `project_uid:`/`project_slug:` tags". Recurs PR #70 `docs/indexer-contract.md:48` ("Several fields ... are marked as non-optional here, but the source struct ... uses `omitempty`/pointers"), PR #33 `internal/domain/model/committee_base.go:141` ("documentation updates are required when modifying the `Tags()` method"), PR #65 `docs/invite-application-flows.md:143` ("Docs say identity lookup failures return `422` ... but the implementation returns `errors.Validation`, which `wrapError` maps to `BadRequest` (HTTP 400)").

**Revised 2026-07-30.** The strongest entry in this KB — 26 further findings of this shape in the
2026-07 window, with fixes verified in `main` for PR #150 (`d685ca7`, `15b932f`, `7859f6a`), #154, #156,
#157, #160 (`35dcb5c`), and #163 (`bfbc5a1`, which updated `docs/fga-contract.md:37`). The two shapes above
were added from that window: PR #156 thread `r3632518923` (*"The updated identity-resolution contract is
still internally inconsistent: the invite flow at line 80 … and the application flow at line 124"*),
PR #160 thread `r3658684364` (prose contradicting the trigger table two sections below), and PR #160 thread
`r3658684324` (*"`nats.Conn.PublishMsg` has no broker acknowledgement and may only enqueue data in the
client buffer"*) — the last verified fixed at `docs/fga-contract.md:39`.

**Live violation at `main@bd39fe9` — an illustration, not a standing finding.** Report it **only** when
the diff itself changes the username-clearing emission path or the contradictory parts of
`docs/fga-contract.md`. Do not revive it against an unrelated edit that merely happens to touch the same
file or the FGA area: a pre-existing drift is not something that change introduced, and reporting it there
buries whether the patch under review made anything worse. The brain's own exclusion — "anything about code
the change does not touch" — governs.
`internal/service/committee_member_writer.go:1254` emits `member_remove` when an update clears the
username, while `docs/fga-contract.md:113` and the trigger table at `:189` still describe `member_remove`
as delete-only and say updates with an empty username are skipped. Copilot raised exactly this on PR #161
and the author deferred it — so the drift is current, not historical.

**Failure message:** Emitted-event behavior changed but the owned contract doc was not updated in the same change; or the doc now contradicts itself, or claims a delivery guarantee the transport does not provide.

**Fix:** update `docs/indexer-contract.md` / `docs/fga-contract.md` / `docs/invite-application-flows.md` in the same PR; mark optional/`omitempty` fields as optional and keep documented HTTP status aligned with the `pkg/errors` type that `wrapError` maps. After editing one section, re-read the whole document — trigger tables especially — for statements the edit falsified, and describe core-NATS publishes as accepted-for-delivery rather than acknowledged.

---

## `indexer-fga-contracts/migration-must-use-envelope` — Critical

**Pattern:** a migration/backfill script under `scripts/migrations/**` publishes raw KV record JSON
(or hand-built data) directly to an `lfx.index.*` subject instead of wrapping it in the
`CommitteeIndexerMessage` envelope (action/headers/data/indexing_config). Downstream consumers expect the
envelope shape and will ignore or fail to decode the raw record, leaving OpenSearch out of sync.

**Detect:** in `scripts/migrations/**/main.go`, find publishes to `IndexCommittee*Subject` constants and
verify the payload is a marshalled `model.CommitteeIndexerMessage` (with `Action` + `IndexingConfig`), not
the raw `baseData`/`settingsData`/record bytes.

**Empirical citation:** PR #78 `scripts/migrations/migrate_join_mode_to_base/main.go:348` (Copilot/CodeRabbit) — "Publishing the raw KV JSON is likely to be ignored or fail to decode by downstream consumers ... Consider publishing the same envelope shape as `internal/service/committee_writer.go` (including auth headers and ... IndexingConfig/tags)."

**Revised 2026-07-30 — citation repointed.** The cited script `migrate_join_mode_to_base/` has been
deleted, so the original citation no longer resolves. The invariant is still violated in `main@bd39fe9` by
three live scripts, which `json.Marshal` a raw map and publish it to an `lfx.index.*` subject:
`scripts/migrations/migrate_counsel_role/main.go:244-248`,
`migrate_member_visibility/main.go:285-292`, and `migrate_show_meeting_attendees/main.go:285-292`.
The compliant counter-example to copy is
`migrate_writers_auditors_to_user_objects/main.go:344-363`. The PR #78 thread above is retained as the
entry's origin.

**Failure message:** Migration publishes raw record JSON to an index subject instead of the `CommitteeIndexerMessage` envelope — the indexer will ignore it.

**Fix:** build a `CommitteeIndexerMessage` with the correct `Action` and `IndexingConfig` and publish that; set the auth headers via `Build(ctx, ...)` as the service does.

---

## `indexer-fga-contracts/subject-bucket-literal` — Important

**Pattern:** a NATS subject, queue group, KV bucket, Object Store, or stream name is hardcoded as a string
literal at a call site instead of referencing a `pkg/constants` symbol (`subjects.go` / `storage.go`).
Also: a settings/sub-resource indexer publish uses the wrong subject constant.

**Detect:** grep changed Go files for string literals matching `"lfx\.(index|fga-sync|committee-api|projects-api|auth-service|mailing-list-api)\.` or bucket-name literals (`"committee-...`); any such literal outside `pkg/constants/` is a finding. Cross-check that each indexer publish uses the subject constant matching its object type.

**Empirical citation:** PR #6 `internal/service/committee_writer.go:734` (CodeRabbit) — "Publishes settings indexer on the wrong NATS subject". Reinforced by repo rule (`nats-messaging.md`): "Never hardcode a subject or bucket string at a call site."

**Failure message:** NATS subject / KV bucket literal hardcoded at a call site (or wrong subject constant for the object type).

**Fix:** reference the `pkg/constants` subject/bucket symbol; if a new subject/bucket is introduced, add it to `pkg/constants`.

**Scope:** this rule stops at `pkg/constants`. The further obligation to update
`references/nats-messaging.md` alongside a subject or bucket change is **quarantined** — see the README's
quarantined contradictions — so do **not** emit a finding for a missing `nats-messaging.md` update, and do
not treat its absence as approval either. `chart-and-concurrency.md` states the same boundary; both defer to
the human decision rather than to each other.

---

## `indexer-fga-contracts/skip-empty-username-relations` — Important

**Pattern:** FGA `member_put`/`update_access` relations or member access messages are built without
skipping users that have an empty `Username`, or settings writers/auditors relations are populated even
when the slice is empty. The fga-contract requires: skip users with empty `Username`; only set `writer`/`auditor`
relations when the slice is non-empty; `exclude_relations: ["member"]` always set on `update_access`.

**Detect:** in `committee_writer.go` / `committee_member_writer.go` FGA message construction, verify the
`Username == ""` skip guard and the non-empty checks on Writers/Auditors before setting relations.

**Empirical citation:** PR #72 `cmd/committee-api/service/committee_service_response.go:660` (CodeRabbit) — "Don't persist role entries without `Username`." Supported by `docs/fga-contract.md`: "Users with an empty `Username` are skipped" and relations set "Only when ... non-empty".

**Failure message:** FGA member/relation message built without the empty-`Username` skip or non-empty relation guard required by the FGA contract.

**Fix:** skip `CommitteeUser` entries with empty `Username`; only add `writer`/`auditor` relations when the source slice is non-empty; keep `exclude_relations: ["member"]` on `update_access`.

---

## `indexer-fga-contracts/generic-publisher-bypasses-transport-invariant` — Important

**Pattern:** a per-subject transport rule — this subject is async-only, that one is always sync — is
enforced in the named wrapper method but not in the generic subject-taking method beside it. Any caller that
passes the subject directly to the generic method bypasses the rule entirely, and the wrapper's guard reads
like protection it is not providing.

**Detect:** when `docs/fga-contract.md` documents a transport invariant for a subject, require the guard
inside the generic publisher entry point — `(*messagePublisher).Access` in
`internal/infrastructure/nats/messaging_publish.go:128-133` — and not only in the named
`UpdateAccess`/`DeleteAccess` wrappers. A new subject with a documented transport invariant and no `Access`
guard is the finding.

**Empirical citation:** PR #160 `internal/infrastructure/nats/messaging_publish.go:130`
(`copilot-pull-request-reviewer`, thread `r3658684265`) — "The asynchronous-only invariant is still
bypassable through `CommitteePublisher.Access`: it accepts any subject, so a caller can pass
`GenericUpdateAccessSubject` with `sync=true` and still reach `requestMessage`. Guard that subject in
`Access` by delegating to `UpdateAccess` regardless of `sync`." Fixed in `35dcb5c` with
`TestMessagePublisher_AccessGuardsUpdateAccessSubject` asserting `client.requested == 0`.

**Recurrence — the team applied the same guard twice.** At `ec86a8f` only `update_access` was guarded, with
PR #162 open applying the invariant to `delete_access`; that PR has since merged. At `main@bd39fe9` `Access`
guards **both** subjects (`messaging_publish.go:128-133`), with
`TestMessagePublisher_AccessGuardsDeleteAccessSubject` alongside the original
(`internal/infrastructure/nats/messaging_publish_test.go:110` and `:133`). Extending the guard to a second
subject is what promotes this from a one-off fix to a pattern.

**Failure message:** Transport invariant enforced only in the named wrapper — a caller passing this subject to the generic `Access` method bypasses it.

**Fix:** guard the subject inside `Access` by delegating to the named method regardless of `sync`, and add a test asserting the request path was not taken.
