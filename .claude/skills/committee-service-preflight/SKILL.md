---
# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT
name: committee-service-preflight
description: >
  Mechanical pre-PR validation for lfx-v2-committee-service. Checks working
  tree state, license headers, Go formatting, golangci-lint, API and CLI build,
  Go tests, repo-specific protected files, commit verification, and PR change
  summary. Run after /committee-service-pr-readiness has passed. Supports
  report-only or dry-run mode when requested.
allowed-tools: Bash, Read, Glob, Grep, Edit, Write, AskUserQuestion
---

# Committee Service Preflight

You are running the mechanical pre-PR pipeline for `lfx-v2-committee-service`.
Every check here is shell-driven or repository-boundary-driven. Do not perform a
general code review and do not duplicate central LFX topology or platform
architecture guidance.

Run each check in order, report results clearly, and help fix mechanical issues
when the mode allows it.

## Modes

- **Default:** run validation and apply mechanical fixes where the repo already
  provides a safe command (`make fmt`, license-header insertion for new files).
- **`--dry-run`**, **`--report-only`**, or **"report only"**: report what would
  be changed, but do not modify files and do not run generators that rewrite
  output.

Args format: `[base-branch] [mode] [extra instructions]`.

- Default base: `origin/main`.
- If the base has no slash, normalize it to `origin/<base>`.
- `/committee-service-pr-readiness` should already have passed. If it has not,
  stop and run that shape check first unless the user explicitly says to skip
  it.

## Check 0 - Working tree status

Run:

```bash
git status --short
git diff --stat <base>...HEAD
git diff --name-only <base>...HEAD
git log --format="%h %s%n%b" <base>..HEAD
```

Evaluate:

- **Uncommitted changes** - ask whether to commit, stash, or include them in
  this preflight. Do not silently ignore them.
- **No commits ahead of base** - stop and ask whether the contributor is on the
  intended branch.
- **Commit messages missing JIRA ticket** - flag commits without `LFXV2-` in
  subject or body.
- **Commits missing signoff** - flag commits without `Signed-off-by:` trailers.
- **Unexpected dirty tree from previous tooling** - report before continuing.

## Check 1 - License headers

Run:

```bash
make license-check
```

Required header for new Go files:

```go
// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT
```

This repo's `Makefile` excludes `gen/`, `vendor/`, and `megalinter-reports/`
from the basic license check. The PR workflow also excludes generated Goa
output. Do not add headers to generated files under `gen/`.

In default mode, add missing headers only when the correct comment style is
obvious from nearby files. In dry-run mode, report the files and exact header
needed.

## Check 2 - Go formatting

Default mode:

```bash
make fmt
```

Dry-run mode:

```bash
gofmt -l $(git ls-files '*.go' ':!:gen/*' ':!:vendor/*')
```

If formatting changes files in default mode, report the changed paths and rerun
the dry-run `gofmt -l` check until it is empty.

## Check 3 - Linting

Run:

```bash
make lint
```

This uses `golangci-lint` pinned by `Makefile`. If the tool is missing, the
Makefile may install it. If install or lint fails, report the exact failure and
fix only clear mechanical issues such as unused imports introduced by the
current change.

## Check 4 - Build verification

Run:

```bash
make build
make build-cli
```

The API and committee CLI both need to build. If build output points to a
generated-code mismatch and `cmd/committee-api/design/**` changed, run
`make apigen` in default mode, then rerun both builds. In dry-run mode, report
that `make apigen` is required without running it.

## Check 5 - Tests

Run:

```bash
make test
```

This wraps `go test -v -race -coverprofile=coverage.out ./...`. If it fails,
report the package and failing test. Fix only issues clearly caused by the
current mechanical changes; otherwise stop and surface the failure.

## Check 6 - Protected files

Use:

```bash
git diff --name-only <base>...HEAD
```

Flag changes to this repo-specific protected list:

- `cmd/committee-api/design/**` - Goa API design; requires `make apigen` and
  matching generated output when behavior changes.
- `gen/**` - generated Goa output; should only change from `make apigen`.
- `charts/lfx-v2-committee-service/**` - service-local Helm and runtime
  resources.
