---
name: develop
description: >
  Guided development workflow for building, fixing, updating, or refactoring
  the committee service — new endpoints, business logic, data models, NATS
  storage, or full features end-to-end. Use whenever someone wants to add a
  feature, fix a bug, modify existing behavior, or implement any code change
  in this Go service.
allowed-tools: Bash, Read, Write, Edit, Glob, Grep, AskUserQuestion
---

# Committee Service Development Guide

You are helping a contributor build within the LFX V2 Committee Service. This is a Go service — explain concepts in plain language and walk through each step. Point to existing code as examples whenever possible.

**Important:** You are building within the existing architecture, not redesigning it. If something seems to require changes to authentication, infrastructure, or deployment configuration, flag it for a code owner.

## The Mental Model: How This Service Works

Before writing any code, help the contributor understand the flow. When someone calls an API endpoint (e.g., "get committee ABC"), here's what happens:

```
HTTP Request
    ↓
HTTP Handler (cmd/committee-api/service/)
    — thin layer, validates input, calls internal service
    ↓
Internal Service (internal/service/)
    — business logic: validation, rules, orchestration
    ↓
Port Interface (internal/domain/port/)
    — an "abstract contract" that defines what storage operations exist
    ↓
NATS Implementation (internal/infrastructure/nats/)
    — actually reads/writes to the NATS key-value buckets
    ↓
NATS (the database)
```

**The golden rule:** Build from the bottom up. Data models and interfaces first, then storage, then business logic, then the HTTP layer. This prevents building an API that doesn't have the data to back it up.

## Step 1: Branch Setup

Before writing any code:

```bash
git checkout main
git pull origin main
```

Create a feature branch named after the JIRA ticket:
```bash
git checkout -b feat/LFXV2-<number>-short-description
```

If there's no JIRA ticket yet, ask the contributor to create one in the LFXV2 project first.

## Step 2: Understand the Feature

Ask the contributor what they're building. Before writing code, answer:

1. **What is the feature?** Describe it in one sentence from the user's perspective.
2. **What data does it need?** What fields/information are involved?
3. **What operation is this?** Create, Read, Update, Delete, or a query?
4. **Does the API endpoint already exist?** Check `cmd/committee-api/design/committee_svc.go` and the generated `gen/` folder.
5. **Does similar code already exist?** Check for existing patterns to follow.

## Step 3: Explore Existing Code First

Always read what exists before writing anything new:

```bash
# See what endpoints already exist
ls gen/http/committee/

# Read the design specification (source of truth for the API contract)
# cmd/committee-api/design/committee_svc.go

# See existing service methods
ls cmd/committee-api/service/

# See internal business logic
ls internal/service/

# See data models
ls internal/domain/model/

# See port interfaces (what storage operations are available)
ls internal/domain/port/

# See NATS implementations
ls internal/infrastructure/nats/
```

Read at least one existing example in the same area as the work being done before generating new code.

## Step 4: Plan Before Coding

Based on the feature, determine which layers need to change:

| Layer | File Location | When to Change |
|-------|--------------|----------------|
| **API Design** | `cmd/committee-api/design/committee_svc.go` | New endpoint or new request/response fields |
| **Data Types** | `cmd/committee-api/design/type.go` or `<entity>_type.go` | New Goa types for request/response |
| **Helm Ruleset** | `charts/lfx-v2-committee-service/templates/ruleset.yaml` | Every new endpoint needs an auth rule |
| **Domain Model** | `internal/domain/model/` | New or changed data shape stored in NATS |
| **Port Interface** | `internal/domain/port/` | New storage operation needed |
| **NATS Storage** | `internal/infrastructure/nats/` | Implementing a new port operation |
| **Internal Service** | `internal/service/` | New business logic |
| **HTTP Handler** | `cmd/committee-api/service/` | Wiring up a new endpoint |

**Build order is strict:** Domain model → Port interface → NATS implementation → Internal service → API design → Code generation → HTTP handler

Never write the HTTP handler before the storage layer exists — there's nothing for it to call.

## Step 5: Working with the Goa Design Files

This service uses **Goa**, a framework that generates HTTP server code from a design specification. Think of it like a blueprint: you describe what your API should look like, and Goa writes the boilerplate HTTP code.

### When to modify design files

Only modify design files if you're adding a **new endpoint** or changing the **request/response structure** of an existing one. For pure business logic changes, skip directly to Step 7.

### The design files

