# NATS KV & Object Store Storage

Patterns specific to this service's NATS KV / Object Store adapters in
`internal/infrastructure/nats/`: optimistic-locking discipline, conflict mapping, committee-existence
guards before reads, secondary-index (lookup-key) reservation and rollback, and orphaned-object cleanup.
These are data-integrity patterns — cost-of-miss promotes them.

**Read when:** any file under `internal/infrastructure/nats/**`, `internal/service/*writer.go`,
`internal/service/*reader.go`, `cmd/committee-api/service/committee_service.go` (handler-level existence
guards), or `internal/infrastructure/mock/**` (mock semantics must mirror storage).

---

## `nats-storage-kv/delete-must-use-revision` — Important

**Pattern:** a KV delete uses `Purge` (or ignores the passed-in `revision`) instead of
`Delete(ctx, uid, jetstream.LastRevision(revision))`. This drops optimistic locking and can delete the
wrong version under concurrent updates — inconsistent with the rest of the repo's KV deletes. Also: an
unused `revision` parameter on a delete method is a tell that locking was dropped.

**Detect:** in `internal/infrastructure/nats/*_storage.go`, find `Delete*`/`*Folder`/`*Link` methods. Flag
`.Purge(` used for the primary record delete, or a `revision` parameter that the method never passes to
`jetstream.LastRevision(...)`.

**Empirical citation:** PR #68 `internal/infrastructure/nats/link_storage.go:84` (Copilot) — "DeleteLink ignores the passed-in revision and uses KV Purge, which bypasses the optimistic-locking pattern used elsewhere (e.g., storage.Delete uses jetstream.LastRevision)." Recurs `link_storage.go:164` (DeleteLinkFolder) and PR #68 CodeRabbit `link_storage.go:91` ("Unused `revision` parameter in `DeleteLink`").

**Failure message:** KV delete uses `Purge` / ignores `revision` — bypasses the optimistic-locking pattern used elsewhere in this repo.

**Fix:** delete the primary record with `Delete(ctx, uid, jetstream.LastRevision(revision))`; reserve `Purge` for best-effort lookup-key cleanup only; handle `jetstream.ErrKeyNotFound` → `errs.NewNotFound`.

---

## `nats-storage-kv/conflict-mapping` — Important

**Pattern:** a JetStream "wrong last sequence" / revision-mismatch error is returned as a generic 500
(`Unexpected`) instead of `409 Conflict`, or `ErrKeyNotFound` is mapped to `Conflict` instead of `NotFound`.
Also: a read-modify-write path (`Get` → mutate → `Update(revision)`) on an event-handler code path does not
retry on conflict, so a concurrent write silently drops the update.

**Detect:** in `*_storage.go`, check that revision-conflict errors (`jetstream.JSErrCodeStreamWrongLastSequence`)
map to `errs.NewConflict` and `jetstream.ErrKeyNotFound` maps to `errs.NewNotFound`. For event-handler
read-modify-write paths (`message_handler.go`, `UpdateHasMailingList`), check for a bounded retry-on-conflict loop.

**Empirical citation:** PR #19 `internal/infrastructure/nats/storage.go:311` (CodeRabbit) — "Return 409 Conflict on revision-mismatch ('wrong last sequence') instead of 500". Recurs PR #71 `document_storage.go:152` ("Map `ErrKeyNotFound` to `NotFound` instead of `Conflict`"), PR #74 `storage.go:231` (Copilot, `UpdateHasMailingList` missing conflict retry), PR #92 `message_handler.go:942` (dealako, silent drop on optimistic-lock conflict in `HandleInviteAccepted`).

**Failure message:** KV revision-conflict / not-found mapped to the wrong error type, or event-handler RMW path lacks conflict retry.

**Fix:** map `JSErrCodeStreamWrongLastSequence` → `errs.NewConflict` (409), `ErrKeyNotFound` → `errs.NewNotFound` (404); on event-handler RMW paths add a bounded retry-on-conflict loop (re-`Get`, re-apply, re-`Update`) or return a non-nil error so NATS redelivers.

---

## `nats-storage-kv/missing-existence-guard` — Important

**Pattern:** a list/get handler reads a sub-resource collection from KV without first verifying the parent
committee exists, so a non-existent committee UID returns `200` + empty array instead of the documented
`404`. The repo convention is to call `GetBase` (committee existence check) before listing
links/documents/members/invites/applications.

**Detect:** in `cmd/committee-api/service/committee_service.go`, for each `List*`/`Get*` sub-resource
handler, confirm a `GetBase(ctx, uid)` (or equivalent existence check) precedes the storage list call.

**Empirical citation:** PR #71 `cmd/committee-api/service/committee_service.go:1290` (Copilot) — "ListCommitteeDocuments does not verify the committee exists (unlike ListCommitteeLinks which calls GetBase first) ... an unknown committee UID will return 200 with an empty list, which conflicts with the API behavior implied elsewhere (and the OpenAPI 404 response)." Recurs PR #61 `committee_service.go:352/489` (ListInvites/ListApplications, same issue).

