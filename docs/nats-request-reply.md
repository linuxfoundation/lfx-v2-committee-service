<!--
Copyright The Linux Foundation and each contributor to LFX.
SPDX-License-Identifier: CC-BY-4.0
-->

# NATS Request-Reply Subjects

This document describes the NATS request-reply subjects served by the committee service. These are synchronous, point-to-point subjects (core NATS request/reply with a queue group) used by other services to query committee state. All subjects share the `lfx.committee-api.queue` queue group.

For event subjects emitted by this service (fire-and-forget publishes) see [Indexer Contract](indexer-contract.md) and [FGA Contract](fga-contract.md).

---

## `lfx.committee-api.get_project`

Resolves a v2 committee UID to the UID of the project that owns it.

**Subject constant:** `pkg/constants` — `CommitteeGetProjectSubject`.  
**Request/response types:** `pkg/api` — `GetCommitteeProjectRequest`, `GetCommitteeProjectResponse`.  
Consumers should import both packages to use the typed structs and constant rather than hard-coding strings.

### Request

```json
{ "committee_uid": "<v2 UUID>" }
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `committee_uid` | string (UUID v4) | yes | The v2 UID of the committee to look up. |

### Response (success)

```json
{ "project_uid": "<v2 UUID>" }
```

| Field | Type | Description |
|-------|------|-------------|
| `project_uid` | string (UUID v4) | The v2 UID of the owning project. |

### Response (not found)

```json
{ "error": "not found" }
```

Returned when no committee exists for the supplied UID. The NATS reply is still sent (no timeout); only the `error` field is set.

### Response (request error)

```json
{ "error": "<message>" }
```

Returned for malformed JSON or an invalid (non-UUID) `committee_uid`. The `error` field describes the failure.

### Example

```go
import committeeapi "github.com/linuxfoundation/lfx-v2-committee-service/pkg/api"
import "github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"

reqBytes, _ := json.Marshal(committeeapi.GetCommitteeProjectRequest{CommitteeUID: committeeUID})
msg, err := nc.Request(constants.CommitteeGetProjectSubject, reqBytes, 5*time.Second)
if err != nil {
    // NATS timeout or connection error
}

var resp committeeapi.GetCommitteeProjectResponse
if err := json.Unmarshal(msg.Data, &resp); err != nil {
    // malformed reply
}
if resp.Error != "" {
    // "not found" or request validation error
}
// resp.ProjectUID is the owning project's v2 UID
```

---

## `lfx.committee-api.name_to_uid`

Resolves a committee's UID from its project UID + committee name. Used by consumers (e.g. `lfx-v1-sync-helper`'s `--check-committee-names` backfill flag) to detect a lost forward-mapping case that needs repair rather than a duplicate create.

**Subject constant:** `pkg/constants` — `CommitteeNameToUIDSubject`.  
**Request/response types:** `pkg/api` — `CommitteeNameToUIDRequest`, `CommitteeNameToUIDResponse`.  
Consumers should import both packages to use the typed structs and constant rather than hard-coding strings.

### Request

```json
{ "project_uid": "<v2 UUID>", "name": "<committee name>" }
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `project_uid` | string (UUID v4) | yes | The v2 UID of the project to search within. |
| `name` | string | yes | The committee name to match exactly against the committee's stored name. |

### Response (success)

```json
{ "committee_uid": "<v2 UUID>" }
```

| Field | Type | Description |
|-------|------|-------------|
| `committee_uid` | string (UUID v4) | The v2 UID of the matching committee. |

### Response (not found)

```json
{}
```

Returned when no live committee matches the given project UID + name pair. This is not an error condition — both `committee_uid` and `error` are omitted (`omitempty`) from the reply.

### Response (request error)

```json
{ "error": "<message>" }
```

Returned for malformed JSON, an invalid (non-UUID) `project_uid`, or an empty `name`. The `error` field describes the failure.

### Example

```go
import committeeapi "github.com/linuxfoundation/lfx-v2-committee-service/pkg/api"
import "github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"

reqBytes, _ := json.Marshal(committeeapi.CommitteeNameToUIDRequest{ProjectUID: projectUID, Name: name})
msg, err := nc.Request(constants.CommitteeNameToUIDSubject, reqBytes, 5*time.Second)
if err != nil {
    // NATS timeout or connection error
}

var resp committeeapi.CommitteeNameToUIDResponse
if err := json.Unmarshal(msg.Data, &resp); err != nil {
    // malformed reply
}
if resp.Error != "" {
    // request validation error
}
if resp.CommitteeUID == "" {
    // no live committee matches this project UID + name pair
}
```

---

## `lfx.committee-api.get_name`

Returns the display name of a committee.

> **Wire format:** plain-text, not JSON. Request payload is the raw committee UID string; success reply is the raw name string; failure reply is `{"error":"<message>"}`.

**Defined in:** `pkg/constants` — `CommitteeGetNameSubject`.

### Request

```text
<committee_uid>
```

Plain UTF-8 UUID string (no JSON wrapper).

### Response (success)

```text
<committee name>
```

Plain UTF-8 string.

### Response (failure)

```json
{ "error": "<message>" }
```

---

## `lfx.committee-api.list_members`

Returns all members of a committee as a JSON array.

> **Wire format:** plain-text UID in, JSON array out. Request payload is the raw committee UID string.

**Defined in:** `pkg/constants` — `CommitteeListMembersSubject`.

### Request

```text
<committee_uid>
```

Plain UTF-8 UUID string (no JSON wrapper).

### Response (success)

JSON array of committee member objects:

```json
[
  {
    "uid": "...",
    "committee_uid": "...",
    "username": "...",
    "role": "...",
    ...
  }
]
```

### Response (failure)

```json
{ "error": "<message>" }
```
