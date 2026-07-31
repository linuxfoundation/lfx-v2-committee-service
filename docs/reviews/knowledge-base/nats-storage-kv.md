# NATS KV & Object Store Storage

Patterns specific to this service's NATS KV / Object Store adapters in
`internal/infrastructure/nats/`: optimistic-locking discipline, conflict mapping, committee-existence
guards before reads, secondary-index (lookup-key) reservation and rollback, and orphaned-object cleanup.
These are data-integrity patterns — cost-of-miss promotes them.

**Read when:** any file under `internal/infrastructure/nats/**`, `internal/service/*writer.go`,
`internal/service/*reader.go`, `internal/service/message_handler.go` (read-modify-write conflict retries,
and the `usernameMatches`/`emailMatches` normalization helpers),
`cmd/committee-api/service/committee_service.go` (handler-level existence
guards), `internal/infrastructure/mock/**` (mock semantics must mirror storage),
`pkg/constants/storage.go`, or `cmd/committee-cli/commands/sync/**` (secondary-index backfills).

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

**Pattern:** a `List*` handler reads a sub-resource collection from KV without first verifying the parent
committee exists, so a non-existent committee UID returns `200` + empty array instead of the documented
`404`. The repo convention is to call `GetBase` (committee existence check) before listing
links/documents/members/invites/applications.

**Detect:** in `cmd/committee-api/service/committee_service.go`, for each **`List*`** sub-resource handler,
confirm a `GetBase(ctx, uid)` (or equivalent existence check) precedes the storage list call.

**Single-resource `Get*` handlers are out of scope.** They legitimately rely on the sub-resource read's own
404 plus a committee-ownership check, so requiring a separate `GetBase` there produces false positives.

**Empirical citation:** PR #71 `cmd/committee-api/service/committee_service.go:1290` (Copilot) — "ListCommitteeDocuments does not verify the committee exists (unlike ListCommitteeLinks which calls GetBase first) ... an unknown committee UID will return 200 with an empty list, which conflicts with the API behavior implied elsewhere (and the OpenAPI 404 response)." Recurs PR #61 `committee_service.go:352/489` (ListInvites/ListApplications, same issue).

**Revised 2026-07-30 — scope narrowed, citations superseded.** The invariant is upheld, but all three cited
handlers (`ListInvites`, `ListApplications`, `ListCommitteeDocuments`) no longer exist. The two surviving
list handlers both guard, and are the current reference implementations:
`ListCommitteeLinks` (`committee_service.go:2199` → its `GetBase` existence check at `:2202`) and
`ListCommitteeLinkFolders` (`:2291` → `:2294`). **Corrected 2026-07-31:** these were previously cited as
`:2208`/`:2211` and `:2296`/`:2299`, which resolve to a `wrapError` return and a slice initialization — anchor
on the function name and its `GetBase` call, not on those numbers. The `Get*` exclusion above was added in the same pass. The PR #71/#61 threads are
retained as provenance.

**Failure message:** Sub-resource `List*` handler does not verify the committee exists first — non-existent UID returns 200 + empty instead of 404.

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

**Pattern:** a document delete removes the KV metadata but leaves the file behind in the
`committee-documents` Object Store. Separately, an Object Store blob written before its KV metadata record
is left orphaned when metadata creation fails **and nothing in the code says that was the decision**.

**Detect:** in `internal/service/document_writer.go`, `internal/infrastructure/nats/document_storage.go`,
and the mocks, check that delete paths remove **both** the metadata and the object-store file. For the
write path, flag orphaning only where it is *undocumented*.

**The existing metadata-failure orphan is an accepted trade-off, not a finding.**
`internal/service/document_writer.go:115-119` explains in code why the object is deliberately not rolled
back. Flagging it re-opens a decision the team wrote down; a reviewer that quotes this entry against that
block is wrong. The rule bites when a *new* Object Store write path orphans silently with no such note.

**Empirical citation:** PR #71 `internal/service/document_writer.go:128` (Copilot/CodeRabbit) — "If CreateDocumentMetadata fails after PutDocumentFile succeeds, the uploaded object is left orphaned in the object store ... Consider best-effort deleting the object-store entry on metadata failure." Recurs PR #71 `mock/document.go:100` ("Missing file cleanup in DeleteDocumentMetadata").

**Revised 2026-07-30 — narrowed.** The delete half holds and is verified at `document_storage.go:161` plus
`mock/document.go:98`. The metadata-failure half was rewritten because the behaviour it flagged became a
documented accepted trade-off in the code during the mining window; as originally written the entry fired
against that decision. The PR #71 threads are retained as provenance.

**Failure message:** Document delete leaves the object-store file behind, or a new Object Store write path orphans its blob on metadata failure with no documented rationale.

**Fix:** on document delete, remove both the metadata and the object-store file. On a new write path, either best-effort delete the object-store entry on metadata-create failure, or record in code why the orphan is accepted — as `document_writer.go:115-119` does.

---

## `nats-storage-kv/normalize-index-key-inputs` — Important

**Pattern:** a member/invite/application uniqueness key or identity key is built from raw email/username
without normalizing (`strings.ToLower(strings.TrimSpace(...))`), so case or whitespace variants produce
duplicate or non-matching keys. Email is normalized but username is left verbatim, or presence checks
(`hasUsername`/`hasEmail`) run against the raw string so `"   "` passes and is stored as empty.

**Scope extends past the key builders** to **every guard that compares a caller-supplied value against an
indexed one**. A builder that normalizes and a comparison that does not are the same defect: the comparison
misses the record the index would have found.

