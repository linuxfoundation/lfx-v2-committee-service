<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# NATS KV and secondary-index patterns

Local empirical patterns for this service's KV adapters and the lookup-key
secondary indexes layered on the `committee-members` bucket.

**Read when** the patch touches `internal/infrastructure/nats/**`,
`internal/service/*writer.go`, `internal/service/*reader.go`,
`pkg/constants/storage.go`, `internal/infrastructure/mock/**`, or
`cmd/committee-cli/commands/sync/**`.

---

## `nats-storage-kv/new-secondary-index-needs-backfill-and-cleanup` — Critical

**Pattern:** a new persistent secondary index is added to the members bucket —
a `KVLookup*Prefix` constant plus a `Build*IndexKey` method and a write-path
call — but only one half of its lifecycle ships. Either deleted records keep
their index key forever, or records that existed before the deploy are never
indexed at all, so any reader of the index silently sees a partial view.

**Detect:** the diff adds a `KVLookupMembersBy*Prefix` constant to
`pkg/constants/storage.go`, or a `Build*IndexKey` method on a member model.
Require all three:

1. the key is appended to `indicesToDelete` in `DeleteMember`
   (`internal/service/committee_member_writer.go:805-822` is the block —
   every other index has an entry there);
2. a backfill exists at `cmd/committee-cli/commands/sync/members_by_*_index.go`
   and is registered in `cmd/committee-cli/commands/sync/sync.go`;
3. the change says the backfill runs **before** the consumer that reads the
   index.

Any of the three missing is a finding. A read path that consults the new index
while (2) is absent is the critical case.

**Evidence:** `copilot-pull-request-reviewer`, PR #161.

- Thread `r3659896585`, on `internal/service/committee_member_writer.go:307`:
  *"Adding this persistent secondary key also requires deletion cleanup.
  `DeleteMember` currently collects only uniqueness, committee, organization,
  and email keys (`committee_member_writer.go:797-817`), so every deleted
  member with a username leaves an orphaned username-index key indefinitely."*
  Fixed in `b74396c`. Verified in `main@bd39fe9` at
  `internal/service/committee_member_writer.go:819-822`, with
  `TestDeleteMember_IndexKeyIncluded`
  (`internal/service/committee_member_writer_test.go:798`).
- Thread `r3659843291`, same file: *"This only indexes members created after
  deployment … Existing seats with unchanged usernames therefore have no key,
  so `ListMembersByUsername` returns no matches and the primary scrub behavior
  silently misses them. Add an idempotent username-index backfill command and
  deployment sequencing, analogous to `members-by-email-index`."* Fixed in
  `a9c62dc`. Verified at
  `cmd/committee-cli/commands/sync/members_by_username_index.go`, registered as
  `"members-by-username-index"` in `sync.go:25`.

**Why it earns a place:** the repo now carries four of these indexes — by
committee, by email, by organization, by username — and PR #161 shows both
obligations being missed on the newest one, then acted on twice in the same PR.
Cost of miss is permanent: an incomplete index makes the user-deletion scrub a
silent blind spot, and nothing fails loudly.

**Failure message:** New secondary index added without delete-path cleanup
and/or a backfill command — existing records stay unindexed and deleted records
leak orphaned keys.

**Fix:** append the key to `indicesToDelete` in `DeleteMember` behind a
non-empty guard, mirroring the email index; add a
`members_by_<attr>_index.go` backfill with `--dry-run` defaulting to true and
register it in `sync.go`; and state the backfill-before-consumer ordering in
the change.
