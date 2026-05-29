# Logging, Typed Errors & Secret Handling

Patterns around this repo's logging discipline (PII redaction via `pkg/redaction`, no secret/URL leakage),
the `pkg/errors` typed-error family, and silent-failure tells. Security/data patterns — promoted at a
single occurrence.

**Read when:** any `.go` file that logs (`slog.*Context`, `log.*`), returns errors, or builds error
messages — especially `internal/service/**`, `internal/infrastructure/nats/**`,
`cmd/committee-api/service/**`, `cmd/committee-cli/**`, and `scripts/migrations/**`.

---

## `logging-errors-secrets/pii-in-logs` — Critical

**Pattern:** a raw email address or username (Auth0 sub / LFID) is logged or interpolated into an error
message without `redaction.RedactEmail` / `redaction.Redact`. The committee member, invite, subscriber,
and notification flows all redact identifiers; new code on those paths must too.

**Detect:** grep changed files for `slog.*Context(` calls and `errors.New*`/`fmt.Errorf` whose args include
`.Email`, `.InviteeEmail`, `.ApplicantUID`, `.Username`, `principal`, or a recipient email, without a
`redaction.` wrapper.

**Empirical citation:** PR #16 `cmd/committee-api/service/committee_service.go:232` (CodeRabbit) — "Avoid logging PII (email/username) at request handling — redact or remove." Recurs PR #91 `committee_subscriber.go:47` ("Remove raw user identifiers from logs"), PR #91 `message_handler.go:513` ("Remove recipient email addresses from logs"), PR #44 `messaging_request.go:53` (Copilot, "The error message exposes the email address in plain text ... use `redaction.RedactEmail()`"), PR #61 `committee_application.go:50` ("Redact `applicant_uid` before logging it").

**Failure message:** Raw email/username/principal logged or put in an error message without `pkg/redaction`.

**Fix:** wrap with `redaction.RedactEmail(...)` for emails and `redaction.Redact(...)` for usernames/subs/principals in both log fields and error message strings.

---

## `logging-errors-secrets/no-raw-secret-or-url` — Critical

**Pattern:** a raw NATS URL, bearer token, or secret is logged or embedded in an error message. Migration
scripts and the CLI repeatedly logged `NATS_URL`; connection-error messages embedded it. Also: a hardcoded
`Authorization` bearer in a migration script, or chart values that put secrets directly in `values.yaml`
instead of `valueFrom`/keypair.

**Detect:** grep changed Go for `slog`/`fmt.Errorf` args or string concatenation containing a NATS URL
variable, `NATS_URL`, `Authorization`, `Bearer `, or token vars. In chart changes, flag secret literals in
`values.yaml` not sourced via `valueFrom`.

**Empirical citation:** PR #78 `scripts/migrations/migrate_join_mode_to_base/main.go:65` (CodeRabbit) — "Do not log the raw NATS URL." (recurs `reindex_committees/main.go:88`, PR #89 `migrate_counsel_role/main.go:65`, PR #87 `cmd/committee-cli/main.go:90` "Don't include raw `NATS_URL` in connection errors"). Hardcoded token PR #78 `reindex_committees/main.go:45` (Copilot, "This hard-codes an Authorization header value ... read a bearer token from an env var"). Chart-secret PR #98 `providers.go:529` (jordane, "Best practice is to not use a secret here, but to use a keypair").

**Failure message:** Raw NATS URL / bearer token / secret logged, embedded in an error, or placed directly in chart values.

**Fix:** omit the URL/token from logs and error messages; read tokens from env/flags; in charts use `valueFrom`/secret keypair rather than inline secret values.

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

**Failure message:** Control flow branches on `err.Error()` substring matching — brittle to message changes/wrapping.

**Fix:** define a sentinel error and branch with `errors.Is`/`errors.As`; if string matching is unavoidable, lower-case both sides.

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
