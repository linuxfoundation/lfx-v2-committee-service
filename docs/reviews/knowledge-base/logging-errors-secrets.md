# Logging, Typed Errors & Secret Handling

Patterns around this repo's logging discipline (PII redaction via `pkg/redaction`, no secret/URL leakage),
the `pkg/errors` typed-error family, and silent-failure tells. Security/data patterns — promoted at a
single occurrence.

**Read when:** any `.go` file that logs (`slog.*Context`, `log.*`), returns errors, or builds error
messages — especially `internal/service/**`, `internal/infrastructure/nats/**`,
`cmd/committee-api/service/**`, `cmd/committee-cli/**`, and `scripts/migrations/**`. **Also read on any
change under `charts/lfx-v2-committee-service/**` (notably `values.yaml`)**, because
`secrets-in-logs-or-charts` below inspects both `values.yaml` and the chart templates for inline secrets — a
chart-only change would otherwise never reach that half of the rule.

---

## `logging-errors-secrets/pii-in-logs` — Critical

**Pattern:** a raw email address or username (Auth0 sub / LFID) is logged or interpolated into an error
message without `redaction.RedactEmail` / `redaction.Redact`. The committee member, invite, subscriber,
and notification flows all redact identifiers; new code on those paths must too.

**Three further shapes** were raised and fixed in the 2026-07 window and are part of this entry:

1. **Redaction removed.** The diff deletes a `redaction.Redact` / `redaction.RedactEmail` wrapper from a log
   or error argument. This is a finding on its own — the line still compiles and now leaks.
2. **Rendered email subject logged.** Distinct from a NATS subject, which is safe: current notification
   subjects embed the recipient's resolved display name.
3. **`principal` logged unredacted at the Goa handler layer**, including in `cmd/committee-api/service/**` —
   the LFID username is PII wherever it appears.

**Detect:** grep changed files for `slog.*Context(` calls and `errors.New*`/`fmt.Errorf` whose args include
`.Email`, `.InviteeEmail`, `.ApplicantEmail`, `.Username`, `principal`, or a recipient email, without a
`redaction.` wrapper. Then check the three shapes above — in particular, read the diff for a `redaction.`
wrapper *disappearing*, which no "identifier appears in a log" rule can catch.

**Scope includes `internal/infrastructure/mock/**`.** At the time of mining the mock layer was the only
surface with raw-identifier hits: a full sweep of `internal/`, `cmd/`, `scripts/`, and `pkg/` found three,
all in `internal/infrastructure/mock/committee.go` (`:668`, `:769`, `:779`). Production code was clean.

**Empirical citation:** PR #16 `cmd/committee-api/service/committee_service.go:232` (CodeRabbit) — "Avoid logging PII (email/username) at request handling — redact or remove." Recurs PR #91 `committee_subscriber.go:47` ("Remove raw user identifiers from logs"), PR #91 `message_handler.go:513` ("Remove recipient email addresses from logs"), PR #44 `messaging_request.go:53` (Copilot, "The error message exposes the email address in plain text ... use `redaction.RedactEmail()`"), PR #61 `committee_application.go:50` ("Redact `applicant_uid` before logging it").

**Revised 2026-07-30 — Detect corrected and three shapes added.** `pkg/redaction.Redact`/`RedactEmail` are
used pervasively — **120 matching lines at `main@bd39fe9`** across `cmd/`, `internal/`, `pkg/` and
`scripts/`, which is **121 call expressions**: `cmd/committee-api/service/committee_service.go:1750` carries
both `RedactEmail` and `Redact` on one line. Reproduce with
`git grep -n 'redaction\.Redact\|redaction\.RedactEmail' bd39fe9 -- cmd internal pkg scripts | wc -l`; the
command counts *lines*, not calls, and needs no `_test.go` filter because that path set contains no test-file
matches. The PR #16 site is fixed (`committee_service.go:69-75` logs only `len(token)`).
The point of the number is "this is the house convention, not an occasional courtesy"; re-run the command
rather than trusting the figure if you need it exact. **Corrected 2026-07-31: previously stated as 111.**
Two corrections: the Detect list named `.ApplicantUID`, but the model field carrying PII is
`.ApplicantEmail`; and the entry did not state whether the mock layer was in scope — it is, and it was the
only surface with hits.

New shapes, all `copilot-pull-request-reviewer`, all acted on:

- PR #152 thread `r3589754533` — commit `6f9c231` had *deleted* `redaction.RedactEmail` from send-success
  logs: "This change removes the existing email redaction … the warning would leak that address; restore
  `RedactEmail`." Restored in `a4ec014`.
- PR #152 thread `r3589685368` — "`req.Subject` is not safe log metadata: current notification subjects embed
  a user's resolved display name." Fixed in `a4ec014` by dropping the field; verified at `main@bd39fe9` —
  no rendered subject is logged in `internal/infrastructure/nats/email_sender.go`.
- PR #156 thread `r3632379416` — "This emits the caller's unredacted LFID username at info level." Fixed.

**Failure message:** Raw email/username/principal logged or put in an error message without `pkg/redaction`, an existing redaction removed, or a rendered email subject logged.

