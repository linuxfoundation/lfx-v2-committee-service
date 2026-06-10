# Goa Presentation Layer & Design

Patterns in the Goa presentation layer (`cmd/committee-api/service/*`), the Goa design
(`cmd/committee-api/design/**`), and the custom multipart decoder (`cmd/committee-api/http.go`):
nil-returning stubs, nil-pointer dereferences on payloads, payload↔domain mapping symmetry, read-only
field handling, ETag/If-Match optimistic concurrency, and multipart validation.

**Read when:** any file under `cmd/committee-api/service/**` (especially `committee_service.go`,
`committee_service_response.go`), `cmd/committee-api/design/**`, or
`cmd/committee-api/http.go`.

---

## `goa-presentation/nil-nil-stub-or-deref` — Critical

**Pattern:** a Goa service method returns `(nil, nil)` (a stub that the transport layer turns into a
misleading 200 OK with an empty body), or a conversion/handler dereferences a payload pointer (`*p.UID`,
`base.X`) without a nil check and will panic on a malformed request.

**Detect:** in `cmd/committee-api/service/*.go`, flag `return nil, nil` in a method that should return a
result or typed error. Flag `*p.UID` / pointer-field dereferences in handlers and `convert*` helpers that
aren't preceded by a nil/empty guard.

**Empirical citation:** PR #14 `cmd/committee-api/service/committee_member_service.go:70` (CodeRabbit) — "Create returns (nil, nil) — high risk of runtime failure or incorrect 200 OK" (recurs at :99/:134/:164 and `committee_service.go:233/261`). Nil-deref recurs PR #6 `committee_service.go:82` (CodeRabbit, "Nil-UID will panic – validate incoming request"), PR #41 `committee_service_response.go:172` (jordane, "we're dereferencing base without checking it ... not safe to use unless this has been done"), PR #97 `committee_service.go:1457` ("Guard against nil `Claim` output before dereference").

**Failure message:** Goa service method returns `(nil, nil)` (misleading 200) or dereferences a payload pointer without a nil guard (panic risk).

**Fix:** return a concrete result or a typed `pkg/errors` value; validate required pointer fields (UID, etc.) and return `errors.NewValidation` before dereferencing.

---

## `goa-presentation/payload-mapping-symmetry` — Important

**Pattern:** a new field is added to the domain model and one conversion direction, but a sibling
conversion helper is not updated — so the field is silently dropped. The base/settings/full response split
means a field often needs mapping in several of: `convertPayloadTo{Base,Settings}`,
`convertPayloadToUpdate{Base,Settings}`, `convert{Base,Settings,DomainToFull}Response`. An explicitly-empty
list (`[]`) must also be preserved (not collapsed to `nil`) so clients can clear writers/auditors.

**Detect:** when the diff adds/renames a field on `CommitteeBase`/`CommitteeSettings`/member, grep every
`convert*` helper in `committee_service_response.go` for that field and flag any helper that maps the
sibling fields but not the new one. Flag `len(x)==0 → nil` conversions that lose the empty-vs-omitted
distinction.

**Empirical citation:** PR #67 `cmd/committee-api/service/committee_service_response.go:70` (Copilot) — "`JoinMode` is now correctly copied into the base on create, but the update-base payload conversion (`convertPayloadToUpdateBase`) still doesn't set `base.JoinMode`" (recurs at :182 for `convertBaseToResponse`). Empty-list case PR #72 `committee_service_response.go:633` ("returns `nil` when `len(users) == 0`, which loses the distinction between an omitted list (`nil`) and an explicitly empty list (`[]`) ... prevents clients from clearing writers/auditors").

**Failure message:** New/renamed field mapped in one conversion helper but not its siblings (silently dropped), or an explicit empty list collapsed to nil.

**Fix:** map the field in every relevant `convert*` helper (create, update, and all response variants); return `nil` only when the input slice is itself `nil`, otherwise a non-nil empty slice.

---

## `goa-presentation/readonly-field-leak` — Important

