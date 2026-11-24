<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# V1 committee audit history: offline preview

## Status and safety boundary

`committee-cli report v1-audit-history` implements the **offline schema/source
mapping slice** of `linuxfoundation/lfx-self-serve-ops#1420`. It reads local
NDJSON and an optional fixed mapping manifest. It runs before NATS, PostgreSQL,
OpenSearch, directory or storage-mode initialization, including with invalid
connection settings. It cannot publish, mutate resources, send notifications,
change FGA, allocate revisions, bootstrap current search, or import history.

The optional output contains **private draft plans**, not indexer envelopes or
outbox events. It has no transport credentials or actor JWTs. Do not send it to
an indexing subject or treat a locally importable row as authorized for dispatch.
Publishing, resumable execution, reconciliation, approved indexer policies,
erasure-fence checks and generic SDK preflight remain subsequent work.

The native committee publisher and its complete-resource schemas are separate.
This preview does not require the publisher's transactional outbox to be enabled,
and it does not change any live publication path or upgrade the indexer SDK.

## Source requirements

Export **every column** of `platform.committee_audit_log` as one `row_to_json`
object per line. Do not use the legacy audit API, CSV, or the materialized view:
those filter/transform data or omit the source UUID. Preserve JSON types/nulls and
timestamp precision. Keep exports outside Git and restrict permissions.

Required metadata: nonzero UUID `id`, `record_id`, action, record type and nonzero
RFC3339 `created_at`; members also require `sub_record_id`. Before/after images
must match INSERT/UPDATE/DELETE sides. An image's `sfid` must match the source
resource record ID. A member image must also identify its parent through
`collaboration_name__c`. Project scope comes only from each historical image's
`project_name__c`, never from today's resource or a name lookup.

Supported record types are `COMMITTEE` and `COMMITTEEMEMBER`. `sub_record_id` is
the **membership record's ID**, not `contact_name__c` (the person's ID).
`changed_record` is not used to reconstruct state: comparisons use the full
approved portions of before/after images. Empty images are treated as absent.
Duplicate JSON keys, noncanonical metadata key casing, invalid UTF-8, unpaired
UTF-16 surrogate escapes (such as `\uD800`), trailing JSON and excessive nesting
are rejected. Unknown top-level columns fail closed.

## Historical snapshot schemas

The local closed schema definitions live in
`internal/domain/model/v1_audit_history.go`:

- `committee.legacy.v1`: committee business attributes.
- `committee_settings.legacy.v1`: only observed business-email requirement.
- `committee_member.legacy.v1`: membership, organization, role and voting fields.

Each snapshot contains mapped `uid`, `committee_uid`, `project_uid` and a closed
`fields` object. **Legacy field names and values are deliberate**, not guessed V2
category, name, role or date conversions. The schema must be reviewed/registered
for the eventual import publisher before any dispatch. Local validation is not
production policy approval; once adopted, changing the allowed shape needs a new
version rather than silently broadening it.

| Resource | Allowed fields |
| --- | --- |
| Committee | `mailing_list__c`, `public_name`, `type__c`, `description__c`, `committee_website__c`, `mailing_list_email`, `chat_channel`, `enable_voting__c`, `sso_group_enabled`, `sso_group_name`, `public_enabled`, `join_mode` |
| Settings | `business_email_required__c` |
| Member identity/context | `contact_name__c`, `contactemail__c`, `member_name`, `account__c`, `account_name`, `title`, `type__c` |
| Member state | `role__c`, `appointed_by__c`, `status__c`, `start_date__c`, `end_date__c`, `voting_status__c`, `votind_start_date__c`, `voting_end_date__c` |

Boolean source fields must remain booleans or null; other listed fields must be
strings or null. Unknown source-image fields are excluded, never forwarded.
Captured strings are bounded, never truncated. Website/chat URL forms with
userinfo, queries, fragments, unsupported schemes or an opaque `https:` form
(no `//`), and incoming-webhook URLs reject the row rather than retaining possible
credentials. Webhook URLs are the Slack and Microsoft Teams webhook hosts, plus any
URL with a `webhook`, `webhooks`, `webhookb2` or `hooks` path segment on any host
(covering Discord, legacy Office 365 connectors and self-hosted chat servers).
Older `chat_channel` values can be plain channel names such as `#general`; those
are kept as text. Webhooks, raw request/profile maps, bookkeeping timestamps,
unrelated personal fields and live V2-only defaults are not copied.