**Fix:** wrap with `redaction.RedactEmail(...)` for emails and `redaction.Redact(...)` for usernames/subs/principals in both log fields and error message strings; restore any redaction the diff removed; and drop rendered email subjects from log arguments entirely rather than redacting them.

---

## `logging-errors-secrets/no-raw-secret-or-url` — Critical

**Pattern:** a raw NATS URL, bearer token, or secret is logged or embedded in an error message. Migration
scripts and the CLI repeatedly logged `NATS_URL`; connection-error messages embedded it. Also: a hardcoded
`Authorization` bearer in a migration script, or a secret committed directly into the chart — in `values.yaml`
or in a template under `charts/lfx-v2-committee-service/templates/**` — instead of `valueFrom`/keypair.

**Detect:** grep changed Go for `slog`/`fmt.Errorf` args or string concatenation containing a NATS URL
variable, `NATS_URL`, `Authorization`, `Bearer `, or token vars.

In chart changes, inspect **both** `values.yaml` **and** anything under
`charts/lfx-v2-committee-service/templates/**`:

- in `values.yaml`, flag a secret literal not sourced via `valueFrom`;
- in a template, flag a literal credential, token or password wherever it lands — an `env:` entry with a
  literal `value:` for a secret-shaped key, a `stringData:`/`data:` block with an inlined credential, or a
  URL with embedded credentials. A template can introduce an inline secret without `values.yaml` changing at
  all, so `values.yaml` alone is not the whole surface.

The fix in both cases is indirection: `valueFrom.secretKeyRef` (or an equivalent external reference), never a
literal committed to the chart.

**Empirical citation:** PR #78 `scripts/migrations/migrate_join_mode_to_base/main.go:65` (CodeRabbit) — "Do not log the raw NATS URL." (recurs `reindex_committees/main.go:88`, PR #89 `migrate_counsel_role/main.go:65`, PR #87 `cmd/committee-cli/main.go:90` "Don't include raw `NATS_URL` in connection errors"). Hardcoded token PR #78 `reindex_committees/main.go:45` (Copilot, "This hard-codes an Authorization header value ... read a bearer token from an env var"). Chart-secret PR #98 `providers.go:529` (jordane, "Best practice is to not use a secret here, but to use a keypair").

**Revised 2026-07-30 — citations repointed; the invariant is broadly violated today.** Both cited scripts
(`migrate_join_mode_to_base/`, `reindex_committees/`) have been deleted, so those citations no longer
resolve. The fixes that did land are narrow: `migrate_counsel_role/main.go:63` redacts the flag and
`cmd/committee-cli/main.go:106` no longer embeds the URL. The redaction never propagated — **13 live
violations at `main@bd39fe9`**:

- `internal/infrastructure/nats/client.go:65`, `:73`, `:92`, `:100`, `:225`, `:312` log `ConnectedUrl()` raw;
- four migration scripts log `*natsURL` and/or `ConnectedUrl()` raw —
  `migrate_writers_auditors_to_user_objects:78,92`, `migrate_member_visibility:76,93`,
  `migrate_show_meeting_attendees:76,93`, `migrate_counsel_role:79`.

The chart half is upheld at `main@bd39fe9` on both surfaces: every secret in `values.yaml` uses
`valueFrom.secretKeyRef`, and no file under `charts/lfx-v2-committee-service/templates/**` carries a literal
credential (the only `token` match is prose in a `ruleset.yaml` comment). Treat a match in `client.go` or a
migration script as confirmed by this census rather than speculative.

**Failure message:** Raw NATS URL / bearer token / secret logged, embedded in an error, or placed directly in chart values or a chart template.

**Fix:** omit the URL/token from logs and error messages; read tokens from env/flags; in charts — `values.yaml` and templates alike — use `valueFrom`/secret keypair rather than an inline secret value.

---

## `logging-errors-secrets/typed-domain-errors` — Important

**Pattern:** a `pkg/errors` typed error is bypassed — a bare `errors.New`/`fmt.Errorf` is returned where a
typed `Validation`/`NotFound`/`Conflict`/`Forbidden`/`ServiceUnavailable`/`Unexpected` is expected, an
upstream/remote error string is interpolated into a wrapper message instead of being passed as the wrapped
cause (breaking `errors.As`/`errors.Unwrap`), or a test asserts a Goa error type instead of using
`errors.As` against the `pkg/errors` type. A new error case must also be added to the `wrapError` switch in
`cmd/committee-api/service/error.go`.

**Detect:** in changed Go, flag `errors.New(`/`fmt.Errorf(` returns from service/storage code that should be
typed; flag `errors.New*("...: "+remoteErr, nil)` (cause dropped); in tests flag assertions on Goa error
types rather than `errors.As(err, &typedErr)`.

**Empirical citation:** PR #17 `internal/service/committee_reader.go:156` (CodeRabbit) — "Return a typed NotFound instead of a generic error for cross-committee membership." Recurs PR #7 `committee_validor_test.go:123` ("Assert using errors.As against pkg/errors.Validation"), PR #91 `email_sender.go:42` (dealako, "This path uses bare `fmt.Errorf` while the surrounding code uses `errors.NewServiceUnavailable`/`errors.NewUnexpected`"), PR #92 `invite_sender.go:54` (dealako, remote error "embedded in the message string rather than as the wrapped cause").

