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
4. **Does the API endpoint already exist?** Check `cmd/committee-api/design/committee.go` and the generated `gen/` folder.
5. **Does similar code already exist?** Check for existing patterns to follow.

## Step 3: Explore Existing Code First

Always read what exists before writing anything new:

```bash
# See what endpoints already exist
ls gen/http/committee/

# Read the design specification (source of truth for the API contract)
# cmd/committee-api/design/committee.go

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
| **API Design** | `cmd/committee-api/design/committee.go` | New endpoint or new request/response fields |
| **Data Types** | `cmd/committee-api/design/type.go` | New Goa types for request/response |
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

- `cmd/committee-api/design/committee.go` — Endpoint definitions (HTTP methods, paths, payloads, responses)
- `cmd/committee-api/design/type.go` — Data types used in those endpoints

### After modifying design files

Always regenerate:
```bash
make apigen
```

This regenerates everything in `gen/` — never edit files in `gen/` directly, they will be overwritten.

Read `references/goa-patterns.md` for examples of adding endpoints and types.

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
