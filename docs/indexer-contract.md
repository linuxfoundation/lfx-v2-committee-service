# Indexer Contract — Committee Service

This document is the authoritative reference for all data the committee service sends to the indexer service, which makes resources searchable via the [query service](https://github.com/linuxfoundation/lfx-v2-query-service).

**Update this document in the same PR as any change to indexer message construction.**

---

## Resource Types

- [Committee](#committee)
- [Committee Settings](#committee-settings)
- [Committee Member](#committee-member)
- [Committee Invite](#committee-invite)
- [Committee Application](#committee-application)
- [Committee Link](#committee-link)
- [Committee Link Folder](#committee-link-folder)

---

## Committee

**Object type:** `committee`

**NATS subject:** `lfx.index.committee`

**Source struct:** `internal/domain/model/committee_base.go` — `CommitteeBase`

**Indexed on:** create, update, delete of a committee.

### Data Schema

These fields are indexed and queryable via `filters` or `cel_filter` in the query service.

| Field | Type | Description |
|---|---|---|
| `uid` | string | Committee unique identifier |
| `project_uid` | string | UID of the owning project |
| `project_name` | string (optional) | Name of the owning project |
| `project_slug` | string (optional) | Slug of the owning project |
| `name` | string | Committee name |
| `display_name` | string (optional) | Display name (may differ from name) |
| `category` | string | Committee category (e.g., `Board`, `TSC`) |
| `description` | string (optional) | Committee description |
| `website` | string (optional) | Committee website URL |
| `mailing_list` | string (optional) | Mailing list address |
| `chat_channel` | string (optional) | Chat channel identifier |
| `enable_voting` | bool | Whether voting is enabled |
| `sso_group_enabled` | bool | Whether SSO group is enabled |
| `sso_group_name` | string (optional) | SSO group name |
| `requires_review` | bool | Whether membership requires review |
| `public` | bool | Whether the committee is publicly visible |
| `join_mode` | string (optional) | How members can join |
| `calendar.public` | bool (optional) | Whether the committee calendar is public; omitted when the `calendar` object is empty |
| `parent_uid` | string (optional) | UID of the parent committee (if nested) |
| `total_members` | int | Current total member count |
| `total_voting_repos` | int | Current total voting repos count |
| `created_at` | timestamp | Creation time (RFC3339) |
| `updated_at` | timestamp | Last update time (RFC3339) |

### Tags

| Tag Format | Example | Purpose |
|---|---|---|
| `{uid}` | `061a110a-7c38-4cd3-bfcf-fc8511a37f35` | Direct lookup by UID |
| `committee_uid:{uid}` | `committee_uid:061a110a-7c38-4cd3-bfcf-fc8511a37f35` | Namespaced lookup by UID |
| `project_uid:{value}` | `project_uid:cbef1ed5-17dc-4a50-84e2-6cddd70f6878` | Find committees for a project |
| `project_slug:{value}` | `project_slug:test-project-slug-1` | Find committees by project slug |
| `parent_uid:{value}` | `parent_uid:9493eae5-cd73-4c4a-b28f-3b8ec5280f6c` | Find child committees of a parent |
| `category:{value}` | `category:Board` | Find committees by category |

### Access Control (IndexingConfig)

| Field | Value |
|---|---|
| `access_check_object` | `committee:{uid}` |
| `access_check_relation` | `viewer` |
| `history_check_object` | `committee:{uid}` |
| `history_check_relation` | `auditor` |

### Search Behavior

| Field | Value |
|---|---|
| `fulltext` | `name`, `display_name`, `description` |
| `name_and_aliases` | `name`, `display_name` (deduplicated) |
| `sort_name` | `name` |
| `public` | set from `committee.public` |

### Parent References

| Ref | Condition |
|---|---|
| `project:{project_uid}` | Always set |
| `committee:{parent_uid}` | Only when `parent_uid` is set |

---

## Committee Settings

**Object type:** `committee_settings`

**NATS subject:** `lfx.index.committee_settings`

**Source struct:** `internal/domain/model/committee_settings.go` — `CommitteeSettings`

**Indexed on:** create, update, delete of committee settings. Settings share the same UID as their parent committee.

### Data Schema

| Field | Type | Description |
|---|---|---|
| `uid` | string | Committee UID (same as the parent committee) |
| `business_email_required` | bool | Whether a business email is required to join |
| `show_meeting_attendees` | bool | Whether meeting attendees are visible |
| `member_visibility` | string | Who can see members |
| `last_reviewed_at` | string (optional) | RFC3339 timestamp of the last membership review |
| `last_reviewed_by` | string (optional) | UID of who performed the last review |
| `writers` | []object | Users with write access. Each object has `avatar` (string), `email` (string), `name` (string), `username` (string — holds the user ID / sub value) |
| `auditors` | []object | Users with audit access. Each object has `avatar` (string), `email` (string), `name` (string), `username` (string — holds the user ID / sub value) |
| `created_at` | timestamp | Creation time (RFC3339) |
| `updated_at` | timestamp | Last update time (RFC3339) |

### Tags

Same tag set as the parent [Committee](#committee).

### Access Control (IndexingConfig)

| Field | Value |
|---|---|
| `access_check_object` | `committee_settings:{uid}` |
| `access_check_relation` | `auditor` |
| `history_check_object` | `committee_settings:{uid}` |
| `history_check_relation` | `auditor` |

### Search Behavior

| Field | Value |
|---|---|
| `fulltext` | _(none)_ |
| `name_and_aliases` | _(none)_ |
| `sort_name` | _(none)_ |
| `public` | value of parent committee's `public` field |

### Parent References

_(none)_

---

## Committee Member

**Object type:** `committee_member`

**NATS subject:** `lfx.index.committee_member`

**Source struct:** `internal/domain/model/committee_member.go` — `CommitteeMember`

**Indexed on:** create, update, delete of a committee member.

### Data Schema

| Field | Type | Description |
|---|---|---|
| `uid` | string | Member unique identifier |
| `committee_uid` | string | UID of the committee this member belongs to |
| `committee_name` | string | Name of the committee |
| `committee_category` | string | Category of the committee |
| `username` | string | Member's username |
| `email` | string | Member's email address |
| `first_name` | string | Member's first name |
| `last_name` | string | Member's last name |
| `job_title` | string (optional) | Member's job title |
| `linkedin_profile` | string (optional) | Member's LinkedIn profile URL |
| `appointed_by` | string | Who appointed this member |
| `status` | string | Membership status |
| `role.name` | string | Role name within the committee |
| `role.start_date` | string (optional) | Role start date |
| `role.end_date` | string (optional) | Role end date |
| `voting.status` | string | Voting status (e.g., `Voting Rep`, `Non-Voting`) |
| `voting.start_date` | string (optional) | Voting eligibility start date |
| `voting.end_date` | string (optional) | Voting eligibility end date |
| `organization.id` | string (optional) | Member's organization ID |
| `organization.name` | string | Member's organization name |
| `organization.website` | string (optional) | Member's organization website |
| `created_at` | timestamp | Creation time (RFC3339) |
| `updated_at` | timestamp | Last update time (RFC3339) |

### Tags

| Tag Format | Example | Purpose |
|---|---|---|
| `{uid}` | `c53dc2b0-b7ed-483f-9296-b7d904e8d168` | Direct lookup by UID |
| `committee_member_uid:{uid}` | `committee_member_uid:c53dc2b0-b7ed-483f-9296-b7d904e8d168` | Namespaced lookup by UID |
| `committee_uid:{value}` | `committee_uid:061a110a-7c38-4cd3-bfcf-fc8511a37f35` | Find members of a committee |
| `committee_category:{value}` | `committee_category:Board` | Find members by committee category |
| `username:{value}` | `username:govofficial4` | Find members by username |
| `email:{value}` | `email:gac010@example.com` | Find members by email |
| `voting_status:{value}` | `voting_status:Voting Rep` | Find members by voting status |
| `organization_id:{value}` | `organization_id:org-789` | Find members by organization ID |
| `organization_name:{value}` | `organization_name:The Linux Foundation` | Find members by organization name |
| `organization_website:{value}` | `organization_website:linuxfoundation.org` | Find members by organization website |

> Tags for `username`, `email`, `voting_status`, `organization_id`, `organization_name`, and `organization_website` are only emitted when the value is non-empty.

### Access Control (IndexingConfig)

| Field | Value |
|---|---|
| `access_check_object` | `committee:{committee_uid}` |
| `access_check_relation` | `viewer` |
| `history_check_object` | `committee:{committee_uid}` |
| `history_check_relation` | `auditor` |

### Search Behavior

| Field | Value |
|---|---|
| `fulltext` | `first_name`, `last_name`, `email`, `organization.name` |
| `name_and_aliases` | `committee_name`, `first_name`, `last_name`, `username` (non-empty values only) |
| `sort_name` | `first_name` |
| `public` | inherits from parent committee |

### Parent References

| Ref | Condition |
|---|---|
| `committee:{committee_uid}` | Always set |

---

## Committee Invite

**Object type:** `committee_invite`

**NATS subject:** `lfx.index.committee_invite`

**Source struct:** `internal/domain/model/committee_invite.go` — `CommitteeInvite`

**Indexed on:** create, update, delete of a committee invite.

### Data Schema

| Field | Type | Description |
|---|---|---|
| `uid` | string | Invite unique identifier |
| `committee_uid` | string | UID of the committee this invite belongs to |
| `invitee_email` | string | Email address of the invitee |
| `role` | string | Role the invitee is being invited to |
| `status` | string | Invite status (e.g., `Pending`, `Accepted`, `Declined`) |
| `created_at` | timestamp | Creation time (RFC3339) |

### Tags

| Tag Format | Example | Purpose |
|---|---|---|
| `{uid}` | `c53dc2b0-...` | Direct lookup by UID |
| `committee_invite_uid:{uid}` | `committee_invite_uid:c53dc2b0-...` | Namespaced lookup by UID |
| `committee_uid:{value}` | `committee_uid:061a110a-...` | Find invites for a committee |
| `invitee_email:{value}` | `invitee_email:user@example.com` | Find invites by invitee email |
| `status:{value}` | `status:Pending` | Find invites by status |

> Tags for `invitee_email` and `status` are only emitted when the value is non-empty.

### Access Control (IndexingConfig)

| Field | Value |
|---|---|
| `access_check_object` | `committee:{committee_uid}` |
| `access_check_relation` | `viewer` |
| `history_check_object` | `committee:{committee_uid}` |
| `history_check_relation` | `auditor` |

### Search Behavior

| Field | Value |
|---|---|
| `fulltext` | `invitee_email` |
| `name_and_aliases` | `invitee_email` |
| `sort_name` | `invitee_email` |
| `public` | `false` (always) |

### Parent References

| Ref | Condition |
|---|---|
| `committee:{committee_uid}` | Always set |

---

## Committee Application

**Object type:** `committee_application`

**NATS subject:** `lfx.index.committee_application`

**Source struct:** `internal/domain/model/committee_application.go` — `CommitteeApplication`

**Indexed on:** create, update, delete of a committee application.

### Data Schema

| Field | Type | Description |
|---|---|---|
| `uid` | string | Application unique identifier |
| `committee_uid` | string | UID of the committee this application belongs to |
| `applicant_email` | string | Email address of the applicant |
| `message` | string | Application message from the applicant |
| `status` | string | Application status (e.g., `Pending`, `Approved`, `Rejected`) |
| `reviewer_notes` | string | Notes left by the reviewer |
| `created_at` | timestamp | Creation time (RFC3339) |

### Tags

| Tag Format | Example | Purpose |
|---|---|---|
| `{uid}` | `a1b2c3d4-...` | Direct lookup by UID |
| `committee_application_uid:{uid}` | `committee_application_uid:a1b2c3d4-...` | Namespaced lookup by UID |
| `committee_uid:{value}` | `committee_uid:061a110a-...` | Find applications for a committee |
| `applicant_email:{value}` | `applicant_email:user@example.com` | Find applications by applicant email |
| `status:{value}` | `status:Pending` | Find applications by status |

> Tags for `applicant_email` and `status` are only emitted when the value is non-empty.

### Access Control (IndexingConfig)

| Field | Value |
|---|---|
| `access_check_object` | `committee:{committee_uid}` |
| `access_check_relation` | `viewer` |
| `history_check_object` | `committee:{committee_uid}` |
| `history_check_relation` | `auditor` |

### Search Behavior

| Field | Value |
|---|---|
| `fulltext` | `message` |
| `name_and_aliases` | _(none)_ |
| `sort_name` | _(none)_ |
| `public` | `false` (always) |

### Parent References

| Ref | Condition |
|---|---|
| `committee:{committee_uid}` | Always set |

---

## Committee Link

**Object type:** `committee_link`

**NATS subject:** `lfx.index.committee_link`

**Source struct:** `internal/domain/model/committee_link.go` — `CommitteeLink`

**Indexed on:** create, update, delete of a committee link.

### Data Schema

| Field | Type | Description |
|---|---|---|
| `uid` | string | Link unique identifier |
| `committee_uid` | string | UID of the owning committee |
| `folder_uid` | string (optional) | UID of the folder this link belongs to |
| `name` | string | Link display name |
| `url` | string | Link URL |
| `description` | string (optional) | Link description |
| `created_by_uid` | string (optional) | UID of the user who created the link |
| `created_by_name` | string (optional) | Name of the user who created the link |
| `created_at` | timestamp | Creation time (RFC3339) |
| `updated_at` | timestamp | Last update time (RFC3339) |

### Tags

| Tag Format | Example | Purpose |
|---|---|---|
| `{uid}` | `a1b2c3d4-...` | Direct lookup by UID |
| `committee_link_uid:{uid}` | `committee_link_uid:a1b2c3d4-...` | Namespaced lookup by UID |
| `committee_uid:{value}` | `committee_uid:061a110a-...` | Find links belonging to a committee |
| `folder_uid:{value}` | `folder_uid:f0a1b2c3-...` | Find links within a folder |

> `folder_uid` tag is only emitted when `folder_uid` is set.

### Access Control (IndexingConfig)

| Field | Value |
|---|---|
| `access_check_object` | `committee:{committee_uid}` |
| `access_check_relation` | `viewer` |
| `history_check_object` | `committee:{committee_uid}` |
| `history_check_relation` | `auditor` |

### Search Behavior

| Field | Value |
|---|---|
| `fulltext` | `name`, `description`, `url` |
| `name_and_aliases` | `name` |
| `sort_name` | `name` |
| `public` | inherits from parent committee |

### Parent References

| Ref | Condition |
|---|---|
| `committee:{committee_uid}` | Always set |
| `committee_link_folder:{folder_uid}` | Only when `folder_uid` is set |

---

## Committee Link Folder

**Object type:** `committee_link_folder`

**NATS subject:** `lfx.index.committee_link_folder`

**Source struct:** `internal/domain/model/committee_link.go` — `CommitteeLinkFolder`

**Indexed on:** create, update, delete of a committee link folder.

### Data Schema

| Field | Type | Description |
|---|---|---|
| `uid` | string | Folder unique identifier |
| `committee_uid` | string | UID of the owning committee |
| `name` | string | Folder name |
| `created_by_uid` | string (optional) | UID of the user who created the folder |
| `created_by_name` | string (optional) | Name of the user who created the folder |
| `created_at` | timestamp | Creation time (RFC3339) |
| `updated_at` | timestamp | Last update time (RFC3339) |

### Tags

| Tag Format | Example | Purpose |
|---|---|---|
| `{uid}` | `f0a1b2c3-...` | Direct lookup by UID |
| `committee_link_folder_uid:{uid}` | `committee_link_folder_uid:f0a1b2c3-...` | Namespaced lookup by UID |
| `committee_uid:{value}` | `committee_uid:061a110a-...` | Find folders belonging to a committee |

### Access Control (IndexingConfig)

| Field | Value |
|---|---|
| `access_check_object` | `committee:{committee_uid}` |
| `access_check_relation` | `viewer` |
| `history_check_object` | `committee:{committee_uid}` |
| `history_check_relation` | `auditor` |

### Search Behavior

| Field | Value |
|---|---|
| `fulltext` | `name` |
| `name_and_aliases` | `name` |
| `sort_name` | `name` |
| `public` | inherits from parent committee |

### Parent References

| Ref | Condition |
|---|---|
| `committee:{committee_uid}` | Always set |
