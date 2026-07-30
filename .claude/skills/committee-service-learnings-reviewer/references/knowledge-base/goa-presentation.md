<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Goa design and presentation patterns

Local empirical patterns for the Goa design layer and the handlers that adapt
it.

**Read when** the patch touches `cmd/committee-api/design/**`,
`cmd/committee-api/service/**`, or `cmd/committee-api/http.go`.

---

## `goa-presentation/url-scheme-allowlist` — Critical

**Pattern:** a URL-bearing design attribute validates shape but not scheme.
`dsl.FormatURI` accepts any URI scheme, and a `Pattern` whose `https?://`
prefix is optional lets the scheme fall through to the permissive tail. A
`javascript:` URI then satisfies validation, is persisted, and is returned to
the UI as a link.

**Detect:** a new or changed URL-bearing attribute in
`cmd/committee-api/design/**` that relies on `dsl.Format(dsl.FormatURI)` alone,
or declares a `Pattern` in which the scheme prefix is optional — an `(https?://)?`
group is the tell. Require the shared `urlPattern`
(`cmd/committee-api/design/type.go:208`) or an equivalent explicitly anchored
`^https?://`.

**Evidence:** `copilot-pull-request-reviewer`, PR #149, thread `r3560697597`,
on `cmd/committee-api/design/type.go:220`: *"`FormatURI` accepts non-HTTP URI
schemes, and this pattern does not close that gap because its optional prefix
lets the scheme be consumed by the fallback. For example, `javascript:alert(1)`
matches the regex and is a valid URI, so it can be persisted and returned as a
repository link."*

Fixed in `91e89f5`, which changed
`^(https?://)?[^\s/$.?#].[^\s]*$` to `^https?://[^\s/$.?#][^\s]*$` and added
`TestURLPattern` covering the rejected schemes. Verified in `main@bd39fe9` at
`cmd/committee-api/design/type.go:208` and
`cmd/committee-api/design/type_test.go:11`.

**Why it earns a place:** cost of miss is a stored-XSS vector — a persisted
`javascript:` URI rendered as a link reaches the self-serve UI — and the fix
landed with a regression test, which is the strongest acted-on signal
available.

**Failure message:** URL attribute accepts non-HTTP(S) schemes; a
`javascript:` URI passes validation and can be persisted and rendered as a link.

**Fix:** use the shared `urlPattern` from `cmd/committee-api/design/type.go`,
or anchor the scheme explicitly with `^https?://`, and add the rejected schemes
to `TestURLPattern`.