- `go.mod`, `go.sum` - dependency graph and checksums.
- `Makefile` - build, lint, generation, and test command behavior.
- `CLAUDE.md` - repo workflow guidance.
- `.claude/skills/**` - repo-local skill behavior.
- `docs/indexer-contract.md`, `docs/fga-contract.md`,
  `docs/invite-application-flows.md` - emitted-message contracts and
  invite/application state-machine docs.

Additional protected-file rules:

- If `cmd/committee-api/design/**` changed, `gen/**` should usually change too.
- If `gen/**` changed without `cmd/committee-api/design/**`, verify the PR
  explains why generated output changed alone.
- If committee-owned emitted events, FGA tuples, invite/application behavior,
  links, folders, or documents changed, the matching contract doc must be
  updated in the same PR.

## Check 7 - Commit verification

Run:

```bash
git status --short
git log --format="%G? %h %s%n%b" <base>..HEAD
```

Verify:

- All intended changes are committed, or uncommitted changes are explicitly
  called out.
- Commit subjects follow conventional commit shape: `type(scope): description`
  or `type: description`.
- Every commit has `Signed-off-by:`.
- Every commit has a good GPG signature (`%G?` is `G`) or the report calls out
  the exact unsigned/bad-signature commits.
- Commit subjects or bodies reference `LFXV2-<digits>` unless the user has
  explicitly identified non-ticketed work.

## Check 8 - Change summary

Generate:

```bash
git diff --stat <base>...HEAD
git diff --name-status <base>...HEAD
```

Summarize for the PR body:

1. **Goa/API changes** - design files, service adapters, or generated output.
2. **Domain/service changes** - `internal/domain/`, `internal/service/`,
   `internal/infrastructure/`, `pkg/`.
3. **CLI changes** - `cmd/committee-cli/`.
4. **Contract docs** - `docs/indexer-contract.md`, `docs/fga-contract.md`,
   `docs/invite-application-flows.md`.
5. **Chart changes** - `charts/lfx-v2-committee-service/`.
6. **Dependencies/build workflow** - `go.mod`, `go.sum`, `Makefile`.
7. **Repo guidance** - `CLAUDE.md` or `.claude/skills/**`.

## Results report

Render:

```text
PREFLIGHT RESULTS
-----------------
PASS Working tree     - Clean, N commits ahead of origin/main
PASS License headers  - make license-check passed
PASS Formatting       - make fmt clean
PASS Linting          - make lint passed
PASS Build            - API and CLI builds passed
PASS Tests            - make test passed
PASS Protected files  - docs/indexer-contract.md touched; called out for PR body
PASS Commits          - Conventional, signed off, GPG signed
-----------------
READY FOR PR
```

If there are issues:

```text
PREFLIGHT RESULTS
-----------------
PASS Working tree     - Clean, N commits ahead of origin/main
PASS License headers  - make license-check passed
FAIL Formatting       - 2 Go files need gofmt
FAIL Linting          - golangci-lint failed in internal/service
PASS Build            - API and CLI builds passed
PASS Tests            - make test passed
WARN Protected files  - go.mod/go.sum changed; explain dependency reason
PASS Commits          - Conventional, signed off, GPG signed
-----------------
NOT READY - Fix failed checks before submitting
```

Verdict rules:

- **NOT READY** - any failed working-tree, license, format, lint, build, test,
  or commit-verification check.
- **READY WITH CALLOUTS** - mechanical checks pass, but protected files or
  contract-sensitive paths need PR-body explanation.
- **READY FOR PR** - all checks pass and no protected-file callouts remain.

## Scope boundaries

This skill does:

- Check working tree state, headers, formatting, lint, builds, tests, protected
  files, commits, and PR summary.
- Run `make fmt` and safe header fixes in default mode.
- Run `make apigen` only when Goa design changed and default mode allows file
  rewrites.

This skill does not:

- Perform a broad code review.
- Hand-edit `gen/**`.
- Decide cross-repo ownership or platform architecture.
- Create or post the PR unless the user explicitly asks after a clean report.
