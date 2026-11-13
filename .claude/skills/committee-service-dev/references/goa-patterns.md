<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Goa Patterns (committee-service)

Repo-local Goa design layout for this service: where files live, the base / settings split, sub-resource method shape, multipart document upload, and ETag handling. Go and Goa coding rules live in SKILL.md; platform-level Goa-on-NATS context lives in `lfx-skills:lfx-platform-architecture`.

## Committee-service local specifics

- Design files live in `cmd/committee-api/design/`.
- Follows this repo's base/settings split: `CommitteeBase` and
  `CommitteeSettings` with separate `*-with-readonly-attributes` response types.
- Endpoint families are committee base/settings, committee members,
  org-scoped committee seats (`get-org-committee-seats`,
  `reassign-org-committee-seat`), invite/application flows, self-join/leave,
  committee links, link folders, committee documents, and working-group weekly
  briefs (`GET .../weekly-briefs/current`, `POST .../weekly-briefs/generate`).
- Links and documents also support a full-replacement `PUT` with `If-Match`
  (`/committees/{uid}/links/{link_uid}` and `/committees/{uid}/documents/{document_uid}`;
  a move is a `PUT` that names the target `folder_uid`, an omitted `folder_uid` is the
  root, and a document's uploaded file is never changed). Link folders still support
  only create/get/list/delete shapes as applicable; there is no update endpoint for
  them today.
- Document upload uses Goa multipart request wiring plus the custom decoder in
  `cmd/committee-api/http.go`.
- ETag/If-Match handling is used for mutable KV-backed resources and delete
  operations that require optimistic locking.