- `cmd/committee-api/design/committee_svc.go` — Endpoint definitions (HTTP methods, paths, payloads, responses)
- `cmd/committee-api/design/type.go` — Data types used in those endpoints

### After modifying design files

Always regenerate:
```bash
make apigen
```

This regenerates everything in `gen/` — never edit files in `gen/` directly, they will be overwritten.

Read `references/goa-patterns.md` for examples of adding endpoints and types.

## Step 5b: Add a Heimdall Ruleset Entry

**Every new endpoint must have a corresponding rule in `charts/lfx-v2-committee-service/templates/ruleset.yaml`.** Without it, the request will be blocked at the gateway when deployed.

Each rule specifies the HTTP method + path and what authorization check to perform. The check uses OpenFGA, which enforces access based on the user's relation to an object. The authorization model for committees is defined in [`lfx-v2-helm`](https://github.com/linuxfoundation/lfx-v2-helm/blob/main/charts/lfx-platform/templates/openfga/model.yaml). To see the live model in your local cluster:

```bash
kubectl describe authorizationmodel lfx-core -n lfx
```

At the time of writing, the `committee` type defines these relations:

```text
type committee
  relations
    define member: [user]                  # explicitly added as a committee member
    define writer: [user] or writer from project  # can modify the committee
    define auditor: [user, team#member] or auditor from project  # can view settings/sensitive data
    define viewer: [user:*] or member or auditor  # can view general committee data (public = anyone)
```

**Choose the relation based on what the endpoint does:**

| Endpoint type | Relation | Example |
| ------------- | -------- | ------- |
| Read public committee data | `viewer` | GET /committees/:uid |
| Read sensitive settings | `auditor` | GET /committees/:uid/settings |
| Create, update, delete | `writer` | PUT /committees/:uid |
| Self-action (user acts on themselves, no prior relation) | none — use `allow_all` | join, leave, accept invite |

For a standard protected endpoint, the rule looks like this:

```yaml
- id: "rule:lfx:lfx-v2-committee-service:<resource>:<action>"
  allow_encoded_slashes: 'off'
  match:
    methods:
      - GET
    routes:
      - path: /committees/:uid/your-resource
  execute:
    - authenticator: oidc
    - authenticator: anonymous_authenticator
    {{- if .Values.app.use_oidc_contextualizer }}
    - contextualizer: oidc_contextualizer
    {{- end }}
    {{- if .Values.openfga.enabled }}
    - authorizer: openfga_check
      config:
        values:
          relation: viewer          # change to writer/auditor as needed
          object: "committee:{{ "{{- .Request.URL.Captures.uid -}}" }}"
    {{- else }}
    - authorizer: allow_all
    {{- end }}
    - finalizer: create_jwt
      config:
        values:
          aud: {{ .Values.app.audience }}
```

For self-action endpoints (where the user has no prior OpenFGA relation to the object), skip the `openfga_check` and use `allow_all` — the service layer enforces business rules:

```yaml
    {{- if .Values.openfga.enabled }}
    - authorizer: allow_all         # no prior relation to check
    {{- else }}
    - authorizer: allow_all
    {{- end }}
```

## Step 6: Data Models and Storage

### Domain models (what gets stored)

Data models live in `internal/domain/model/`. Each file represents one concept (e.g., `committee.go`, `committee_member.go`).

**Conventions:**
- Use Go structs with json tags
- Include license header on all new files
- Keep models focused — one concept per file

Example structure:
```go
// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

type Committee struct {
    UID       string `json:"uid"`
    Name      string `json:"name"`
    ProjectID string `json:"project_id"`
    // ...
}
```

### Port interfaces (the storage contract)

Interfaces in `internal/domain/port/` define what operations are available for each type of data. The service layer calls these interfaces — it doesn't know or care about NATS specifically.

When adding a new operation, add it to the appropriate interface. Read the existing interfaces to understand the pattern.

### NATS implementations

`internal/infrastructure/nats/` contains the actual NATS code. Each file implements the port interface for a data type. When you add a new operation to a port interface, you must implement it here.

Data is stored as JSON in NATS key-value buckets. Each entry has a key (usually a UID) and a JSON-encoded value.

Read `references/nats-patterns.md` for NATS storage patterns.

## Step 7: Business Logic (Internal Service)

`internal/service/` contains the business rules. This is where most of the interesting work happens.

