# Indexer & FGA Contracts

Patterns where indexer (`lfx.index.*`) or FGA (`lfx.fga-sync.*`) emission code drifts from
the committee-owned contracts (`docs/indexer-contract.md`, `docs/fga-contract.md`) or from the
generic indexer/fga-sync envelopes. These are contract-violation patterns: a single miss ships a
message the indexer or fga-sync silently drops or mis-processes, so cost-of-miss promotes them
even at one occurrence.

**Read when:** any file under `internal/service/*writer.go`, `internal/service/message_handler.go`,
`internal/domain/model/committee_*.go` (Tags/Build), `pkg/constants/subjects.go`, `docs/indexer-contract.md`,
`docs/fga-contract.md`, or migration scripts under `scripts/migrations/**` that publish to index/fga subjects.

---

## `indexer-fga-contracts/missing-indexing-config` — Critical

**Pattern:** a new indexer message is published (new sub-resource: invite, application, link, folder,
document, group_weekly_brief) without setting `IndexingConfig`. Unlike `committee`/`committee_member`,
there is no server-side enricher registered for the newer sub-resources, so the indexer cannot process
a message that lacks a client-supplied `IndexingConfig` (with `ObjectID`, `AccessCheckObject/Relation`,
`HistoryCheckObject/Relation`, `ParentRefs`, `Public`). `IndexingConfig` must be set for **all** actions
including delete (the indexer needs `ObjectID` to remove the document).

**Detect:** for each `model.CommitteeIndexerMessage{...}` or `*IndexerMessage` built in a new publish path,
grep the surrounding function for `IndexingConfig`. Compare against the existing `buildCommitteeIndexingConfig`
in `internal/service/committee_writer.go`. Flag if a publish path for a non-`committee`/non-`committee_member`
object omits it.

**Empirical citation:** PR #61 `cmd/committee-api/service/committee_service.go:764` (andrest50) — "there is no server-side enricher registered in the indexer service for `committee_invite` or `committee_application`, so the indexer will fail to process these messages without a client-supplied `IndexingConfig`." Recurs PR #68 `internal/service/link_writer.go:188` (mauriciozanettisalomao) — "you have to use the IndexConfig structure to send what you need to be indexed, mainly around the access check."

**Failure message:** Indexer message for a new sub-resource is built without `IndexingConfig` — the indexer will drop it (no enricher exists for this object type).

**Fix:** populate `IndexingConfig` (ObjectID, AccessCheckObject/Relation pointing at the parent committee, HistoryCheckObject/Relation, ParentRefs, Public, Tags) for create/update AND delete, mirroring `buildCommitteeIndexingConfig`.

---

## `indexer-fga-contracts/contract-doc-out-of-sync` — Important

**Pattern:** code changes the indexed payload, tag set, FGA `data` fields, or invite/application status
transitions, but the matching contract doc (`docs/indexer-contract.md`, `docs/fga-contract.md`,
`docs/invite-application-flows.md`) is not updated in the same PR. Includes: new `Tags()` entries, new
indexed fields, new object types, `omitempty`/optional fields documented as required, and HTTP-status
mismatches between docs and `wrapError`.

**Detect:** if the diff touches a `Tags()` method, an indexed struct (`internal/domain/model/committee_*.go`),
a `build*IndexingConfig`, an FGA message builder, or invite/application status strings, check whether the
corresponding `docs/*.md` is also in the diff. Flag mismatches in field-optionality, tag presence, object
types listed, or documented HTTP status vs `pkg/errors` type → `wrapError` mapping.

**Empirical citation:** PR #76 `docs/indexer-contract.md:157` (Copilot) — "The committee member indexer contract doesn't mention the newly added `project_uid`/`project_slug` fields or the new `project_uid:`/`project_slug:` tags". Recurs PR #70 `docs/indexer-contract.md:48` ("Several fields ... are marked as non-optional here, but the source struct ... uses `omitempty`/pointers"), PR #33 `internal/domain/model/committee_base.go:141` ("documentation updates are required when modifying the `Tags()` method"), PR #65 `docs/invite-application-flows.md:143` ("Docs say identity lookup failures return `422` ... but the implementation returns `errors.Validation`, which `wrapError` maps to `BadRequest` (HTTP 400)").

**Failure message:** Emitted-event behavior changed but the owned contract doc was not updated in the same change.

**Fix:** update `docs/indexer-contract.md` / `docs/fga-contract.md` / `docs/invite-application-flows.md` in the same PR; mark optional/`omitempty` fields as optional and keep documented HTTP status aligned with the `pkg/errors` type that `wrapError` maps.

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

**Fix:** reference the `pkg/constants` subject/bucket symbol; if a new subject/bucket is introduced, add it to `pkg/constants` and update `references/nats-messaging.md`.

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
