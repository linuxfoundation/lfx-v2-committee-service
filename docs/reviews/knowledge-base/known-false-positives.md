# Known false positives — applied LAST in every review pass

Findings that match any pattern below MUST be dropped, regardless of which source (rule file, checklist,
pattern file) originally produced them. This list is the floor — even a quotable pattern doesn't survive if
it matches a known false positive.

Used by the `lfx-skills:lfx-committee-service-learnings-reviewer` subagent (Step 4), and relevant filter
discipline for the `lfx-skills:lfx-committee-service-code-reviewer` subagent.

---

## Already-enforced-by-tooling

### License-header complaints on a file that has one

**Pattern matched:** finding states a `.go` file is missing the MIT license header.

**Why false:** the Makefile `license-check` target and the `github-license-compliance` bot already enforce headers; Goa-generated files under `gen/` are intentionally excluded. If CI is green the header is present (or correctly exempt). Bots occasionally misread it.

**Source:** `Makefile` `license-check`; SKILL.md "License header on every new `.go` file (Goa-generated files are excluded by the Makefile's `license-check`)".

### gofmt / goimports / golangci-lint style nits

**Pattern matched:** import-block blank lines, gofmt spacing, unchecked-by-the-team style lints, "run gofmt" suggestions (e.g., PR #57 `otel_test.go:8` extra blank line in imports).

**Why false:** `make fmt` (gofmt + goimports) and `make lint` (golangci-lint pinned in the Makefile) run in preflight/CI and own this. Surfacing a formatting-only nit is duplicate signal.

**Source:** `Makefile` `fmt`/`lint`; SKILL.md "Formatting, linting, headers".

### Doc table / markdown rendering nits (`||` leading double-pipe, fenced-block language)

**Pattern matched:** "tables start with `||` and render an extra empty column" or "add a language identifier to the fenced code block" on `docs/**` / `README.md` / `.md`.

**Why false:** these are cosmetic markdown-lint nits the team accepts; they recur on every docs PR (#70, #75, #87, #64) and are not acted on as review blockers. They do not change emitted behavior. (Substantive contract-accuracy findings on the same docs — wrong optionality, missing object type — are NOT false positives; those route to `indexer-fga-contracts/contract-doc-out-of-sync`.)

---

## Generic correctness owned by the general reviewer

### Generic nil-check / add-a-test / rename / comment-wording without a repo contract

**Pattern matched:** a bare "add a nil check", "add a unit test", "this comment should be capitalized / end with a period", "rename this variable", or "extract a helper" finding that does not tie to a committee-service contract, the `pkg/errors`/`pkg/redaction`/`pkg/constants` conventions, or a flow/chart-coupling rule.

**Why false:** generic senior-review intuition is owned by `lfx-skills:lfx-general-code-reviewer`. The learnings KB only ships findings that quote a repo-specific pattern entry. (Note: a nil-deref that panics on a Goa payload pointer IS in the KB — `goa-presentation/nil-nil-stub-or-deref` — so quote that entry when it applies; a generic nil-check elsewhere is not.)

**Source:** the committee-service code-reviewer / general-reviewer scope split; playbook §2 hard gate "Repo-specific, not generic."

---

## Review-automation quirks

### CodeRabbit `🏁 Script executed:` reconnaissance dumps and `> ‼️ IMPORTANT` banners

**Pattern matched:** any text quoting a CodeRabbit `🏁 Script executed:` block, a `> ‼️ IMPORTANT` collapsed banner, or its internal `wc`/`grep` verification output.

**Why false:** this is CodeRabbit's internal reasoning, not a finding. Surfacing it is noise.

### Copilot "Add custom instructions" / promotional CTA and PR-description-vs-design "drift"

**Pattern matched:** the trailing "Improve your code reviews — add custom instructions" CTA; or "PR title/description says X but the code does Y" scope-mismatch comments (e.g., PR #75/#76/#78/#84 "update the PR description").

**Why false:** the CTA is promotional. PR-description-scope comments are process notes for the author at PR time, not code defects the learnings reviewer should re-raise on a local diff — there is no PR description in a pre-PR commit review. (If the *contract doc* is out of sync with the code, that IS a finding — route to `indexer-fga-contracts/contract-doc-out-of-sync`.)

### Dependency / toolchain version speculation

**Pattern matched:** "Go version 1.24/1.25 does not exist", "verify this OpenTelemetry/golangci-lint version exists", "the latest released version is vX.Y.Z".

**Why false:** these are point-in-time bot guesses (e.g., PR #1 "Go 1.24 does not exist", PR #55 OTel version notes) that go stale immediately and are governed by `go.mod` + the Makefile-pinned toolchain, which CI validates. Not a durable review pattern.

---

## Chart-replica defaults

### KV bucket / deployment `replicas` default flagged as wrong for local clusters

**Pattern matched:** "defaulting KV bucket `replicas` to 3 prevents creation on single-node/local clusters" or "replicaCount changed from 3 to 1 affects all environments".

**Why false (conditional):** the team intentionally sets HA-oriented defaults (3) in the committed chart and overrides to 1 locally via `values.local.yaml`; deployed values live in `lfx-v2-argocd`. The bots raise this on most chart PRs (#63, #64) and the resolution is environment override, not a code change. Only valid if a PR demonstrably lowers the committed production default without an override path — author's call.

---

## How to add a new entry

When the bots (CodeRabbit `coderabbitai`, Copilot login `Copilot`/`copilot-pull-request-reviewer`) or a
human reviewer surface a finding the team has explicitly decided is not relevant for this repo:

1. Add an entry here with **Pattern matched**, **Why false**, and (where applicable) **Source**.
2. If the pattern was previously in a category `<file>.md`, remove it there — don't keep a pattern in both.
3. Permanent bot quirks (CTA text, script dumps) are durable entries; one-off misreads need no entry.

This file should accumulate slowly. If it grows past ~40 entries, that's a signal the KB is too permissive —
re-audit.