An absent field means **unknown/not captured**. A captured null, false or empty
string retains its distinct value. An old settings image with no captured
settings fields is a snapshot with an empty `fields` object, not an invented
false value. Create/delete events require meaningful captured fields; updates
with no difference in the selected fields are skipped. A committee source row
can yield distinct committee and settings events, but only for changed groups.

Known gaps include grants/auditor rosters held outside this table, other V2-only
settings, events predating source capture, excluded/unmapped field changes and
missing historical project scope. Ownership moves are reported as requiring
review rather than choosing an unapproved cross-scope history policy. No
completeness claim is made for skipped or absent data.

## Fixed offline mapping manifest

Without `--mappings`, the command is an inventory: rows needing IDs/revisions are
reported as skipped. It does not invent UUIDs or connect to live mapping stores.
For candidate plans, provide a reviewed export in this format (fake values):

```json
{
  "version": 1,
  "source": "dev-v1",
  "projects": {"project-1": "00000000-0000-4000-8000-000000000002"},
  "committees": {"committee-1": "00000000-0000-4000-8000-000000000001"},
  "members": {"member-record-1": "00000000-0000-4000-8000-000000000003"},
  "actors": {"actor-1": "user:first-last"},
  "revisions": {
    "00000000-0000-4000-8000-000000000010/committee": 1,
    "00000000-0000-4000-8000-000000000010/committee_settings": 1
  },
  "v2_origin_actors": ["v2-sync-actor-1"],
  "native_history_since": "2026-12-01T00:00:00Z"
}
```

The manifest source must match `--source`. Keep namespaces distinct across
environments and freeze the export/manifest before comparing runs. Mapped targets
must be nonzero canonical UUIDs. Use authoritative retained mappings for deleted
members/resources; do not generate replacements, reinterpret tombstones as
permission to recreate resources, or match a person by email alone.

Revision keys are `<source-event-uuid>/<resource-type>`. Supply reviewed positive
integers through MaxInt64 with an approved historical ordering meaning. The
preview never derives a revision from line position, current time, broker order
or an independent guessed live counter. Missing revisions skip the whole source
row, including any split resource events. Conflicting revisions for one mapped
resource are rejected within the bounded preview. Existing history/current
lineage is not consulted; global reconciliation and approval remain prerequisites.

Source occurrence times are converted to UTC without loss of fractional
precision. Captured resource-date strings are preserved. Equal source timestamps
do not establish chronological order; source UUIDs are identities, not an ordering
oracle. Event IDs hash the source namespace, table, source UUID and target resource
type, so split events are distinct and repeated previews are deterministic.
Within a run, identical source UUID/content repeats are skipped and changed
content under the same UUID is invalid. This applies across the whole run, even
when `--committee-id` excludes one of the copies. No durable replay state is written.

### Changes made in v2

A change made in v2 is synced back to v1, and the v1 trigger records it as an
ordinary audit row. Its `created_by` is the v2-to-v1 sync's actor ID, with no
username or `user_source`, so v1 never recorded the human who made it. Once native
v2 committee history is live in an environment, v2 already holds that change with
its real actor, and importing the v1 row would record it twice.

Both optional manifest fields are environment-specific and must be reviewed:

- `v2_origin_actors`: the exact recorded `created_by` IDs the sync writes with.
  Listing them never changes rows by other actors. Confirm each ID is used only
  by the v2-to-v1 sync: every row it recorded after the cutover is skipped.
- `native_history_since`: an RFC 3339 time in UTC (ending in `Z`), when native
  committee history began in this environment (the deployment of the live
  committee audit publisher). It requires `v2_origin_actors`.

INSERT and UPDATE rows by a listed actor at or after the cutover are skipped as
`recorded_natively_in_v2`. DELETE rows are never skipped: the trigger records the
deleted row's previous last modifier, not the deleter, so a sync actor on a delete
does not show the delete came from v2. A v2-originated delete may therefore be
planned twice, which reconciliation can find. Earlier rows, and every row while
no cutover is set, are the only record of those changes and are planned normally.
Map their actor explicitly (for example `service:<sync client>`) rather than
inventing a person.

