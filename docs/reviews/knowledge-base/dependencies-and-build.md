# Dependency Declaration & Version Consistency

Patterns about what the repo *declares* it depends on — `go.mod` version selection and versioned import
paths — as opposed to how the code uses those dependencies. Both shapes below are mechanically checkable
from the declarations themselves, with no runtime reasoning.

**Read when:** `go.mod` or `go.sum` changed, **or** any `.go` file changed that imports a module whose
version is part of its import path (notably `go.opentelemetry.io/otel/semconv/vX.Y.Z`).

These two entries were previously recorded as "routed to the mechanical gates" and therefore excluded from
this KB. That routing was wrong: `/committee-service-pr-readiness` is a PR-shape check that treats `go.mod`
only as a protected path and does not audit code, and `/committee-service-preflight` builds, formats and lints
but never compares declared versions across importers. Neither performed these checks, so both findings were
being lost rather than owned. They are empirical findings from merged PRs and they live here.

---

## `dependencies-and-build/pseudo-version-pins-unreleased-commit` — Important

**Pattern:** a `go.mod` requirement pins an untagged commit through a pseudo-version even though a real
release tag exists at or before that commit — most often a first-party `github.com/linuxfoundation/*` module
pinned mid-development and never moved to the tag once it shipped.

**Detect — two steps, and the second is mandatory.**

1. In `go.mod`, find a version matching `v<major>.<minor>.<patch>-0.<14-digit timestamp>-<12-hex>` where the
   base version is **not** `v0.0.0` — for example `v0.1.10-0.20260716124858-8a4848f0064c`.
2. **Verify independently that a release tag at or above that version actually exists.** Run
   `go list -m -versions <module>` (or `git ls-remote --tags <repo>`) and check for a real `v0.1.10` or later.
   Report only if such a tag exists.

**Do not infer tag existence from the version string — the string does not carry it.** Go builds a
pseudo-version from the *latest tag reachable from the commit*, incrementing the patch: a commit after tag
`v0.1.9` is named `v0.1.10-0.<timestamp>-<sha>` **whether or not `v0.1.10` was ever tagged**. So the `v0.1.10`
in that string is a sort key derived from `v0.1.9`, not a claim that `v0.1.10` is released. Skipping step 2
flags every legitimate pin to an unreleased commit and tells the developer to switch to a tag that does not
exist — a fabricated remedy, which is worse than missing the finding.

Treat `github.com/linuxfoundation/*` requirements as the highest-value case, since those releases are cut by
this org and a pin usually just outlived its reason — but they get step 2 like everything else.

**Empirical citation:** PR #153 — the requirement
`github.com/linuxfoundation/lfx-v2-invite-service v0.1.10-0.20260716124858-8a4848f0064c`, fixed in `fa3044e`
("chore(deps): upgrade lfx-v2-invite-service to released v0.1.10") by replacing it with the plain tag
`v0.1.10`. The fix is one line in `go.mod` plus the matching `go.sum` update.

**Failure message:** `go.mod` pins an untagged commit via pseudo-version although the release tag it is
derived from already exists.

**Fix:** require the released tag (`v0.1.10`) and refresh `go.sum` — `go get <module>@v<tag>` then
`go mod tidy`.

**Boundary — do not flag these.** A `v0.0.0-<timestamp>-<sha>` pseudo-version is the *correct and only* way
to depend on a module that has never tagged a release, and this repo legitimately carries several at
`main@bd39fe9`: `github.com/akamensky/base58`, `github.com/dimfeld/httppath`, `github.com/manveru/faker`, and
both `google.golang.org/genproto/googleapis/{api,rpc}`. The `v0.0.0-` prefix is the discriminator — it says no
tag exists to prefer. Equally, a deliberate pin to an unreleased commit is legitimate when the change depends
on an unshipped fix; that is a finding only if nothing explains it, and an explanation in the PR or a `go.mod`
comment settles it.

---

## `dependencies-and-build/versioned-import-path-drift` — Important

**Pattern:** the same dependency is imported at two different versions in the same module, because the version
lives in the import path and each importer carries its own copy. The build succeeds — they are distinct
packages — so nothing catches it except reading both files.

**Detect:** collect every import path of the form `go.opentelemetry.io/otel/semconv/v<X>.<Y>.<Z>` across
changed and unchanged `.go` files; if more than one distinct version appears in the module, that is a finding.
Reproduce with `git grep -n 'otel/semconv/v' -- '*.go'`. The same rule applies to any dependency that encodes
its version in the import path, not only semconv.

**Empirical citation:** fixed in `ca05b3e` ("fix(review): add CustomClaims test assertions and standardize
semconv version"), which moved `cmd/committee-api/http.go` from
`semconv "go.opentelemetry.io/otel/semconv/v1.40.0"` to `v1.41.0`, matching `pkg/utils/otel.go`, which was
already on `v1.41.0`.

**Failure message:** the same versioned dependency is imported at two different versions across the module.

**Fix:** standardize every importer on one version — normally the highest already present in the tree — and
change the import paths together in one commit.

**Currently non-generating, kept as an active rule.** At `main@bd39fe9` both importers agree
(`cmd/committee-api/http.go` and `pkg/utils/otel.go` are both `v1.41.0`), so this rule emits nothing against
the current tree. It stays active because the drift is invisible to the compiler and to lint, recurs whenever
one importer is bumped alone, and costs a real inconsistency in emitted telemetry attributes.

**Boundary:** an intentional straddle — one importer held back because a dependency requires the older
attribute set — is a finding only if it is undocumented. A comment naming the constraint resolves it.
