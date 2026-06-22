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
"lfx.committee-api.get_project"                   // resolve committee UID to owning project UID (pkg/api: GetCommitteeProjectRequest/Response)
```

### Inbound event subjects (consumed from other services)

```go
"lfx.mailing-list-api.committee_mailing_list.changed" // handled by internal/service/message_handler.go (updates has_mailing_list + re-index)
"lfx.invite-service.invite_accepted"                  // published by the invite service after it processes a self-serve acceptance (enriched event embedding the invite record); handled by HandleInviteAccepted (owned by lfx-v2-invite-service: inviteapi.InviteServiceAcceptedSubject)
```

### Self-consumed event subjects

These are published by this service and also subscribed by this service (queue
`lfx.committee-api.queue`, registered in `cmd/committee-api/service/providers.go`).

```go
"lfx.committee-api.committee.updated"             // emitted after committee base updates; consumed to re-sync denormalized member fields
"lfx.committee-api.committee_settings.updated"    // emitted after settings updates; consumed to drive the LFID settings-invite flow
"lfx.committee-api.committee_member.created"       // also self-consumed (re-index / role-notification paths)
"lfx.committee-api.committee_member.deleted"       // also self-consumed
"lfx.committee-api.committee_document.created"     // also self-consumed (document-upload notification emails to committee members)
"lfx.committee-api.committee_link.created"         // also self-consumed (link-added notification emails to committee members)
"lfx.committee-api.weekly_brief.generate_requested" // consumed by the durable weekly-brief generate consumer (NOT via the queue subscription)
```

### Outbound request/reply subjects (owned by peer services)

```go
"lfx.projects-api.get_name"                       // project name lookup
"lfx.projects-api.get_slug"                       // project slug lookup
"lfx.auth-service.email_to_sub"                   // invite/member email -> Auth0 sub lookup
"lfx.auth-service.user_emails.read"               // principal -> primary/alternate email lookup for self-service flows
"lfx.auth-service.user_metadata.read"             // principal -> profile metadata lookup
"lfx.invite-service.send_invite"                  // invite-service send for committee invites, non-LFID members, and LFID settings invites (see docs/invite-application-flows.md)
"lfx.email-service.send_email"                    // direct notification emails (role/document/link notifications; owned by lfx-v2-email-service: emailapi.SendEmailSubject)
```

### Outbound notification subjects

```go
"lfx.committee-api.committee.updated"             // committee changed (before/after)
"lfx.committee-api.committee_settings.updated"    // committee settings changed (before/after)
"lfx.committee-api.committee_member.created"
"lfx.committee-api.committee_member.updated"
"lfx.committee-api.committee_member.deleted"
"lfx.committee-api.committee_document.created"     // emitted by internal/service/document_writer.go after a document upload
"lfx.committee-api.committee_link.created"         // emitted by internal/service/link_writer.go after a link create
"lfx.committee-api.weekly_brief.generate_requested" // emitted by POST /committees/{uid}/weekly-briefs/generate after the brief is claimed
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

### KV buckets

- `committees`: committee base
- `committee-settings`: settings (gated by `auditor`)
- `committee-members`: committee membership
- `committee-invites`: invite state
- `committee-applications`: application state
- `committee-links`: committee links
- `committee-folders`: link folder grouping
- `committee-documents-metadata`: committee document metadata (constant `KVBucketNameCommitteeDocuments`; matches the helm chart's `committee_documents_metadata_kv_bucket`)
- `group-weekly-briefs`: working-group weekly brief drafts (key: brief UID; value: full brief JSON)
- `group-weekly-brief-uid-index`: maps `{committee_uid}.{window_yyyymmdd}` → brief UID
- `group-weekly-brief-throttle`: per-window regeneration throttle counts

All initialized in `internal/infrastructure/nats/client.go`. The base/settings
split and per-sub-resource bucketing follow the native service pattern.

### Object Stores

- `committee-documents`: uploaded committee document file bytes. Metadata stays
  in the `committee-documents-metadata` KV bucket.

### JetStream streams

- `committee-member-events` (constant `StreamNameCommitteeMemberEvents`):
  durable stream that captures `lfx.committee-api.committee_member.*`. Consumed
  by the `committee-service-total-members` durable consumer
  (`ConsumerNameTotalMembersSync`), which filters to created and deleted member
  events to keep `total_members` accurate.
- `weekly-brief-events` (constant `StreamNameWeeklyBriefEvents`): durable stream
  that captures `lfx.committee-api.weekly_brief.*`. Consumed by the
  `committee-service-weekly-brief-generate` durable consumer
  (`ConsumerNameWeeklyBriefGenerate`), which runs the async brief generation
  (source gather → LLM → finalize) after a generate is requested.

See `charts/lfx-v2-committee-service/templates/nats-streams.yaml` and
`charts/lfx-v2-committee-service/values.yaml` for chart wiring.
