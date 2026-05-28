<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# NATS Messaging (committee-service)

Repo-local inventory of NATS subjects, queue groups, KV buckets, Object Stores, and streams owned or consumed by this service. Coding rules for NATS live in SKILL.md; platform ownership of NATS/KV lives in `lfx-skills:lfx-platform-architecture`.

## Committee-service local specifics

### Queue group

```go
"lfx.committee-api.queue"                         // shared queue group for this service
```

### Inbound RPC subjects (handled by this service)

```go
"lfx.committee-api.get_name"                      // get committee name by UID
"lfx.committee-api.list_members"                  // list committee members
```

### Inbound event subjects (consumed from other services)

```go
"lfx.mailing-list-api.committee_mailing_list.changed" // handled by internal/service/message_handler.go (updates has_mailing_list + re-index)
```

### Self-consumed event subjects

```go
"lfx.committee-api.committee.updated"             // emitted after committee base updates; consumed to re-sync denormalized member fields
```

### Outbound request/reply subjects (owned by peer services)

```go
"lfx.projects-api.get_name"                       // project name lookup
"lfx.projects-api.get_slug"                       // project slug lookup
"lfx.auth-service.email_to_sub"                   // invite/member email -> Auth0 sub lookup
"lfx.auth-service.user_emails.read"               // principal -> primary/alternate email lookup for self-service flows
```

### Outbound notification subjects

```go
"lfx.committee-api.committee.updated"             // committee changed (before/after)
"lfx.committee-api.committee_member.created"
"lfx.committee-api.committee_member.updated"
"lfx.committee-api.committee_member.deleted"
```

### Indexer subjects (see `docs/indexer-contract.md`)

```go
"lfx.index.committee"
"lfx.index.committee_settings"
"lfx.index.committee_member"
"lfx.index.committee_invite"
"lfx.index.committee_application"
"lfx.index.committee_link"
"lfx.index.committee_link_folder"
"lfx.index.committee_document"
```

### FGA subjects (see `docs/fga-contract.md`)

```go
"lfx.fga-sync.update_access"
"lfx.fga-sync.delete_access"
"lfx.fga-sync.member_put"
"lfx.fga-sync.member_remove"
```

### KV buckets (one per indexed sub-resource)

- `committees`: committee base
- `committee-settings`: settings (gated by `auditor`)
- `committee-members`: committee membership
- `committee-invites`: invite state
- `committee-applications`: application state
- `committee-links`: committee links
- `committee-folders`: link folder grouping
- `committee-documents-metadata`: committee document metadata (constant `KVBucketNameCommitteeDocuments`; matches the helm chart's `committee_documents_metadata_kv_bucket`)

All initialized in `internal/infrastructure/nats/client.go`. The base/settings
split and per-sub-resource bucketing follow the native service pattern.

### Object Stores

- `committee-documents`: uploaded committee document file bytes. Metadata stays
  in the `committee-documents-metadata` KV bucket.

### JetStream streams

- `committee-member-events` (constant `StreamNameCommitteeMemberEvents`):
  durable stream that captures `lfx.committee-api.committee_member.*`. Consumed
  by the `committee-service-total-members` durable consumer, which filters to
  created and deleted member events to keep `total_members` accurate. See
  `charts/lfx-v2-committee-service/templates/nats-streams.yaml` and
  `charts/lfx-v2-committee-service/values.yaml` for chart wiring.
