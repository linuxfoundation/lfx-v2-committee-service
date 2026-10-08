<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Working-Group Weekly Brief

The weekly brief is an AI-written summary of a committee's activity over one
UTC week: meetings, meeting AI summaries, votes, surveys, mailing-list threads,
member joins/updates, and project memberships. This page is the entry point for
contributors. It explains the end-to-end pipeline and how to extend it. For
per-source query details see [Source reference](#source-reference);
for the prompt eval harness see
[evals/weekly-brief/README.md](../evals/weekly-brief/README.md).

## Pipeline at a glance

```text
POST /weekly-briefs/generate
  -> Claim (sync): edited-brief guard, throttle check + bump, persist brief as "generating"
  -> publish lfx.committee-api.weekly_brief.generate_requested (weekly-brief-events stream)
  -> 202 Accepted (brief in "generating" state)

durable consumer (committee-service-weekly-brief-generate)
  -> Fulfill (async): gather sources -> build evidence -> AI adapter -> persist
  -> brief becomes "generated" (or "error"), then lfx.index.group_weekly_brief is published
```

### 1. Trigger and claim

`POST /committees/{uid}/weekly-briefs/generate` (handler in
`cmd/committee-api/service/committee_service.go`) calls
`GroupWeeklyBriefGenerator.Claim` (`internal/service/group_weekly_brief_generator.go`),
which runs synchronously:

1. Resolves the window with `model.WeeklyWindow(now)`.
2. Rejects with an edited-brief conflict if the existing brief is in `edited`
   state and `force` is not set.
3. Applies the per-committee, per-window throttle: 2 fresh generations and 3
   regenerations (`GroupWeeklyBriefGenerateLimit`, `GroupWeeklyBriefRegenerationLimit`
   in `internal/domain/model/group_weekly_brief.go`). Over the limit returns 429
   with the counters and reset time.
4. Bumps the throttle counter, then persists the brief in `generating` state.

The handler then publishes `GenerateWeeklyBriefRequestedEvent` on
`lfx.committee-api.weekly_brief.generate_requested` and returns 202. The event
carries `requested_at`, which pins the window so the async phase computes
exactly the same one.

### 2. Window

`WeeklyWindow` returns a UTC Sunday 00:00:00 to Saturday 23:59:59.999999999
window anchored on the most recent Saturday on or before today. Sunday through
Friday select the previous, completed week. On Saturday the window is the
current, not-yet-finished week.

### 3. Async fulfillment

The durable consumer `committee-service-weekly-brief-generate` (`AckWait` 5
minutes, `MaxDeliver` 3, see `internal/infrastructure/nats/stream_consumer.go`)
hands each event to `weekly_brief_message_handler.go`, which calls
`Fulfill`. The handler derives `MembersHidden` from the committee's
`member_visibility` setting: anything other than `basic_profile` hides member
names, so the brief only contains counts such as "3 new members".

`Fulfill`:

1. Re-reads the claimed brief; if it is missing or no longer `generating`, it
   logs and ACKs (another worker handled it).
2. Gathers sources. The member source is internal, and a failure there is
   returned so the message is retried. The query-service sources (meetings, AI
   summaries, mailing lists, votes, vote results, surveys, project
   memberships) degrade to empty on error.
3. If every source is empty: when any source errored, it returns the error so
   the message is retried (an outage must not look like "no activity").
   Otherwise the brief is finalized in `error` state with `error_reason`
   `no_sources` and the message is ACKed.
4. Converts the activity into `ClaimEvidence` rows and `SourceRef`s
   (`buildClaimsAndRefs`), calls the AI adapter, and on failure finalizes the
   brief as `error` with reason `ai_error`.
5. On success writes the brief as `generated` with `brief_text`,
   `source_refs`, `prompt_version`, `model`, and `private_source_present`, then
   publishes the indexer message (see [indexer-contract.md](indexer-contract.md)).

### Brief states

`empty`, `generating`, `generated`, `edited`, `approved`, `error`
(`internal/domain/model/group_weekly_brief.go`).

## Activity sources

Each query-service source lives in `internal/infrastructure/m2m/` and is bundled
into `ActivitySources` in the generator. The member reader plus meetings,
mailing lists, and votes are required; AI summaries, vote results, surveys,
and project memberships are optional and degrade to zero when not wired.
Resource types, tag filters, and date fields are listed in the
[Source reference](#source-reference) below.

Two behaviors to keep in mind when a brief looks empty:

- With `QUERY_SERVICE_URL` unset, the query-service sources return zero
  results (no startup failure). With it set, the `M2M_AUTH_*` credentials are
  required.
- The mailing-list source filters on the `committee:` tag, not `committee_uid:`.
  This matches what the mailing-list service emits; do not change one side
  without a coordinated re-index.

### Source reference

#### Meetings — `meeting_source.go`

| Field | Value |
|-------|-------|
| Resource type | `v1_past_meeting` |
| Committee tag | `committee_uid:{uid}` |
| Date filter | `date_field=start_time` + `date_from`/`date_to` |
| Date field | `start_time` |

#### Meeting AI Summaries — `meeting_ai_summary_source.go`

| Field | Value |
|-------|-------|
| Resource type | `v1_past_meeting_summary` |
| Committee tag | `committee_uid:{uid}` |
| Date filter | `date_field=summary_start_time` + `date_from`/`date_to` |

#### Votes — `vote_source.go`

| Field | Value |
|-------|-------|
| Resource type | `vote` |
| Committee tag | `committee_uid:{uid}` |
| Date filter | `date_field=end_time` + `date_from`/`date_to` |

#### Vote Results — `vote_result_source.go`

| Field | Value |
|-------|-------|
| Resource type | `vote_result` |
| Tag | `vote_uid:{uid}` (not committee-scoped; looked up per vote) |

#### Surveys — `survey_source.go`

| Field | Value |
|-------|-------|
| Resource type | `survey` |
| Committee tag | `committee_uid:{uid}` |
| Date filter | `date_field=survey_cutoff_date` + `date_from`/`date_to` |

#### Project Membership — `project_membership_source.go`

| Field | Value |
|-------|-------|
| Resource type | `project_membership` |
| Tag | `project_uid:{uid}` (project-scoped; resolved from committee) |
| Date filter | `date_field=purchase_date` + `date_from`/`date_to` |

#### Mailing List Messages — `mailing_list_source.go`

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


### Date filter styles

All sources that filter by a date window use the field+range style:

| Style | Params | Used by |
|-------|--------|---------|
| Field + range | `date_field=<field>`, `date_from`, `date_to` | All window-filtered sources |

`date_field` names a key inside the resource's `data` blob; `date_from`/`date_to` are
ISO 8601 datetime strings. Query-service normalizes them to second precision internally
(`parseDateFilter` re-formats parsed values with `time.RFC3339`), so sub-second
components are not forwarded to OpenSearch. Vote Results has no date-window filter —
it is looked up per-vote, not by time range.

## AI adapter

Selected at startup by `AI_SOURCE` (default `fake`):

| Adapter | When | Notes |
|---|---|---|
| Fake (`internal/infrastructure/ai/fake_adapter.go`) | `AI_SOURCE=fake` (default) | Deterministic, no network or credentials. Never copies source text into output. Used for local dev, CI, and the hermetic eval suite. |
| LiteLLM (`internal/infrastructure/ai/litellm_adapter.go`) | `AI_SOURCE=live` | Calls a LiteLLM chat-completions endpoint at temperature 0 with up to 3 attempts and a 500 ms backoff. Requires `LITELLM_BASE_URL`, `LITELLM_API_KEY`, `LITELLM_MODEL`. |

### Prompts

The live adapter has no built-in prompts. It reads two files from the
directory in `WEEKLY_BRIEF_PROMPT_DIR` (a ConfigMap mount in deployments):
`system_prompt` and `user_prompt_template`. The `prompt_version` stored on each
brief is an 8-character sha256 prefix of those files. If the directory is unset
or a file is missing, empty, or fails to parse, the service still starts, but
every live generation fails. Prompts are loaded once when the adapter is created and the load error is kept, so fixing or mounting the files requires restarting or redeploying the service. Default prompt text lives
in `charts/lfx-v2-committee-service/values.yaml` under `weeklyBriefPrompts`.

### Required env vars

| Variable | Needed for |
|---|---|
| `AI_SOURCE` | `live` to use the LLM; `fake` by default |
| `LITELLM_BASE_URL`, `LITELLM_API_KEY`, `LITELLM_MODEL` | `AI_SOURCE=live` |
| `WEEKLY_BRIEF_PROMPT_DIR` | `AI_SOURCE=live` |
| `QUERY_SERVICE_URL` | Optional; set it to include query-service activity |
| `M2M_AUTH_CLIENT_ID`, `M2M_AUTH_PRIVATE_KEY`, `M2M_AUTH_DOMAIN` | Required when `QUERY_SERVICE_URL` is set |

The full list is in the [API README](../cmd/committee-api/README.md#3-export-environment-variables).

## API endpoints and access

| Endpoint | Purpose | Required access |
|---|---|---|
| `GET /committees/{uid}/weekly-briefs/current` | Read the brief and throttle for the current window (200 with nulls on a miss) | viewer |
| `POST /committees/{uid}/weekly-briefs/generate` | Claim and start async generation (202) | writer |
| `POST /committees/{uid}/weekly-briefs/preview-generate` | Generate synchronously without writing storage or throttle | writer |
| `PUT /committees/{uid}/weekly-briefs/current` | Save an edit; moves the brief to `edited`. Needs the `revision` token from GET (409 on mismatch) | writer |
| `POST /committees/{uid}/weekly-briefs/share-to-chat` | Send the brief to the committee's chat webhook. Brief must be `generated`, `edited`, or `approved`; 409 on stale `revision`, error if no webhook is configured | writer |

The brief document is indexed as `group_weekly_brief` with
`access_check_relation: viewer`, so visibility matches the committee's own: a
public committee's brief is visible to anyone who can view the group, and a
private committee's brief only to members and elevated users. This mirrors
`GET /current`. `private_source_present` is only a UI disclosure flag.

## Storage

| Store | Contents |
|---|---|
| KV `group-weekly-briefs` | Brief JSON, keyed by brief UID |
| KV `group-weekly-brief-uid-index` | `{committee_uid}.{window_yyyymmdd}` to brief UID |
| KV `group-weekly-brief-throttle` | Per-committee, per-window generate and regeneration counts |
| Stream `weekly-brief-events` | Generate-requested events |

Bucket names are defined in `pkg/constants/storage.go`. The CLI can rebuild the
index messages with `committee-cli sync backfill-weekly-brief-index`
(see the [CLI README](../cmd/committee-cli/README.md)).

## Release gate

`.github/workflows/ko-build-tag.yaml` runs `weekly-brief-eval-live.yml` as an
`eval-live` job that the publish job `needs`, so a failing live eval blocks the
release. The workflow pins `LITELLM_BASE_URL` and `LITELLM_MODEL` in its `env:`
block and reads `LITELLM_API_KEY` from AWS Secrets Manager via OIDC. To rotate
the key or change the endpoint or model, edit the workflow, not GitHub repo
secrets.

Before opening a PR that changes prompts or evidence formatting, run the
live eval locally:

```sh
WEEKLY_BRIEF_PROMPT_DIR=$(make eval-prompt-dir) \
LITELLM_BASE_URL=... LITELLM_API_KEY=... LITELLM_MODEL=... \
  make eval-live
```

The hermetic suite runs with plain `go test ./evals/weekly-brief/...`.

## Adding a new activity source

1. Add the port interface and activity type in
   `internal/domain/port/group_weekly_brief_sources.go`.
2. Implement it in `internal/infrastructure/m2m/<name>_source.go`, with a
   `_test.go` alongside it. Follow an existing source for query-service access
   and tag/date filters.
3. Add a field to `ActivitySources` in
   `internal/service/group_weekly_brief_generator.go` and gather it in
   `Fulfill` with `gatherDegradable`. Include its error in the no-source
   retry check, and add the activity to the "all empty" condition.
4. Turn the activity into evidence in `buildClaimsAndRefs`, and include it in
   `derivePrivateSourcePresent` if it can come from non-public data. Keep raw
   source text out of `ClaimEvidence.Summary`.
5. Wire the adapter in `cmd/committee-api/service/providers.go`. Use a
   `QUERY_*_TYPE` env var for the resource type only if it must be overridable,
   and document it in the API README.
6. Add or extend a fixture under `evals/weekly-brief/fixtures/`.
7. Add the source to the [Source reference](#source-reference) in the same PR.
8. Run `go test ./evals/weekly-brief/...` and, if prompts or evidence format
   changed, `make eval-live`.

## Debugging an empty or degraded brief

1. Check the brief's state in `group-weekly-briefs`. `error` with
   `error_reason` `no_sources` or `ai_error` says which stage failed.
2. Confirm the source records are indexed in query-service for that committee
   and window; missing data is usually upstream.
3. Check `QUERY_SERVICE_URL` and the `M2M_AUTH_*` credentials. If the URL is
   unset, every query-service source returns zero.
4. Check `AI_SOURCE`. `fake` produces a structurally valid brief with no real
   content, and `live` fails every generation without readable prompts.
5. Check the throttle bucket if the API returns 429.