**Failure message:** Bare/Goa error used where a `pkg/errors` typed error belongs, or a remote error string interpolated instead of wrapped as the cause.

**Fix:** return the appropriate `pkg/errors` constructor; pass the upstream error as the wrapped cause (second arg) so `errors.As`/`errors.Is` work; add new cases to the `wrapError` switch; assert with `errors.As` against the `pkg/errors` type in tests.

---

## `logging-errors-secrets/sentinel-not-text-match` — Important

**Pattern:** control flow (skip vs update, not-found classification) is driven by substring-matching
`err.Error()` (e.g., `strings.Contains(err.Error(), "not counsel")` or a case-sensitive `"not found"`
check), which is brittle and breaks when the message changes or is wrapped.

**Detect:** grep changed Go for `strings.Contains(err.Error()` / `strings.Contains(*.Error(), "not found"`
used to branch logic; flag case-sensitive `"not found"` checks.

**Empirical citation:** PR #89 `scripts/migrations/migrate_counsel_role/main.go:125` (Copilot) — "Skip/updated classification is driven by matching substrings in err.Error() ('not counsel'), which is brittle ... Consider using a sentinel error (e.g., var ErrNotCounsel = errors.New(...)) and checking it with errors.Is." Recurs PR #45 `models.go:49` ("The case-sensitive check for 'not found' may lead to inconsistent error handling ... use a case-insensitive check") and PR #78 `migrate_join_mode_to_base/main.go:129` ("Use a sentinel error instead of matching error text").

**Revised 2026-07-30 — all three citations repointed.** Every originally cited site is deleted or relocated,
but the pattern is empirically alive. The migration-script instance this entry was built from was fixed in
one script and then **re-introduced in three others** — all verified at `main@bd39fe9`:
`migrate_show_meeting_attendees/main.go:137` and `migrate_member_visibility/main.go:137`
(`strings.Contains(err.Error(), "already has field")`), and
`migrate_writers_auditors_to_user_objects/main.go:135` (`"already migrated"`). Two more outside the
migrations: `cmd/committee-api/http.go:183` (`"request body too large"`) and
`internal/infrastructure/nats/group_weekly_brief_storage.go:295` (`"wrong last sequence"` — the
optimistic-lock probe). Quote whichever matches the diff.

**Corrected 2026-07-31.** This previously claimed "7 current hits, all driving control flow". Both halves
were wrong: `bd39fe9` has 8 `err.Error()` uses outside tests, and two of them
(`internal/infrastructure/nats/client.go:135` and `:163`) are `span.SetStatus(codes.Error, err.Error())` —
telemetry, which never drives control flow and is **not** this pattern. Named sites replace the aggregate,
because a count is the part that rots while the examples stay checkable.

**Two accepted variants — not findings.** `internal/infrastructure/nats/models.go:121` and
`group_weekly_brief_storage.go:295` are compatibility fallbacks with documented rationale. Text matching
there is a deliberate interop decision, not brittleness.

**Failure message:** Control flow branches on `err.Error()` substring matching — brittle to message changes/wrapping.

**Fix:** define a sentinel error and branch with `errors.Is`/`errors.As`; if string matching is unavoidable, lower-case both sides and say in a comment why the sentinel is not available.

---

## `logging-errors-secrets/silent-failure` — Important

**Pattern:** an error or failure path is swallowed — a build/publish failure logged as a warning and then
`nil` returned (so a worker pool treats it as success and a dependent sync silently doesn't happen), a
success log line emitted on the failure path, or a `Makefile`/hook that masks errors. When downstream
correctness depends on the side effect (e.g., the `committee.updated` event drives member re-sync), the
error must be propagated, not logged-and-dropped.

**Detect:** in changed Go, flag `slog.Warn...(...)` immediately followed by `return nil` in a function whose
caller relies on the result; flag "success"/"deleted" log lines that aren't guarded by the success branch;
flag shell `|| true` / `2>/dev/null` that hides build/provisioning errors in Makefile/hooks.

**Empirical citation:** PR #82 `internal/service/committee_writer.go:761` (CodeRabbit) — "Do not swallow `committee.updated` build failures." Same PR Copilot `committee_writer.go:758` ("If `CommitteeEvent.Build` fails, this code logs a warning and returns `nil`, which makes the worker pool treat the publish as successful and the update proceeds without emitting `committee.updated`"). Recurs PR #6 `committee_writer.go:109` ("'successfully deleted key' logged even on failure"), PR #93 `Makefile:176` ("Silent failure masking prevents debugging NATS provisioning issues"), PR #98 group_weekly_brief_generator log-level findings (jordane).

**Failure message:** Failure swallowed (warn-then-return-nil) on a path whose result drives downstream correctness, or success logged on the failure branch.

**Fix:** propagate the error (return it / surface it through the worker-pool result) when downstream correctness depends on the side effect; guard success log lines by the success branch; don't mask shell errors in Makefile/hooks.
