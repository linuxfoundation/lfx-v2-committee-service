# Goa Design Patterns

Goa is a code generation framework. You write a **design specification** describing your API, then run `make apigen` to generate the HTTP server, client, and OpenAPI documentation automatically.

## Files

- `cmd/committee-api/design/committee.go` — Service and method (endpoint) definitions
- `cmd/committee-api/design/type.go` — Reusable data types

**Never edit files in `gen/`** — they are fully overwritten by `make apigen`.

## Adding a New Endpoint

### Step 1: Define the method in committee.go

Add a new `dsl.Method(...)` block inside the existing `dsl.Service(...)` block:

```go
dsl.Method("get-committee-stats", func() {
    dsl.Description("Get statistics for a committee")

    dsl.Security(JWTAuth)

    dsl.Payload(func() {
        BearerTokenAttribute()
        VersionAttribute()
        CommitteeUIDAttribute()  // reuse existing attribute helpers
    })

    dsl.Result(func() {
        dsl.Attribute("stats", CommitteeStats)  // your new type
        dsl.Required("stats")
    })

    dsl.Error("BadRequest", BadRequestError, "Bad request")
    dsl.Error("NotFound", NotFoundError, "Resource not found")
    dsl.Error("InternalServerError", InternalServerError, "Internal server error")

    dsl.HTTP(func() {
        dsl.GET("/committees/{committee_uid}/stats")
        dsl.Param("version:v")
        dsl.Header("bearer_token:Authorization")
        dsl.Response(dsl.StatusOK)
        dsl.Response("BadRequest", dsl.StatusBadRequest)
        dsl.Response("NotFound", dsl.StatusNotFound)
        dsl.Response("InternalServerError", dsl.StatusInternalServerError)
    })
})
```

### Step 2: Define new types in type.go (if needed)

If the endpoint needs new request/response shapes, add types to `type.go`:

```go
var CommitteeStats = dsl.Type("committee-stats", func() {
    dsl.Description("Statistics for a committee")
    dsl.Attribute("member_count", dsl.Int, "Number of active members")
    dsl.Attribute("created_at", dsl.String, "ISO 8601 creation timestamp")
    dsl.Required("member_count", "created_at")
})
```

### Step 3: Regenerate

```bash
make apigen
```

This generates new files in `gen/http/committee/` including server stubs you must implement.

### Step 4: Implement the handler

After generation, Goa will expect a method on the service struct in `cmd/committee-api/service/`. Look for the new method name (camelCased from the design name) and implement it. Follow existing handlers as examples.

## Reusable Attribute Helpers

Look in `type.go` for helper functions like `CommitteeUIDAttribute()`, `BearerTokenAttribute()`, `VersionAttribute()` — use these instead of defining the same attributes repeatedly.

To add a new reusable attribute:
```go
func MemberCountAttribute() {
    dsl.Attribute("member_count", dsl.Int, "Number of members in the committee")
}
```

## Standard Error Types

These are already defined — always use them for consistency:
- `BadRequestError` — invalid input (400)
- `NotFoundError` — resource doesn't exist (404)
- `ConflictError` — uniqueness violation (409)
- `InternalServerError` — unexpected error (500)
- `ServiceUnavailableError` — dependency unavailable (503)

## Design Constraints

- Method names use kebab-case: `"get-committee-stats"` not `"getCommitteeStats"`
- HTTP paths use underscores for path params: `{committee_uid}`
- Always include `BearerTokenAttribute()` and `VersionAttribute()` in payloads
- Always declare all error types that might be returned