**Pattern:** a server-maintained or server-derived field is exposed as client-writable in a create/update
payload — e.g., `has_mailing_list` (maintained via mailing-list-api events) added to
`CommitteeBaseAttributes()`, or `uploaded_by`/`created_by` accepted from the client instead of taken from
the JWT principal. Read-only fields belong only in the `*-with-readonly-attributes` result types.

**Detect:** in `cmd/committee-api/design/type.go`, check that event-maintained / server-derived fields are
not part of the create/update payload attribute helpers. In handlers, confirm `created_by_uid`/`uploaded_by`
are sourced from the context principal, not the payload.

**Empirical citation:** PR #74 `cmd/committee-api/design/type.go:26` (Copilot) — "`HasMailingListAttribute()` is now part of `CommitteeBaseAttributes()` ... This makes `has_mailing_list` client-writable ... Consider removing it from the request payload attributes and only including it in read-only/result types." Recurs PR #71 `committee_service.go:1267` (CodeRabbit, "Don't persist client-supplied uploader identity") and PR #71 `design/type.go:983` (andrest50, "can you see if it's possible to get this from the JWT like the username").

**Failure message:** Server-maintained/derived field exposed as client-writable in a create/update payload (or uploader/creator identity taken from the payload instead of the JWT).

**Fix:** keep event-maintained/server-derived fields out of the request payload attribute helpers (result types only); source `created_by`/`uploaded_by` from the context principal.

---

## `goa-presentation/etag-if-match-required` — Important

**Pattern:** a mutable KV-backed resource's update/delete endpoint lacks `If-Match` (ETag) optimistic
concurrency, or `If-Match` is defined but not marked `Required`, so concurrent mutations can clobber each
other. New mutable resources (links, folders, documents) should follow the committee base endpoints'
ETag/If-Match pattern (GET returns the ETag; update/delete require it).

**Detect:** in `cmd/committee-api/design/**`, for update/delete of a mutable KV-backed resource, verify an
`If-Match` header is declared and `dsl.Required`. In handlers, verify the etag validator is enabled and
returns a proper Conflict error (not nil) on mismatch.

**Empirical citation:** PR #14 `cmd/committee-api/design/committee-members.go:112` (CodeRabbit) — "Update: If-Match must be required to enforce optimistic concurrency" (recurs :151 for Delete). Endorsed by andrest50 PR #68 `committee.go:1020/1164` ("We usually have an etag (If-Match header) ... to avoid concurrent requests to delete the same resource ... See the committee base endpoints to do the same here").

**Failure message:** Mutable KV-backed resource update/delete lacks a required `If-Match` (ETag) header for optimistic concurrency.

**Fix:** add a `Required` `If-Match` header to the update/delete design (and a GET that returns the ETag); enable the etag validator in the handler and return a Conflict on mismatch.

---

## `goa-presentation/multipart-bypasses-validation` — Important

**Pattern:** the custom multipart decoder in `cmd/committee-api/http.go` populates the payload directly and
bypasses the generated request-body validation (maxLength on name/description, required file part, media-type
allowlist), and/or caps reads only per-part without bounding total request size or part count, and/or leaves
multipart parts unclosed (resource leak).

**Detect:** in `cmd/committee-api/http.go`, for the document-upload decoder, verify it builds/validates via
the generated `Validate*RequestBody`, normalizes and allowlists the media type, wraps the body with a total
size cap (`http.MaxBytesReader`), and closes each part.

**Empirical citation:** PR #71 `cmd/committee-api/http.go:173` (Copilot) — "The custom multipart decoder populates UploadCommitteeDocumentPayload directly and bypasses the generated request-body validation (e.g., maxLength constraints ...). This allows oversized fields through to storage/indexing." Recurs same PR Copilot :187 (no total-size/part-count cap), CodeRabbit `http.go:12` ("Normalize the uploaded media type before allowlist checks"), CodeRabbit `http.go:173/149` (unclosed parts → resource leak).

**Failure message:** Custom multipart decoder bypasses generated validation / lacks a total-size cap / leaks unclosed parts.

**Fix:** decode into the generated request-body type and call `Validate*RequestBody`; normalize the media type before the allowlist check; wrap the body with `http.MaxBytesReader`; close every part (including on error paths).
