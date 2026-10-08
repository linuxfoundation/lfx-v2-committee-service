# NATS Storage Reference

This service stores all state in NATS JetStream: Key-Value (KV) buckets, one Object Store, and three
streams. This document is the single reference for what exists, how keys are shaped, and which
secondary indexes are maintained.

Sources of truth:

- Bucket, prefix, stream, and consumer names: `pkg/constants/storage.go`
- Bucket handle binding at startup (buckets are created by the chart, not the service): `internal/infrastructure/nats/client.go`
- Provisioning parameters (history, size, replicas): `charts/lfx-v2-committee-service/values.yaml`
- Index maintenance: `internal/infrastructure/nats/storage.go`, `internal/service/committee_member_writer.go`

## KV buckets

All buckets are provisioned by the Helm chart (`creation: true`, `keep: true` by default). With the
chart defaults every bucket below uses `file` storage, `history: 20`, `maxValueSize` 10MB,
`maxBytes` 1GB, and `replicas: 1`. The values are overridable per environment in `lfx-v2-argocd`,
so confirm against the live bucket (`nats kv status <bucket>`) during an incident.

| Bucket | Primary key | Value | Holds |
|---|---|---|---|
| `committees` | `<committee_uid>` | `CommitteeBase` JSON | Committee records, plus unique-name lookup keys (see below) |
| `committee-settings` | `<committee_uid>` | `CommitteeSettings` JSON | Per-committee settings (same UID as the committee) |
| `committee-members` | `<member_uid>` | `CommitteeMember` JSON | Members, plus unique and secondary-index keys |
| `committee-invites` | `<invite_uid>` | `CommitteeInvite` JSON | Invites, plus a unique lookup key |
| `committee-applications` | `<application_uid>` | `CommitteeApplication` JSON | Applications, plus a unique lookup key |
| `committee-links` | `<link_uid>` | `CommitteeLink` JSON | Links |
| `committee-folders` | `<folder_uid>` | `CommitteeLinkFolder` JSON | Link folders, plus a unique lookup key |
| `committee-documents-metadata` | `<document_uid>` | `CommitteeDocument` JSON | Document metadata, plus a unique lookup key. File bytes live in the Object Store. |
| `group-weekly-briefs` | `<brief_uid>` | `GroupWeeklyBrief` JSON | Full weekly briefs |
| `group-weekly-brief-uid-index` | `<committee_uid>.<yyyymmdd>` | `<brief_uid>` | Maps a (committee, window start) pair to its brief |
| `group-weekly-brief-throttle` | `<committee_uid>.<yyyymmdd>` | throttle counter JSON | Per-window generation and regeneration counters. `Claim` reads the counters and must write the increment successfully before it saves the brief, so a throttle-bucket failure can block generation. Only the read endpoint treats throttle data as optional. |

Weekly-brief keys pass through `sanitizeKVKey`, which rewrites `/`, `:`, and spaces to `.`.

## Object Store

| Store | Key | Holds |
|---|---|---|
| `committee-documents` | `<document_uid>` | The uploaded document file. Metadata is in `committee-documents-metadata`. Chart default: `file` storage, 10GB `maxBytes`. |

## Unique-constraint lookup keys

These keys live in the same bucket as the record they protect. They are created with KV `Create`
(fails if present) so a duplicate write returns a conflict. The value is the owning record's UID.
`<hash>` is a lowercase SHA-256 hex digest of the normalized inputs joined with `|`.

| Bucket | Key | Hash input | Enforces |
|---|---|---|---|
| `committees` | `lookup/committees/<hash>` | `project_uid\|name` | One committee name per project |
| `committees` | `lookup/committee-sso-groups/<sso_group_name>` | (raw SSO group name) | Unique SSO group name |
| `committee-members` | `lookup/committee-members/<hash>` | `committee_uid\|email` (trimmed, lowercased) | One seat per email per committee |
| `committee-invites` | `lookup/committee-invites/<hash>` | `committee_uid\|invitee_email` (trimmed, lowercased) | One invite per email per committee |
| `committee-applications` | `lookup/committee-applications/<hash>` | `committee_uid\|applicant_email` (trimmed, lowercased) | One application per applicant per committee |
| `committee-folders` | `lookup/committee-folders/<hash>` | `committee_uid\|folder name` | Unique folder name per committee |
| `committee-documents-metadata` | `lookup/committee-documents/<hash>` | `committee_uid\|document name` | Unique document name per committee |