**Key patterns:**
- Services receive port interfaces via constructor injection (not direct NATS calls)
- Return domain errors from `pkg/errors/` rather than raw errors
- Use structured logging from `pkg/log/` for important operations
- Validate inputs before calling storage

Read an existing service file in the same area before writing new business logic.

## Step 8: HTTP Handler (Last Step)

After all the lower layers are in place, wire up the HTTP handler in `cmd/committee-api/service/`.

Handlers in this service are thin — they:
1. Extract data from the Goa-generated request type
2. Call the internal service
3. Map the result to the Goa-generated response type
4. Return errors using the `pkg/errors/` package

No business logic should live in handlers. If you find yourself writing complex logic here, it belongs in the internal service layer.

## Step 8b: V1 Compatibility — Does This Change Need to Sync?

This service coexists with a V1 system ([`project-management`](https://github.com/linuxfoundation/project-management)) that exposes the same committee data via its own API ([V1 committee API docs](https://api-gw.platform.linuxfoundation.org/project-service/v1/api-docs#tag/committeeV2)). The two systems are kept in sync by the `lfx-v1-sync-helper` service.

**Ask yourself: does this change touch a field that also exists (or should exist) in V1?**

If yes, you need changes in **three repos**, not just this one:

| Repo | What to update |
| ---- | -------------- |
| `lfx-v2-committee-service` (this repo) | V2 domain model + API design |
| [`project-management`](https://github.com/linuxfoundation/project-management) | V1 API + data model to add/change the field |
| [`lfx-v1-sync-helper`](https://github.com/linuxfoundation/lfx-v1-sync-helper) | Sync logic that converts between V1 and V2 models |

### How the sync works

**V2 → V1 (when data is written in V2):**
Write in V2 → indexed in OpenSearch → triggers `lfx-v1-sync-helper` → writes to V1 (PostgreSQL). The sync helper contains a function that converts the V2 data model to the V1 data model. **Any new V2 field that should appear in V1 must be mapped there.**

**V1 → V2 (when data is written in V1):**
Write in V1 → triggers `lfx-v1-sync-helper` → indexes in V2 (OpenSearch). The sync helper contains a separate function that maps V1 attributes to V2 attributes. **Any new V1 field that should appear in V2 must be mapped there.**

### Important: module versioning in lfx-v1-sync-helper

The `lfx-v1-sync-helper` imports this committee service as a Go module to use the V2 data model types. If you're adding new fields to the V2 model and updating the sync helper in parallel (before the V2 changes are released and tagged), you must point the sync helper's `go.mod` to your branch version rather than the tagged release:

```bash
# In lfx-v1-sync-helper, point to your in-progress branch
go get github.com/linuxfoundation/lfx-v2-committee-service@<your-branch-or-commit>
```

Revert this to a tagged version before merging the sync helper PR.

Read `references/v1-sync-patterns.md` for details on where exactly to update in `lfx-v1-sync-helper`.

## Step 9: Tests

Tests live next to the code they test, in `*_test.go` files.

```bash
# Run all tests
make test

# Run tests for a specific package
go test -v ./internal/service/...
```

Mock implementations for testing live in `internal/infrastructure/mock/`. When writing tests for service layer code, use these mocks rather than a real NATS connection.

When adding a new operation to a port interface, add it to the mock as well.

## Step 10: Validate

Run the full validation suite before finishing:

```bash
make fmt           # Format code
make check         # Check formatting, lint, and license headers
make test          # Run all tests
make build         # Verify the binary builds
```

Fix any issues before moving on.

## Step 11: Commit

Stage and commit your changes:

```bash
git add <specific files>
git commit -s -m "feat(LFXV2-<number>): short description of what you built"
```

The `-s` flag adds the required `Signed-off-by` line. The commit message format is `type(ticket): description`.

Types: `feat` (new feature), `fix` (bug fix), `refactor`, `test`, `docs`, `chore`.

## Step 12: Summary

Provide a clear summary of what was built:
- All files created or modified, with their purpose
- Any new endpoints (method + path)
- Any new domain models or port interface changes
- How to test the feature manually (e.g., curl command)

**Next step:** Run `/preflight` to validate everything before submitting a PR.

---

## Reference Files

- `references/goa-patterns.md` — How to add endpoints and types to the Goa design
- `references/nats-patterns.md` — NATS key-value storage patterns
- `references/v1-sync-patterns.md` — Where and how to update lfx-v1-sync-helper for V1/V2 data model changes
