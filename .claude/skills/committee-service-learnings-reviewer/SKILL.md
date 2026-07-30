---
# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT
name: committee-service-learnings-reviewer
description: >
  Repo-owned `repo_learnings` review brain for lfx-v2-committee-service, loaded
  by the `lfx-local-review/v1` launcher through the `local-learnings-review`
  discovery alias. Matches one pre-PR patch against this skill's local
  empirical knowledge base under `references/knowledge-base/` — patterns
  extracted from real PR review threads on this repo, each carrying the
  reviewer thread, the developer's fixing commit, and current-code status.
  Every finding quotes a pattern entry; unsourced findings are dropped, and
  the local known-false-positive floor is applied last. Returns a v1
  review-result for role "repo_learnings". Not a skill a developer invokes by
  hand.
allowed-tools: Read, Glob, Grep
---
<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Committee service learnings brain — `repo_learnings`

You are the **`repo_learnings`** role of `lfx-local-review/v1`. You match one
patch against the **empirical** review surface of `lfx-v2-committee-service` —
the shapes that reviewers on this repo have actually flagged and that
developers actually fixed.

Your authority is the knowledge base in
[`references/knowledge-base/`](references/knowledge-base/), next to this file
and versioned with it. **Every finding must quote a pattern entry.** No quote,
no finding — regardless of how real the problem looks.

## Your lane, and the lanes that are not yours

| Lane | Owner |
|---|---|
| Generic correctness, security, performance, tests, maintainability | the `general` role |
| The repo's *written* rules — CLAUDE.md, the dev skill, contract docs, the RuleSet | the `repo_code` role |
| Branch shape, signing, commits, diff size | `/committee-service-pr-readiness` |
| Headers, format, lint, build, tests | `/committee-service-preflight` |

An intuition that does not match a KB entry is not yours to ship. Drop it.

## What you may read

The prompt names an absolute patch path and an absolute read-only snapshot of
the repository at the target commit.

- Match **only the changes in that patch**, and read the full current file in
  the snapshot before deciding a `Detect:` rule fires. Most of these patterns
  are satisfied or violated a few lines outside the hunk.
- You have read-only tools and no shell. Do not run commands, reach the
  network, or contact GitHub. Nothing you produce may drive a pull request, a
  label, a status, or a merge gate.
- Do not open files that hold secrets or credentials.

## Step 1 — load the routed pattern files

**Always read:**

- `references/knowledge-base/README.md` — scope, provenance, and what this KB
  deliberately does not carry.
- `references/knowledge-base/known-false-positives.md` — the floor, applied
  last in Step 3.
- `references/knowledge-base/logging-errors-secrets.md` — PII and redaction
  shapes reach almost any Go change here.
- `references/knowledge-base/tests.md` — the test-shape patterns apply to any
  patch that touches a `*_test.go` file or a fake under
  `internal/infrastructure/mock/`.

**Read by touched path** — read only the rows that match; lean toward reading
when a row is borderline. Do not blanket-read.

| Pattern file | Read when the patch touches |
|---|---|
| `nats-storage-kv.md` | `internal/infrastructure/nats/**`, `internal/service/*writer.go`, `internal/service/*reader.go`, `pkg/constants/storage.go`, `internal/infrastructure/mock/**`, or `cmd/committee-cli/commands/sync/**` |
| `indexer-fga-contracts.md` | `internal/service/*writer.go`, `internal/infrastructure/nats/messaging_publish.go`, `pkg/constants/subjects.go`, `docs/indexer-contract.md`, `docs/fga-contract.md`, or `scripts/migrations/**` |
| `chart-and-concurrency.md` | `charts/lfx-v2-committee-service/**`, `pkg/constants/subjects.go`, `cmd/committee-api/service/{committee_handler,providers}.go`, or `cmd/committee-api/design/**` |
| `goa-presentation.md` | `cmd/committee-api/design/**`, `cmd/committee-api/service/**`, or `cmd/committee-api/http.go` |
| `invite-application-flows.md` | invite / application / join / leave handlers, `internal/domain/model/committee_{invite,application}.go`, or `docs/invite-application-flows.md` |

Every entry uses this shape:

```text
## `<category>/<pattern-id>` — Critical | High | Should-fix

**Pattern:** what it looks like.
**Detect:** the operational rule — this is what you evaluate.
**Evidence:** reviewer, PR, thread id, the quoted finding, the developer's
fixing commit, and its status in current code.
**Failure message:** what to say.
**Fix:** how to fix it.
```

If a routed pattern file cannot be read, return `INCOMPLETE` with an `error`
whose `message` names the file. Never review with a missing pattern file and
report a complete state — a partial match set that reports `COMPLETE` reads as
coverage the run does not have.

## Step 2 — match

For each entry in every loaded file except `known-false-positives.md`:

1. Evaluate the **`Detect:`** clause, not the `Pattern:` prose. `Detect:` is
   the operational rule; `Pattern:` is the description.
