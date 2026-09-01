<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Weekly Brief Activity Sources

The committee weekly brief (`GroupWeeklyBriefGenerator`) aggregates activity from several
query-service sources over a caller-supplied time window. Each source is an adapter in
`internal/infrastructure/m2m/` that implements one or more port interfaces.

---

## Sources

### Meetings — `meeting_source.go`

| Field | Value |
|-------|-------|
| Resource type | `v1_past_meeting` |
| Committee tag | `committee_uid:{uid}` |
| Date filter | `date_field=start_time` + `date_from`/`date_to` |
| Date field | `start_time` |

### Meeting AI Summaries — `meeting_ai_summary_source.go`

| Field | Value |
|-------|-------|
| Resource type | `v1_past_meeting_summary` |
| Committee tag | `committee_uid:{uid}` |
| Date filter | `date_field=summary_start_time` + `date_from`/`date_to` |

### Votes — `vote_source.go`

| Field | Value |
|-------|-------|
| Resource type | `vote` |
| Committee tag | `committee_uid:{uid}` |
| Date filter | `date_field=end_time` + `date_from`/`date_to` |

### Vote Results — `vote_result_source.go`

| Field | Value |
|-------|-------|
| Resource type | `vote_result` |
| Tag | `vote_uid:{uid}` (not committee-scoped; looked up per vote) |

### Surveys — `survey_source.go`

| Field | Value |
|-------|-------|
| Resource type | `survey` |
| Committee tag | `committee_uid:{uid}` |
| Date filter | `date_field=survey_cutoff_date` + `date_from`/`date_to` |

### Project Membership — `project_membership_source.go`

| Field | Value |
|-------|-------|
| Resource type | `project_membership` |
| Tag | `project_uid:{uid}` (project-scoped; resolved from committee) |
| Date filter | `date_field=purchase_date` + `date_from`/`date_to` |

### Mailing List Messages — `mailing_list_source.go`

| Field | Value |
|-------|-------|
| Resource type | `groupsio_mailing_list_message` |
| Committee tag | `committee:{uid}` |
| Date filter | `date_field=created_at` + `date_from`/`date_to` |

Records are per-message; the adapter groups them by `topic_id` and returns one
`MailingListActivity` per thread with subject/excerpt from the earliest in-window message.

**Tag prefix difference:** this source uses `committee:` while all other committee-scoped
sources use `committee_uid:`. This matches the tag emitted by `lfx-v2-mailing-list-service`
(`grpsio_message.go` — `Tags()` method), which chose `committee:` to align with the broader
LFX tag convention for this resource type. Changing either side would require a coordinated
re-index.

**Known limitation:** if a thread's true opener was posted before the brief window, the
earliest in-window message is used as the thread representative. This is acceptable for a
7-day window and documented in `mailing_list_source.go`.

---

## Date filter styles

There are two query-service date filtering styles in use across these sources:

| Style | Params | Used by |
|-------|--------|---------|
| Field + range | `date_field=<field>`, `date_from`, `date_to` | All sources |

All sources use the field+range style. `date_field` names a key inside the resource's
`data` blob; `date_from`/`date_to` are ISO 8601 timestamps.
