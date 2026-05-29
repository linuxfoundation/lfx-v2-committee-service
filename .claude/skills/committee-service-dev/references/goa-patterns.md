<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Goa Patterns (committee-service)

Repo-local Goa design layout for this service: where files live, the base / settings split, sub-resource method shape, multipart document upload, and ETag handling. Go and Goa coding rules live in SKILL.md; platform-level Goa-on-NATS context lives in `lfx-skills:lfx-platform-architecture`.

## Committee-service local specifics

- Design files live in `cmd/committee-api/design/`.
- Follows this repo's base/settings split: `CommitteeBase` and
  `CommitteeSettings` with separate `*-with-readonly-attributes` response types.
- Endpoint families are committee base/settings, committee members,
  invite/application flows, self-join/leave, committee links, link folders,
  committee documents, and working-group weekly briefs
  (`GET .../weekly-briefs/current`, `POST .../weekly-briefs/generate`).
- Links, link folders, and documents currently support create/get/list/delete
  shapes as applicable; there are no update endpoints for those resources
  today.
- Document upload uses Goa multipart request wiring plus the custom decoder in
  `cmd/committee-api/http.go`.
- ETag/If-Match handling is used for mutable KV-backed resources and delete
  operations that require optimistic locking.