Constants `KVLookupSettingsInvitePrefix` (`lookup/committee-settings-invite/<invite_uid>`) and
`KVSlugPrefix` (`slug/`) are defined, but no current write path creates keys with them. The `slug/`
prefix is only skipped when scanning for records.

## Member secondary indexes

All member secondary indexes live in the `committee-members` bucket. The key is
`lookup/<index>/<segment>.<member_uid>` and the value is `<member_uid>`. The dot-separated layout lets
callers do a server-side filtered scan with `ListKeysFiltered("<prefix>/<segment>.*")` instead of
reading the whole bucket. Hashes are lowercase SHA-256 hex of the trimmed, lowercased value, which
keeps keys dot-free and avoids storing raw emails or usernames in key names.

| Prefix | Segment | Semantics | Written on | Backfill / repair |
|---|---|---|---|---|
| `lookup/committee-members-by-committee/` | `<committee_uid>` | All members of a committee (list members) | Member create | `committee-cli sync members-by-committee-index` |
| `lookup/committee-members-by-organization/` | `<org_sfid>` (`organization.id`) | All seats held by an organization (Org Lens, LFXV2-1865) | Member create; re-keyed when `organization.id` changes | `committee-cli sync members-by-organization-index` |
| `lookup/committee-members-by-email/` | `<email_hash>` | All seats held by an email; used to react to alternate-email changes and user merges (LFXV2-2521) | Member create; re-keyed when email changes | `committee-cli sync members-by-email-index` |
| `lookup/committee-members-by-username/` | `<username_hash>` | All seats held by an LFID username; used by the user-deleted scrub flow (LFXV2-2645) | Member create when a username is set; re-keyed when it changes | `committee-cli sync members-by-username-index` |

Writers: `IndexMemberBy*` in `internal/infrastructure/nats/storage.go`, called from
`internal/service/committee_member_writer.go` (create, update, delete). On update, stale keys are
removed after the new ones are written; on delete, all of the member's index keys are removed. See
[`cmd/committee-cli/README.md`](../cmd/committee-cli/README.md) for the `sync` flags. `--dry-run` defaults differ per command (for example `members-by-committee-index` defaults to
`false`), so check each command's flags before running.

## JetStream streams

| Stream | Subjects | Retention | Durable consumer |
|---|---|---|---|
| `committee-member-events` | `lfx.committee-api.committee_member.*` | limits, 24h `maxAge` | `committee-service-total-members` (keeps `total_members` accurate) |
| `weekly-brief-events` | `lfx.committee-api.weekly_brief.*` | limits, 24h `maxAge` | `committee-service-weekly-brief-generate` (async brief generation) |
| `user-email-events` | `lfx.user-email.*` | limits, 24h `maxAge` | `committee-service-user-email-sync` (username re-resolution) |

## Incident tips

- **Scanning a bucket:** `ListKeys` on `committees`, `committee-members`, `committee-invites`,
  `committee-links`, `committee-folders`, and `committee-documents-metadata` also returns `lookup/`
  keys. The service filters those out (and `slug/` in `committees`); do the same in scripts and
  one-off repairs, or you will try to unmarshal a UID string as a record.
- **"Member exists but is missing from a list or lookup":** the secondary index is probably absent.
  Check with `nats kv ls committee-members | grep committee-members-by-`, then run the matching `sync members-by-*-index` command with
  `--dry-run` first.
- **Unexpected 409 on create:** a unique lookup key outlived its record (for example, a failed
  rollback). Find the key from the table above, confirm its value UID no longer exists in the
  primary bucket, then purge the key.
- **Stale index after an email, org, or username change:** the update path deletes the old key
  after writing the new one, so a leftover key points at a member that no longer matches its
  segment. Confirm against the member record, then purge it.
- **Weekly brief missing for a window:** look up `<committee_uid>.<yyyymmdd>` in
  `group-weekly-brief-uid-index`, then fetch the UID from `group-weekly-briefs`. If the index key is
  missing but the brief exists in `group-weekly-briefs`, the API cannot find it. `committee-cli sync backfill-weekly-brief-index` does not fix this: it walks the existing index keys and republishes search updates, so it never rebuilds the KV index. Restore the mapping by writing `<committee_uid>.<yyyymmdd>` -> `<brief_uid>` into `group-weekly-brief-uid-index`, then run the backfill to refresh search.
- **Do not hand-edit** primary records without going through the CLI or API; updates use optimistic
  concurrency (KV revision), and the indexer and FGA events are emitted by the service, not by the
  bucket.
