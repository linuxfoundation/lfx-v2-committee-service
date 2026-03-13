---
name: preflight
description: >
  Pre-PR validation — format, lint, license headers, tests, build, and
  protected file check. Use before submitting any PR, to check if code is
  ready, validate changes, or verify a branch is clean and ready for review.
allowed-tools: Bash, Read, Glob, Grep, AskUserQuestion
---

# Pre-Submission Preflight Check

You are running a comprehensive validation before the contributor submits a pull request. Run each check in order, report results clearly, and help fix any issues found.

## Check 0: Working Tree Status

Before running any validation, understand what has changed:

```bash
git status
git diff --stat origin/main...HEAD
git log --format="%h %s%n%b" origin/main...HEAD
```

**Evaluate:**
- **Uncommitted changes?** — Ask the contributor: should we commit them now, or are they intentionally unstaged?
- **No commits ahead of main?** — The branch has nothing to validate. Ask if they're on the right branch.
- **Commit messages missing JIRA ticket?** — Flag commits that don't include `LFXV2-` references.
- **Commits missing `Signed-off-by`?** — Flag any commits without this line (visible in the full log output above).

Resolve any blockers before proceeding.

## Check 1: Code Formatting

```bash
make fmt
```

This formats all Go files to the project's standard style. It modifies files in place.

> **Why do this first:** Formatting issues would otherwise show up as lint errors, creating noise. Formatting first cleans those up automatically.

If files were modified, check what changed:
```bash
git diff --stat
```

If formatting changed files, remind the contributor to commit those changes before submitting the PR.

## Check 2: License Headers

```bash
make license-check
```

Every source file (`.go`, `.html`, `.txt`) must start with:

```
// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT
```

If any files are missing headers, add them to the top of each file before the package declaration:

```go
// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package yourpackage
```

## Check 3: Linting

```bash
make lint
```

The linter checks for code quality issues: unused variables, missing error handling, stylistic problems, and more. Fix any errors reported.

Common issues and fixes:
- **"declared and not used"** — Remove the unused variable or use it
- **"error return value not checked"** — Add `if err != nil { ... }` handling
- **Formatting issues** — Run `make fmt` again (should have been caught in Check 1)

### Re-validate after fixes

If you fixed anything in Checks 1–3, re-run lint to confirm the fixes are clean:
```bash
make lint
```

## Check 4: Tests

```bash
make test
```

All tests must pass. If tests fail:
- Read the failure output carefully — it usually points to the exact file and line
- Check if you changed something that broke an existing test
- If you added new code, check that there are tests covering it

If adding new tests, run them first to confirm they pass:
```bash
go test -v ./internal/service/...   # example: test a specific package
```

## Check 5: Build Verification

```bash
make build
```

The service must compile without errors. Build failures typically mean:
- Type errors (wrong type passed to a function)
- Missing imports (forgot to import a new package)
- Interface not fully implemented (added a method to a port but didn't implement it in the NATS layer or mock)

If you recently ran `make apigen` (after modifying design files), make sure you've implemented any new handler methods that Goa expects.

## Check 6: Protected Files

Check that no infrastructure files were accidentally modified:

```bash
git diff --name-only origin/main...HEAD
```

**Flag changes to any of these files** — they should NOT be modified without code owner approval:

- `cmd/committee-api/main.go`
- `cmd/committee-api/http.go`
- `internal/middleware/`
- `internal/infrastructure/auth/`
- `internal/infrastructure/nats/client.go`
- `charts/`
- `.github/workflows/`
- `Makefile`
- `go.mod` / `go.sum`
- `Dockerfile`

If protected files appear in the diff, ask the contributor whether those changes were intentional. If they were accidental, help them revert just those files:
```bash
git checkout origin/main -- <path/to/protected/file>
```

## Check 7: Commit Verification

Verify all changes are properly committed and follow conventions:

```bash
git status
git log --format="%h %s%n%b" origin/main...HEAD
```

**For each commit, verify:**
- Message format: `type(LFXV2-<number>): short description`
  - Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`
- Has `Signed-off-by: Name <email>` line (the `-s` flag in git commit adds this)
- References a JIRA ticket

If the last commit is missing sign-off and hasn't been pushed yet, it can be amended:
```bash
git commit --amend -s --no-edit
```

## Check 8: Change Summary

Generate a summary for the PR description:

```bash
git diff --stat origin/main...HEAD
```

List:
1. **New files** — what they do
2. **Modified files** — what changed
3. **New endpoints** — HTTP method + path
4. **Domain model changes** — any new or modified structs
5. **How to test manually** — curl command or steps to verify the feature works

## Results Report

Present a clear pass/fail summary:

```
PREFLIGHT RESULTS
─────────────────────────────────
✓ Working tree        — Clean, N commits ahead of main
✓ Formatting          — Applied / Already clean
✓ License headers     — All files have headers
✓ Linting             — No errors
✓ Tests               — All passed
✓ Build               — Succeeded
✓ Protected files     — None modified
✓ Commits             — Conventions followed, signed off
─────────────────────────────────
READY FOR PR
```

Or with issues:

```
PREFLIGHT RESULTS
─────────────────────────────────
✓ Working tree        — Clean, N commits ahead of main
✓ Formatting          — Applied
✓ License headers     — All files have headers
✗ Linting             — 2 errors (see above)
✗ Tests               — 1 failure (see above)
✓ Build               — Succeeded
✓ Protected files     — None modified
✓ Commits             — Conventions followed, signed off
─────────────────────────────────
ISSUES FOUND — Fix before submitting
```

## If All Checks Pass

Offer to create the PR:

> "All preflight checks passed! Ready to create a PR. Would you like me to create it with `gh pr create`?"

When creating the PR, include in the description:
- What the change does (from the change summary)
- How to test it manually
- Reference to the JIRA ticket