**Failure message:** Sub-resource list/get handler does not verify the committee exists first — non-existent UID returns 200 + empty instead of 404.

**Fix:** call `GetBase(ctx, committeeUID)` (or a dedicated exists check) at the top of the handler and return `NotFound` when missing, matching `ListCommitteeLinks`.

---

## `nats-storage-kv/lookup-key-reservation-rollback` — Important

**Pattern:** a uniqueness lookup key (secondary index) is reserved via `Create` before writing the primary
record, but if the primary write fails the reserved key is left behind, so future creates incorrectly
conflict. The orchestrator must capture the returned lookup key and clean it up on failure (the
`committeeWriterOrchestrator` rollback pattern). Mirror this in mocks: mock `Unique*` must return the
lookup key (not the existing UID) on conflict so rollback logic can delete it.

**Detect:** in `*_writer.go` create paths that call `Unique*`/reserve a lookup key, verify the returned key
is captured and deleted on the failure branch. In `internal/infrastructure/mock/*.go`, verify `Unique*`
returns the lookup/index key consistently (including the conflict case), matching the NATS implementation.

**Empirical citation:** PR #68 `internal/service/link_writer.go:148` (Copilot) — "CreateLinkFolder reserves the unique folder-name key before writing the folder record, but if CreateLinkFolder fails after UniqueLinkFolderName succeeds, the reserved lookup key is left behind and future creates will incorrectly conflict. Capture the returned lookup key and clean it up on failure (similar to committeeWriterOrchestrator rollback behavior)." Recurs PR #61 `mock/committee.go:755/806` ("Mock UniqueInvite returns existing.UID on conflict, but the NATS implementation returns the lookup key ... Returning the UID here can break rollback/cleanup logic").

**Failure message:** Reserved uniqueness lookup key is not rolled back on primary-write failure (or mock `Unique*` returns the wrong value on conflict).

**Fix:** capture the lookup key returned by `Unique*` and delete it on the create-failure branch; make mock `Unique*` return the same lookup/index key as the NATS adapter, including on conflict.

---

## `nats-storage-kv/orphaned-object-on-metadata-failure` — Important

**Pattern:** an Object Store blob is written (`PutDocumentFile`) before the KV metadata record, but if
metadata creation fails the uploaded object is left orphaned in the `committee-documents` Object Store.
Document deletes must also remove the file from the Object Store, not just the metadata.

**Detect:** in `internal/service/document_writer.go` and `internal/infrastructure/nats/document_storage.go`
/ mocks, check that an Object Store write is paired with best-effort cleanup on metadata-write failure, and
that delete paths remove both the metadata and the object-store file.

**Empirical citation:** PR #71 `internal/service/document_writer.go:128` (Copilot/CodeRabbit) — "If CreateDocumentMetadata fails after PutDocumentFile succeeds, the uploaded object is left orphaned in the object store ... Consider best-effort deleting the object-store entry on metadata failure." Recurs PR #71 `mock/document.go:100` ("Missing file cleanup in DeleteDocumentMetadata").

**Failure message:** Object Store blob can be orphaned when metadata write fails (or delete leaves the file behind).

**Fix:** on metadata-create failure, best-effort delete the object-store entry; on document delete, remove both metadata and the object-store file.

---

## `nats-storage-kv/normalize-index-key-inputs` — Important

**Pattern:** a member/invite/application uniqueness key or identity key is built from raw email/username
without normalizing (`strings.ToLower(strings.TrimSpace(...))`), so case or whitespace variants produce
duplicate or non-matching keys. Email is normalized but username is left verbatim, or presence checks
(`hasUsername`/`hasEmail`) run against the raw string so `"   "` passes and is stored as empty.

**Detect:** in `BuildIndexKey`, `committeeUserKey`, `userIdentityKey`, and payload-conversion presence
checks, verify both email AND username are `TrimSpace`'d (and email lower-cased) before keying or the
presence check.

**Empirical citation:** PR #16 `internal/domain/model/committee_member.go:83` (CodeRabbit) — "Normalize inputs when building uniqueness key to prevent case/whitespace dupes". Recurs PR #92 `message_handler.go:1015` (dealako, "`committeeUserKey` trims email but not username") and PR #92 `committee_service_response.go:679` ("Whitespace-only username/email passes presence check, stored as empty").

**Failure message:** Uniqueness/identity key or presence check uses un-normalized email/username — case/whitespace variants dupe or mismatch.

**Fix:** apply `strings.TrimSpace` to username and `strings.ToLower(strings.TrimSpace(...))` to email before keying; trim before the presence check so whitespace-only values are treated as absent.
