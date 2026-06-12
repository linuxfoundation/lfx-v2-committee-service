---
# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT
name: committee-service-pr-readiness
description: >
  Pre-PR shape check for local lfx-v2-committee-service work. Audits branch
  name, JIRA reference, conventional commits, rebase status, DCO and GPG
  signing, total diff size, and repo-specific protected files against the
  target base branch. Does not audit Go code, generated output correctness,
  contracts, charts, or tests; run /committee-service-preflight after this
  shape check passes.
context: fork
allowed-tools: Bash, Read, Glob, Grep
---

# Committee Service PR Readiness

You are checking whether **local commits are shaped correctly to open as a PR**
for `lfx-v2-committee-service`.

This skill is a PR-shape check only. Do not review implementation quality, Goa
design correctness, generated code, contract content, chart behavior, or test
coverage here. Those mechanical checks belong to `/committee-service-preflight`;
implementation guidance belongs to `committee-service-dev`.

**Output:** structured shape report with verdict `NOT READY`,
`READY WITH CHANGES`, or `READY`. No git mutations, no PR side effects.

## Phase 1 - Parse arguments

Args format: `[base-branch] [extra instructions]`.

- First token, if it looks like a ref or branch name, is the base branch.
- Default base: `origin/main`.
- If the base has no slash, normalize it to `origin/<base>` before comparing.
- Treat all remaining text as explanatory focus; do not expand the audit scope.

## Phase 2 - Gather shape inputs

Run:

```bash
git fetch origin
git rev-parse --abbrev-ref HEAD
git diff --shortstat <base>...HEAD
git diff --name-only <base>...HEAD
git log --format='%H %s' <base>..HEAD
git log --format='%G? %h %s' <base>..HEAD
git log --format=%B <base>..HEAD
git merge-base --is-ancestor <base> HEAD; echo $?
```

If there are no commits between `<base>` and `HEAD`, stop with:

```text
No commits to audit against <base> - make at least one commit on this branch.
```

## Phase 3 - Protected-file shape check

Build the protected-file result by intersecting `git diff --name-only
<base>...HEAD` with this repo-specific protected list:

- `cmd/committee-api/design/**` - Goa API design; must be intentional and must
  be paired with regenerated output when behavior changes.
- `gen/**` - Goa-generated code; must come from `make apigen`, never hand edits.
- `charts/lfx-v2-committee-service/**` - service-local deployment config.
- `go.mod`, `go.sum` - dependency graph and checksums.
- `Makefile` - build, lint, generation, and test command source of truth.
- `CLAUDE.md` - repo workflow guidance.
- `.claude/skills/**` - repo-local skill behavior.
- `docs/indexer-contract.md`, `docs/fga-contract.md`,
  `docs/invite-application-flows.md` - committee-owned emitted-contract and
  state-machine docs.

Protected files do not automatically block a PR. Flag them so the PR body can
explain intent and request the right reviewer attention. A `gen/**` change
without a matching `cmd/committee-api/design/**` change is a blocker unless the
extra instructions explain a generated-only repair.

## Phase 4 - Shape checks

Produce at most one finding per check:

```json
{
  "severity": "CRITICAL | SHOULD_FIX | NIT",
  "rule": "committee-service-pr-shape/<item-id>",
  "message": "...",
  "suggestion": "..."
}
```

Checks:

- **Branch name** - branch should include an `LFXV2-<digits>` ticket or be an
  explicit maintenance branch (`main`, `release/*`, `hotfix/*`).
- **JIRA ticket** - commit subjects or bodies should include `LFXV2-<digits>`.
  Missing ticket is `SHOULD_FIX` unless the work is explicitly non-ticketed.
- **Conventional commits** - every commit subject should match
  `type(scope): description` or `type: description`; common types are `feat`,
  `fix`, `docs`, `test`, `refactor`, `chore`, `build`, and `ci`.
- **Branch rebased** - `git merge-base --is-ancestor <base> HEAD` should return
  `0`. If not, mark `SHOULD_FIX`.
- **DCO and GPG** - every commit should have a `Signed-off-by:` trailer and a
  good signature (`%G?` is `G`). Missing signoff is `CRITICAL`; missing or bad
  GPG signature is `CRITICAL` unless the repo policy has been waived in the
  extra instructions.
- **Diff size** - summarize additions/deletions. More than 800 additions is
  `SHOULD_FIX`; more than 1500 additions is `CRITICAL` unless the diff is
  mostly generated output from `make apigen`.
- **Protected files** - report every protected path touched and why it matters.

## Phase 5 - Cross-check discipline

- Every finding must be backed by Phase 2 output.
- Do not infer code quality from filenames.
- Do not suggest implementation fixes; only suggest shape fixes such as rename
  branch, amend commit message, rebase, sign commits, split PR, or document
  protected-file intent in the PR body.

## Phase 6 - Render the report

```markdown
# Committee Service PR Readiness

**Branch:** `<current-branch>` -> `<base>`
**Commits:** N | **Additions:** +A | **Deletions:** -D
**Verdict:** NOT READY | READY WITH CHANGES | READY

## PR-shape sanity

| Check | Status | Detail |
| --- | --- | --- |
| Branch name | PASS | feat/LFXV2-1234-committee-links |
| JIRA ticket | PASS | Found LFXV2-1234 in commits |
| Conventional commits | PASS | All commits valid |
| Branch rebased | PASS | origin/main is an ancestor |
| Diff size | PASS | 342 additions |
| DCO + GPG signing | PASS | 3/3 commits signed and signed off |
| Protected files | SHOULD_FIX | docs/indexer-contract.md touched; explain contract update |

## Verdict reasoning

<one line per CRITICAL or SHOULD_FIX finding>
```

Verdict rules:

- **NOT READY** - any `CRITICAL` finding.
- **READY WITH CHANGES** - zero `CRITICAL`; one or more `SHOULD_FIX` findings.
- **READY** - zero `CRITICAL`, zero `SHOULD_FIX`.

## Companion skills

- `/committee-service-preflight` - mechanical Go preflight. Run after this
  shape check passes.
- `committee-service-dev` - repo-local implementation conventions for Go, Goa,
  NATS, contracts, charts, logging, errors, and tests.
