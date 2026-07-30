<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Logging, PII, and redaction patterns

Local empirical patterns for what reaches the logs. These apply to almost any
Go change in this repo.

**Read when** the patch touches any `.go` file that logs or builds an error.

---

## `logging-errors-secrets/redaction-removed-or-missing` — High

**Pattern:** three shapes the legacy `pii-in-logs` entry does not cover, each
raised and fixed in this window. A diff **removes** an existing redaction
wrapper; a rendered **email subject line** is logged as if it were metadata; or
a caller identifier is logged unredacted at the Goa handler layer.

This entry **extends** the legacy `logging-errors-secrets/pii-in-logs` rule
rather than replacing it. Match the legacy entry for a raw identifier appearing
in a log; match this one for a redaction being taken away, for an email subject,
or for `principal` at the handler layer.

**Detect:**

1. The diff deletes a `redaction.Redact` / `redaction.RedactEmail` wrapper from
   a log or error argument. A removed redaction is a finding on its own — the
   log line still compiles and now leaks.
2. A rendered email subject is logged. This is distinct from a NATS subject,
   which is safe: current notification subjects embed the recipient's resolved
   display name.
3. `principal` is logged without `redaction.Redact`, including in
   `cmd/committee-api/service/**` — the LFID username is PII wherever it
   appears.

**Two corrections to the legacy entry's Detect list**, recorded here because
that file is frozen: it names `.ApplicantUID`, but the model field carrying PII
is `.ApplicantEmail`; and it does not say whether
`internal/infrastructure/mock/**` is in scope. At the time of mining, the mock
layer was the *only* surface with raw-identifier hits — a full sweep of
`internal/`, `cmd/`, `scripts/`, and `pkg/` found three, all in
`internal/infrastructure/mock/committee.go`. Production code was clean.

**Evidence:** `copilot-pull-request-reviewer`.

- PR #152, thread `r3589754533`: commit `6f9c231` had *deleted*
  `redaction.RedactEmail` from send-success logs — *"This change removes the
  existing email redaction … the warning would leak that address; restore
  `RedactEmail`."* Restored in `a4ec014`.
- PR #152, thread `r3589685368`: *"`req.Subject` is not safe log metadata:
  current notification subjects embed a user's resolved display name."* Fixed
  in `a4ec014` by dropping the field. Verified in `main@bd39fe9`: no rendered
  subject is logged in `internal/infrastructure/nats/email_sender.go`.
- PR #156, thread `r3632379416`: *"This emits the caller's unredacted LFID
  username at info level."* Fixed.

**Why it earns a place:** recurrence across two PRs with three distinct shapes,
acted on every time, and the removal case is invisible to any rule that only
looks for identifiers appearing — it has to look for redaction disappearing.

**Failure message:** Redaction removed, or PII logged without it — a rendered
email subject, or an unredacted caller identifier.

**Fix:** restore or add the `pkg/redaction` wrapper; drop rendered email
subjects from log arguments entirely rather than redacting them.