**Detect:** in `BuildIndexKey`, `committeeUserKey`, `userIdentityKey`, and payload-conversion presence
checks, verify both email AND username are `TrimSpace`'d (and email lower-cased) before keying or the
presence check. Then check any new or changed identity comparison the same way — a `strings.EqualFold`
without `TrimSpace` against an email or username is a finding. The helpers to reuse are `usernameMatches` /
`emailMatches` (`internal/service/message_handler.go:568-587`), which apply the index normalization.

**Empirical citation:** PR #16 `internal/domain/model/committee_member.go:83` (CodeRabbit) — "Normalize inputs when building uniqueness key to prevent case/whitespace dupes". Recurs PR #92 `message_handler.go:1015` (dealako, "`committeeUserKey` trims email but not username") and PR #92 `committee_service_response.go:679` ("Whitespace-only username/email passes presence check, stored as empty").

**Revised 2026-07-30 — scope extended.** All builders normalize in `main@bd39fe9`, including the new
`BuildUsernameIndexKey:125`, and the PR #140 fix is verified: `emailChanged` is now derived from hash
comparison rather than raw strings. PR #161 added the `usernameMatches`/`emailMatches` helpers named above,
which reuse the index normalization.

**Residual live violations** — `strings.EqualFold` **without** `TrimSpace` in
`cmd/committee-api/service/committee_service.go` at `AcceptInvite:952`, `DeclineInvite:1043`, and
`LeaveCommittee:1345` (verified at `main@bd39fe9`). Copilot flagged this shape on PR #150 and no fix landed,
so it is current. This is what the comparison-guard extension exists to catch.

**Corrected 2026-07-31.** These were previously cited as `:954`, `:1045`, `:1347` — `ec86a8f` line numbers,
not `bd39fe9` as the entry claimed. The file was not named either, so the numbers could not be checked
without guessing the file. Function names are given first here because they survive line drift; the numbers
are the convenience, not the anchor.

**Failure message:** Uniqueness/identity key, presence check, or identity comparison uses un-normalized email/username — case/whitespace variants dupe or mismatch.

**Fix:** apply `strings.TrimSpace` to username and `strings.ToLower(strings.TrimSpace(...))` to email before keying; trim before the presence check so whitespace-only values are treated as absent; and normalize both sides of an identity comparison rather than relying on a bare `EqualFold`.

**On the helpers — do not prescribe a call that will not compile.** `usernameMatches` and `emailMatches` are the
reference implementations of that normalization, but they are **unexported**, in `internal/service`
(`message_handler.go:570` and `:580`). The live violations above are in `cmd/committee-api/service` — the same
package *name*, a different package — so they cannot call them. Within `internal/service`, call them. From any
other package, either apply the same normalization inline or promote a shared exported helper; cite them as the
normalization to match, never as a function the caller can already reach.

---

## `nats-storage-kv/new-secondary-index-needs-backfill-and-cleanup` — Critical

**Pattern:** a new persistent secondary index is added to the members bucket — a `KVLookup*Prefix` constant
plus a `Build*IndexKey` method and a write-path call — but only one half of its lifecycle ships. Either
deleted records keep their index key forever, or records that existed before the deploy are never indexed at
all, so any reader of the index silently sees a partial view.

**Detect:** the diff adds a `KVLookupMembersBy*Prefix` constant to `pkg/constants/storage.go`, or a
`Build*IndexKey` method on a member model. Require all three:

1. the key is appended to `indicesToDelete` in `DeleteMember`
   (`internal/service/committee_member_writer.go:805-822` is the block — every other index has an entry
   there);
2. a backfill exists at `cmd/committee-cli/commands/sync/members_by_*_index.go` and is registered in
   `cmd/committee-cli/commands/sync/sync.go`;
3. the change states that the backfill runs **before** the consumer that reads the index.

Any of the three missing is a finding. A read path that consults the new index while (2) is absent is the
critical case.

**Empirical citation:** PR #161 (`copilot-pull-request-reviewer`), two threads on
`internal/service/committee_member_writer.go`.

- Thread `r3659896585` at `:307` — "Adding this persistent secondary key also requires deletion cleanup.
  `DeleteMember` currently collects only uniqueness, committee, organization, and email keys
  (`committee_member_writer.go:797-817`), so every deleted member with a username leaves an orphaned
  username-index key indefinitely." Fixed in `b74396c`; verified at `main@bd39fe9`
  `committee_member_writer.go:819-822` with `TestDeleteMember_IndexKeyIncluded`
  (`committee_member_writer_test.go:798`).
- Thread `r3659843291`, same file — "This only indexes members created after deployment … Existing seats
  with unchanged usernames therefore have no key, so `ListMembersByUsername` returns no matches and the
  primary scrub behavior silently misses them. Add an idempotent username-index backfill command and
  deployment sequencing, analogous to `members-by-email-index`." Fixed in `a9c62dc`; verified at
  `cmd/committee-cli/commands/sync/members_by_username_index.go`, registered as
  `"members-by-username-index"` in `sync.go:25`.

**Why it earns a place:** the repo carries four of these indexes — by committee, by email, by organization,
by username — and PR #161 shows both obligations being missed on the newest one, then acted on twice in the
same PR. Cost of miss is permanent: an incomplete index makes the user-deletion scrub a silent blind spot,
and nothing fails loudly.

**Failure message:** New secondary index added without delete-path cleanup and/or a backfill command — existing records stay unindexed and deleted records leak orphaned keys.

**Fix:** append the key to `indicesToDelete` in `DeleteMember` behind a non-empty guard, mirroring the email index; add a `members_by_<attr>_index.go` backfill with `--dry-run` defaulting to true and register it in `sync.go`; and state the backfill-before-consumer ordering in the change.