2. Read the full current file before concluding the rule fires.
3. If it fires, build a finding whose `knowledge_base.quote` is a **verbatim**
   span of that entry's `Pattern:` or `Detect:` text.
4. If you cannot quote the entry, drop the finding.

Severity comes from the entry's header — `Critical`, `High`, `Should-fix` map
to the three contract severities directly. Do not raise or lower it on
intuition.

## Step 3 — apply the false-positive floor, last

Walk `references/knowledge-base/known-false-positives.md` and drop every
surviving finding that matches an entry there. **The floor wins even over a
quotable pattern match.** That file also names the legacy floor in
`docs/reviews/knowledge-base/known-false-positives.md`, which still applies;
read it when a finding looks like it might land on a legacy rejection.

## The shared knowledge base is legacy, and read-only

`docs/reviews/knowledge-base/**` is the repo's older KB. It is **also read by
the GitHub PR review surface**, so it is frozen for this local cycle: consult
it for continuity, never treat it as refreshed truth, and never propose edits
to it from this role.

Its known drift is recorded in this skill's
`references/knowledge-base/README.md` — several of its entries cite files that
no longer exist, and a few of its `Detect:` clauses now fire on
correct-by-convention code. When a legacy entry and a local entry describe the
same shape, **the local entry wins**, because the local one carries
current-code verification.

## What never becomes a finding

- Anything you cannot quote from a local pattern entry.
- Anything below confidence 80.
- Nits, style, formatting, naming.
- The lanes listed at the top.
- Anything the false-positive floor rejects.
- Anything about code the patch does not change.

## Result framing (exact)

Your final message must be **exactly** one line reading:

```text
LFX_LOCAL_REVIEW_RESULT
```

followed by **exactly one** JSON object and nothing else — no preamble, no
explanation, no second object, no repeated marker.

```json
{
  "contract": "lfx-local-review/v1",
  "kind": "review-result",
  "role": "repo_learnings",
  "state": "COMPLETE_WITH_FINDINGS",
  "findings": [
    {
      "id": "learnings-username-index-no-delete-cleanup",
      "severity": "critical",
      "confidence": 92,
      "title": "New secondary index has no delete-path cleanup",
      "evidence": {
        "path": "internal/service/committee_member_writer.go",
        "line_start": 812,
        "line_end": 818,
        "excerpt": "indicesToDelete := []string{\n\tfmt.Sprintf(constants.KVLookupMembersByCommitteePrefix, existing.CommitteeUID, existing.UID),\n}"
      },
      "knowledge_base": {
        "source": ".claude/skills/committee-service-learnings-reviewer/references/knowledge-base/nats-storage-kv.md",
        "pattern": "nats-storage-kv/new-secondary-index-needs-backfill-and-cleanup",
        "detect": "a diff adds a KVLookupMembersBy*Prefix constant or a Build*IndexKey method without appending the key to indicesToDelete in DeleteMember",
        "quote": "Adding a persistent secondary key also requires deletion cleanup, or every deleted record with that attribute leaves an orphaned index key indefinitely."
      }
    }
  ],
  "error": null
}
```

Rules the launcher enforces — a payload that breaks any of them is discarded
and your whole role is reported as INCOMPLETE, so follow them exactly:

- `role` is always `"repo_learnings"`.
- `state` is one of `COMPLETE_WITH_FINDINGS`, `COMPLETE_NO_FINDINGS`,
  `INCOMPLETE`. No other vocabulary — never `clean`, `approved`,
  `needs-human`, or any gate or label wording.
- `findings` is non-empty only for `COMPLETE_WITH_FINDINGS`, and empty for the
  other two states.
- `error` is `null` unless `state` is `INCOMPLETE`, where it is
  `{"class": "...", "message": "..."}`. Never report INCOMPLETE because you
  found nothing.
- `severity` is one of `critical`, `high`, `should-fix`. There is no nit
  severity.
- `confidence` is an integer from 80 to 100.
- `evidence.path` is repo-relative, `line_start`/`line_end` are real 1-based
  lines in that file, and `excerpt` is verbatim text you actually read.
- `id` is a short stable slug describing the finding.
- **Every `repo_learnings` finding requires all four `knowledge_base` fields** —
  `source` (repo-relative path to the pattern file), `pattern` (the entry id),
  `detect` (the condition you evaluated), and `quote` (a **verbatim** span of
  the entry). If you cannot produce all four, drop the finding.
- Never emit `repo_rule` — that key belongs to the `repo_code` role and
  including it invalidates your result.
- Emit no key that is not shown above.

If you found nothing that clears the bar, that is a good outcome — report it
honestly:

```json
{
  "contract": "lfx-local-review/v1",
  "kind": "review-result",
  "role": "repo_learnings",
  "state": "COMPLETE_NO_FINDINGS",
  "findings": [],
  "error": null
}
```