The cutover is compared with the v1 row's `created_at`, which is when the sync
wrote the change to v1, not when it was made in v2. A change made in v2 just
before native history started can reach v1 just after it, and would be skipped
although v2 never recorded it. Set the cutover to when the publisher was fully
rolled out, plus the worst-case v2-to-v1 sync delay. Erring late only plans a few
duplicates, which reconciliation against native history can find; erring early
loses those changes without a trace. Before importing, compare the dry-run
`recorded_natively_in_v2` count with native v2 history for the same window.

## Attribution and authorization

An explicit `actors` mapping overrides the recorded actor identity. Otherwise a
valid recorded username becomes a candidate `user:<principal>`; a recorded
`<client>@clients` becomes `service:<client>`. The offline preview cannot verify
account existence, renamed/reassigned usernames or directory ownership: review
these mappings before freezing a dispatch manifest. No current email/profile is
looked up and no historical name/avatar capture is retained as actor identity.

If unresolved, preserve `legacy:<source>/<created_by>`. IDs that cannot fit the
bounded ref grammar use `legacy:<source>/sha256:<hash>`; their original value
remains in the protected source export. With no actor ID, use a source/event-derived
`legacy:<source>/event:<hash>` reference, not one invented user for unrelated
events. Recorded IDs never contain `:`, so these three forms cannot collide. No actor is replaced by the importer and no JWT evidence is manufactured.
The compatible indexer/reader own null identity, false verification and current
display enrichment. A delete's recorded actor may be the previous last modifier,
not the deleter; this tool does not strengthen that source attribution claim.

Candidate history metadata uses the mapped `committee:<uid>#auditor` anchor,
including settings/member deletes, with captured project/committee scope. This
matches native settings documents, which are also checked against
`committee:<uid>#auditor` (see [the indexer contract](indexer-contract.md)); the
OpenFGA model has no `committee_settings` type. Never substitute a project grant.
This is **metadata validation, not a live FGA check**: it creates no grants, and
deleted committees may be unreadable when their tuples are gone.

## Commands, output and failure handling

Inventory without infrastructure or writes:

```bash
committee-cli report v1-audit-history \
  --input ~/scratch/v1-committee-audit.ndjson --source dev-v1
```

Bound a mapped preview and optionally write private draft events:

```bash
committee-cli report v1-audit-history \
  --input audit.ndjson --source dev-v1 --mappings mappings.json \
  --committee-id committee-1 --max-records 1000 \
  --plan-output private-draft.ndjson
```

`--dry-run` defaults true; false is rejected. The default limit is 10,000 nonempty
records read (including records outside the selected committee); maximum 100,000.
`limited: true` means unread input remains and the report is not a full inventory.
Input past the limit is only checked for remaining non-whitespace bytes, never read
as a record, so an oversized or malformed record there cannot fail the preview.
The manifest and individual source lines are bounded to 1 MiB, nesting to 64,
captured strings to 64 KiB, and individual private drafts to 1 MiB. A draft fitting
that local limit is not proof that a future wire envelope fits its own budget.

Stdout contains aggregate counts and line-number/fixed-reason diagnostics only:
`rows`, `importable`, `skipped`, `invalid`, `events`, `limited`, `reasons`, `issues`,
and `actor_candidates` counts (`mapped`, `recorded_user`, `recorded_service`,
`legacy`) for unique in-scope parsed rows. Recorded identities are candidates,
not evidence of a current-directory match or verification.
No names, emails, actor refs, snapshots, source values or private paths are logged.
Exit 0 means the bounded preview ran without invalid records, **not** that all
rows were importable or audit history was persisted. Check skipped/limited counts.

Only explicit `--plan-output` writes draft events with personal approved snapshot
fields. Creation is exclusive, mode 0600; existing files are never overwritten,
including an input/manifest alias. Errors, cancellation (including SIGINT or
SIGTERM, even while a FIFO input is waiting for data), a closed stdout or invalid
records discard new draft output; cleanup failure is reported rather than claiming success.
Keep the file outside Git/general logs, protect its directory/backups, and include
it with source exports/manifests in erasure and retention handling. Drafts may be
partial because of skipped or bounded input and are never publishable as-is.

To stop, terminate the preview; there is no remote state to roll back. Correct
source/mapping gaps without rewriting original exports, and rerun into a new
private filename. Do not relabel events, fabricate unknown values, bypass erased
source IDs or enable a permissive schema validator to turn skips into imports.
Live erasure/replay fences, immutable-content checks, source/publisher policy,
credential handling and actual persistence reconciliation must be implemented
and verified by the later historical-ingestion stage.
