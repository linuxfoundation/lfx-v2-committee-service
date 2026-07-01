// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	authpkg "github.com/linuxfoundation/lfx-v2-committee-service/pkg/auth"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
	fgaconstants "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/constants"
	fgatypes "github.com/linuxfoundation/lfx-v2-fga-sync/pkg/types"
	indexerTypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
	inviteapi "github.com/linuxfoundation/lfx-v2-invite-service/pkg/api"
	"golang.org/x/sync/errgroup"

	"goa.design/goa/v3/security"
)

// committeeServicesrvc service implementation with clean architecture
type committeeServicesrvc struct {
	committeeWriterOrchestrator service.CommitteeWriter
	committeeReaderOrchestrator service.CommitteeReader
	auth                        port.Authenticator
	storage                     port.CommitteeReaderWriter
	publisher                   port.CommitteePublisher
	inviteSender                port.InviteSender
	lfxSelfServeBaseURL         string
	userReader                  port.UserReader
	linkReader                  service.CommitteeLinkDataReader
	linkWriter                  service.CommitteeLinkDataWriter
	docReader                   service.CommitteeDocumentDataReader
	docWriter                   service.CommitteeDocumentDataWriter
	weeklyBriefReader           service.GroupWeeklyBriefDataReader
	weeklyBriefGenerator        service.GroupWeeklyBriefGenerator
	weeklyBriefWriter           service.GroupWeeklyBriefDataWriter
	orgSeatReader               port.OrgCommitteeSeatReader
}

// JWTAuth implements the authorization logic for service "committee-service"
// for the "jwt" security scheme.
func (s *committeeServicesrvc) JWTAuth(ctx context.Context, token string, scheme *security.JWTScheme) (context.Context, error) {

	// Parse the Heimdall-authorized principal from the token
	principal, err := s.auth.ParsePrincipal(ctx, token, slog.Default())
	if err != nil {
		slog.ErrorContext(ctx, "committeeService.jwt-auth",
			"error", err,
			"token_length", len(token),
		)
		return ctx, err
	}

	ctx = context.WithValue(ctx, constants.PrincipalContextID, principal)
	return ctx, nil
}

// Create Committee
func (s *committeeServicesrvc) CreateCommittee(ctx context.Context, p *committeeservice.CreateCommitteePayload) (res *committeeservice.CommitteeFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeService.create-committee",
		"project_uid", p.ProjectUID,
		"name", p.Name,
		"x_sync", p.XSync,
	)

	// Enrich writer/auditor usernames from the auth service; caller-supplied LFIDs are untrusted.
	if err := s.enrichAllRoleFields(ctx, p.Writers, p.Auditors); err != nil {
		return nil, wrapError(ctx, err)
	}

	// After enrichment, every entry must have at least an email or a resolved username.
	if err := validateIdentityFields(p.Writers, p.Auditors); err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert payload to DTO
	request := s.convertPayloadToDomain(p)

	// Execute use case
	response, err := s.committeeWriterOrchestrator.Create(ctx, request, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertDomainToFullResponse(response)

	return result, nil
}

// GetCommitteeBase retrieves the committee base information by UID.
func (s *committeeServicesrvc) GetCommitteeBase(ctx context.Context, p *committeeservice.GetCommitteeBasePayload) (res *committeeservice.GetCommitteeBaseResult, err error) {

	slog.DebugContext(ctx, "committeeService.get-committee-base",
		"committee_uid", p.UID,
	)

	// Execute use case
	committeeBase, revision, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert domain model to GOA response
	result := s.convertBaseToResponse(committeeBase)

	// Create result with ETag (using revision from NATS)
	revisionStr := fmt.Sprintf("%d", revision)
	res = &committeeservice.GetCommitteeBaseResult{
		CommitteeBase: result,
		Etag:          &revisionStr,
	}

	return res, nil
}

// Update Committee
func (s *committeeServicesrvc) UpdateCommitteeBase(ctx context.Context, p *committeeservice.UpdateCommitteeBasePayload) (res *committeeservice.CommitteeBaseWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.update-committee-base",
		"committee_uid", p.UID,
		"x_sync", p.XSync,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
		)
		return nil, wrapError(ctx, err)
	}

	// Convert payload to domain model
	committee := s.convertPayloadToUpdateBase(p)

	// Execute use case
	updatedCommittee, err := s.committeeWriterOrchestrator.Update(ctx, committee, parsedRevision, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertBaseToResponse(&updatedCommittee.CommitteeBase)

	return result, nil
}

// Delete Committee
func (s *committeeServicesrvc) DeleteCommittee(ctx context.Context, p *committeeservice.DeleteCommitteePayload) error {
	slog.DebugContext(ctx, "committeeService.delete-committee",
		"committee_uid", p.UID,
		"x_sync", p.XSync,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
		)
		return wrapError(ctx, err)
	}

	// Execute delete use case
	errDelete := s.committeeWriterOrchestrator.Delete(ctx, *p.UID, parsedRevision, p.XSync)
	if errDelete != nil {
		return wrapError(ctx, errDelete)
	}

	return nil
}

// Get Committee Settings
func (s *committeeServicesrvc) GetCommitteeSettings(ctx context.Context, p *committeeservice.GetCommitteeSettingsPayload) (res *committeeservice.GetCommitteeSettingsResult, err error) {

	slog.DebugContext(ctx, "committeeService.get-committee-settings",
		"committee_uid", p.UID,
	)

	// Execute use case
	committeeSettings, revision, err := s.committeeReaderOrchestrator.GetSettings(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert domain model to GOA response
	result := s.convertSettingsToResponse(committeeSettings)

	// Create result with ETag (using revision from NATS)
	revisionStr := fmt.Sprintf("%d", revision)
	res = &committeeservice.GetCommitteeSettingsResult{
		CommitteeSettings: result,
		Etag:              &revisionStr,
	}

	return res, nil
}

// Update Committee Settings
func (s *committeeServicesrvc) UpdateCommitteeSettings(ctx context.Context, p *committeeservice.UpdateCommitteeSettingsPayload) (res *committeeservice.CommitteeSettingsWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.update-committee-settings",
		"committee_uid", p.UID,
		"x_sync", p.XSync,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
		)
		return nil, wrapError(ctx, err)
	}

	// Enrich writer/auditor usernames from the auth service; caller-supplied LFIDs are untrusted.
	if err := s.enrichAllRoleFields(ctx, p.Writers, p.Auditors); err != nil {
		return nil, wrapError(ctx, err)
	}

	// After enrichment, every entry must have at least an email or a resolved username.
	if err := validateIdentityFields(p.Writers, p.Auditors); err != nil {
		return nil, wrapError(ctx, err)
	}

	// Fetch existing settings so stored writer/auditor identity can be preserved during conversion.
	existingSettings, _, errGet := s.committeeReaderOrchestrator.GetSettings(ctx, *p.UID)
	if errGet != nil {
		return nil, wrapError(ctx, errGet)
	}

	// Convert payload to domain model, seeding each user from the existing record.
	settings := s.convertPayloadToUpdateSettings(p, existingSettings)

	// Execute use case
	updatedSettings, err := s.committeeWriterOrchestrator.UpdateSettings(ctx, settings, parsedRevision, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertSettingsToResponse(updatedSettings)

	return result, nil
}

// CreateCommitteeMember adds a new member to a committee
func (s *committeeServicesrvc) CreateCommitteeMember(ctx context.Context, p *committeeservice.CreateCommitteeMemberPayload) (res *committeeservice.CommitteeMemberFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeMemberService.create-committee-member",
		"committee_uid", p.UID,
		"email", redaction.RedactEmail(p.Email),
		"x_sync", p.XSync,
	)

	// Convert payload to domain model
	request := s.convertMemberPayloadToDomain(p)

	writeCtx := ctx
	if p.SkipEnrichment {
		writeCtx = service.ContextWithSkipMemberEnrichment(ctx)
	} else {
		s.enrichMember(ctx, request)
	}

	// Execute use case
	response, err := s.committeeWriterOrchestrator.CreateMember(writeCtx, request, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertMemberDomainToFullResponse(response)

	return result, nil
}

// GetCommitteeMember retrieves a specific committee member by UID
func (s *committeeServicesrvc) GetCommitteeMember(ctx context.Context, p *committeeservice.GetCommitteeMemberPayload) (res *committeeservice.GetCommitteeMemberResult, err error) {

	slog.DebugContext(ctx, "committeeMemberService.get-committee-member",
		"committee_uid", p.UID,
		"member_uid", p.MemberUID,
	)

	// Execute use case
	committeeMember, revision, err := s.committeeReaderOrchestrator.GetMember(ctx, p.UID, p.MemberUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert domain model to GOA response
	result := s.convertMemberDomainToFullResponse(committeeMember)

	// Create result with ETag (using revision from NATS)
	revisionStr := fmt.Sprintf("%d", revision)
	res = &committeeservice.GetCommitteeMemberResult{
		Member: result,
		Etag:   &revisionStr,
	}

	return res, nil
}

// GetOrgCommitteeSeats lists a B2B org's committee seats across the membership project family for the
// Org Lens Board & Committee tab, paginated. The org filter is the sole scoping control (best-effort:
// org_id is self-reported until LFXV2-330).
func (s *committeeServicesrvc) GetOrgCommitteeSeats(ctx context.Context, p *committeeservice.GetOrgCommitteeSeatsPayload) (res *committeeservice.OrgCommitteeSeatPage, err error) {
	slog.DebugContext(ctx, "committeeService.get-org-committee-seats",
		"org_uid", p.UID,
		"project_uids_count", len(p.ProjectUids),
	)

	if s.orgSeatReader == nil {
		return nil, wrapError(ctx, errors.NewServiceUnavailable("org committee seat reader is not configured"))
	}
	if len(seatCursorKey) == 0 {
		// Page-token signing key not provisioned in this environment (ORG_SEAT_PAGE_TOKEN_HMAC_KEY).
		// Degrade this endpoint only — the rest of the service stays healthy — rather than serving
		// forgeable page tokens or crashing the pod.
		return nil, wrapError(ctx, errors.NewServiceUnavailable("org committee seat pagination is unavailable: ORG_SEAT_PAGE_TOKEN_HMAC_KEY is not configured"))
	}

	members, err := s.orgSeatReader.ListOrgCommitteeSeats(ctx, p.UID, p.ProjectUids)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Deterministic keyset order: sort by member UID so a page boundary is "after the last UID
	// returned". This is stable against deletes and re-fetches within a snapshot; it is NOT
	// insert-stable — because UIDs are random UUIDs, a seat inserted concurrently between page calls
	// may sort before the cursor and be missed (acceptable here: the org's seat set is near-static and
	// a fresh re-list converges). Unlike an offset, it never shifts or skips already-returned rows.
	slices.SortFunc(members, func(a, b *model.CommitteeMember) int {
		return strings.Compare(uidOf(a), uidOf(b))
	})

	pageSize := defaultOrgSeatPageSize
	if p.PageSize != nil && *p.PageSize > 0 {
		pageSize = *p.PageSize
	}
	if pageSize > maxOrgSeatPageSize {
		pageSize = maxOrgSeatPageSize
	}

	afterUID, errCursor := decodeSeatCursor(p.PageToken)
	if errCursor != nil {
		return nil, wrapError(ctx, errors.NewValidation("invalid page_token"))
	}

	page := &committeeservice.OrgCommitteeSeatPage{
		Seats: make([]*committeeservice.OrgCommitteeSeat, 0, pageSize),
	}
	var lastUID string
	for _, m := range members {
		if m == nil {
			continue
		}
		if afterUID != "" && m.UID <= afterUID { // keyset: skip up to and including the cursor UID
			continue
		}
		if len(page.Seats) >= pageSize {
			// At least one eligible row remains beyond this page → emit a next-page cursor.
			next := encodeSeatCursor(lastUID)
			page.PageToken = &next
			break
		}
		page.Seats = append(page.Seats, orgSeatFromMember(m))
		lastUID = m.UID
	}
	return page, nil
}

// uidOf safely reads a member UID for sorting (nil sorts first).
func uidOf(m *model.CommitteeMember) string {
	if m == nil {
		return ""
	}
	return m.UID
}

// org-committee-seat pagination defaults (LFXV2-1865).
const (
	defaultOrgSeatPageSize = 100
	maxOrgSeatPageSize     = 500
)

// seatCursorKey signs page tokens so clients treat them as opaque and cannot forge or hand-construct a
// cursor. It is sourced solely from ORG_SEAT_PAGE_TOKEN_HMAC_KEY and must be stable across replicas and
// rolling restarts so pagination survives horizontal scaling. There is deliberately no hardcoded
// fallback for any environment (a public in-repo key would make tokens forgeable, which is unacceptable
// even in shared dev): when the env var is unset the key is empty and GetOrgCommitteeSeats degrades to a
// 503 — only the org-seat read is disabled, the rest of the service stays healthy and never crashes.
var seatCursorKey = []byte(os.Getenv("ORG_SEAT_PAGE_TOKEN_HMAC_KEY"))

// encodeSeatCursor produces an opaque, HMAC-signed page token for the keyset position (the last UID
// returned): base64url( HMAC-SHA256(afterUID) || afterUID ).
func encodeSeatCursor(afterUID string) string {
	mac := hmac.New(sha256.New, seatCursorKey)
	mac.Write([]byte(afterUID))
	return base64.RawURLEncoding.EncodeToString(append(mac.Sum(nil), []byte(afterUID)...))
}

// decodeSeatCursor verifies a page token's signature and returns the keyset position. A nil/empty token
// is the first page (""). A malformed or tampered token is an error so the caller can return 400.
func decodeSeatCursor(token *string) (string, error) {
	if token == nil || *token == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(*token)
	if err != nil || len(raw) < sha256.Size {
		return "", fmt.Errorf("malformed page_token")
	}
	sig, afterUID := raw[:sha256.Size], raw[sha256.Size:]
	mac := hmac.New(sha256.New, seatCursorKey)
	mac.Write(afterUID)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", fmt.Errorf("invalid page_token signature")
	}
	return string(afterUID), nil
}

// isMembershipEntitlement reports whether a seat is org-reassignable — appointment type
// "Membership Entitlement" (case-insensitive), independent of committee type.
func isMembershipEntitlement(appointedBy string) bool {
	return strings.EqualFold(strings.TrimSpace(appointedBy), "Membership Entitlement")
}

// orgSeatFromMember maps a domain committee member to the Org Lens seat DTO, computing the
// endpoint-derived is_org_editable / reason from the appointment type.
func orgSeatFromMember(m *model.CommitteeMember) *committeeservice.OrgCommitteeSeat {
	editable := isMembershipEntitlement(m.AppointedBy)
	seat := &committeeservice.OrgCommitteeSeat{
		UID:               m.UID,
		CommitteeUID:      m.CommitteeUID,
		CommitteeName:     m.CommitteeName,
		CommitteeCategory: m.CommitteeCategory,
		FirstName:         m.FirstName,
		LastName:          m.LastName,
		Email:             m.Email,
		RoleName:          m.Role.Name,
		VotingStatus:      m.Voting.Status,
		AppointedBy:       m.AppointedBy,
		OrganizationID:    utils.NormalizeAccountSFID(m.Organization.ID),
		IsOrgEditable:     editable,
	}
	if m.JobTitle != "" {
		jt := m.JobTitle
		seat.JobTitle = &jt
	}
	// project_uid / project_slug are optional foundation tags on the model; only set them when present
	// so empty values aren't serialized as empty strings.
	if m.ProjectUID != "" {
		pu := m.ProjectUID
		seat.ProjectUID = &pu
	}
	if m.ProjectSlug != "" {
		ps := m.ProjectSlug
		seat.ProjectSlug = &ps
	}
	if m.Avatar != "" {
		av := m.Avatar
		seat.Avatar = &av
	}
	if m.Username != "" {
		un := m.Username
		seat.Username = &un
	}
	if !editable {
		reason := "This seat is held by foundation election or appointment, not by your organization's membership entitlement."
		seat.Reason = &reason
	}
	return seat
}

// ReassignOrgCommitteeSeat reassigns a Membership-Entitlement committee seat to a new holder for the
// Org Lens Board & Committee tab. It enforces, in code, that the seat belongs to the path org and that
// the seat is a Membership-Entitlement seat, then performs an atomic ReassignMember (create new +
// delete old, with rollback) preserving role/voting/appointed_by.
func (s *committeeServicesrvc) ReassignOrgCommitteeSeat(ctx context.Context, p *committeeservice.ReassignOrgCommitteeSeatPayload) (res *committeeservice.OrgCommitteeSeat, err error) {
	slog.DebugContext(ctx, "committeeService.reassign-org-committee-seat",
		"org_uid", p.UID,
		"member_uid", p.MemberUID,
		"committee_uid", p.CommitteeUID,
		"new_holder_email", redaction.RedactEmail(p.Email),
	)

	// Read the current member (system of record) for the entitlement guard + field preservation.
	member, rev, err := s.committeeReaderOrchestrator.GetMember(ctx, p.CommitteeUID, p.MemberUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Org-ownership guard: the seat must belong to the org named in the path. The edge only checks
	// b2b_org:{uid}#writer, so without this a caller authorized for one org could mutate another
	// org's seat by passing a foreign committee_uid/member_uid. Return NotFound to avoid leaking
	// the existence of seats outside the caller's org. Normalize both sides to the 18-char canonical
	// SFID so a 15-char stored organization.id still matches the 18-char path UID (same Salesforce record).
	if !strings.EqualFold(utils.NormalizeAccountSFID(member.Organization.ID), utils.NormalizeAccountSFID(p.UID)) {
		return nil, wrapError(ctx, errors.NewNotFound("seat not found"))
	}

	// Service-side entitlement guard: only "Membership Entitlement" seats are org-reassignable
	// (by appointment type, independent of committee type). FGA cannot express this.
	if !isMembershipEntitlement(member.AppointedBy) {
		return nil, wrapError(ctx, errors.NewForbidden("seat is not org-editable (not a Membership Entitlement seat)"))
	}

	// Build the replacement seat. A reassignment preserves the seat itself — its committee, role,
	// voting status, appointment type, and holding organization — and swaps only the person holding
	// it. Construct a FRESH base and copy ONLY those seat-defining fields (an allowlist), so future
	// additions to CommitteeMemberBase don't silently leak the previous holder's identity/profile/
	// timestamps onto the new seat. UID/Username are left empty (assigned by CreateMember;
	// username is resolved from the new holder's email).
	newMember := &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		Role:              member.Role,
		AppointedBy:       member.AppointedBy,
		Status:            member.Status,
		Voting:            member.Voting,
		Organization:      member.Organization,
		CommitteeUID:      member.CommitteeUID,
		CommitteeName:     member.CommitteeName,
		CommitteeCategory: member.CommitteeCategory,
		ProjectUID:        member.ProjectUID,
		ProjectSlug:       member.ProjectSlug,
		FirstName:         p.FirstName,
		LastName:          p.LastName,
		Email:             p.Email,
	}}

	// Atomic reassign via the orchestrator's defined create+delete+rollback pattern: the new holder
	// is created (full pipeline incl. by-committee + by-organization indices) and the old member is
	// deleted; if the delete fails the new member is rolled back so no duplicate seat remains.
	created, err := s.committeeWriterOrchestrator.ReassignMember(ctx, p.MemberUID, rev, newMember, false)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	return orgSeatFromMember(created), nil
}

// UpdateCommitteeMember updates an existing committee member
func (s *committeeServicesrvc) UpdateCommitteeMember(ctx context.Context, p *committeeservice.UpdateCommitteeMemberPayload) (res *committeeservice.CommitteeMemberFullWithReadonlyAttributes, err error) {

	slog.DebugContext(ctx, "committeeMemberService.update-committee-member",
		"committee_uid", p.UID,
		"member_uid", p.MemberUID,
		"email", redaction.RedactEmail(p.Email),
		"x_sync", p.XSync,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
			"member_uid", p.MemberUID,
		)
		return nil, wrapError(ctx, err)
	}

	// Convert payload to domain model
	committeeMember := s.convertPayloadToUpdateMember(p)

	writeCtx := ctx
	if p.SkipEnrichment {
		writeCtx = service.ContextWithSkipMemberEnrichment(ctx)
	} else {
		s.enrichMember(ctx, committeeMember)
	}

	// Execute use case
	updatedMember, err := s.committeeWriterOrchestrator.UpdateMember(writeCtx, committeeMember, parsedRevision, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Convert response to GOA result
	result := s.convertMemberDomainToFullResponse(updatedMember)

	return result, nil
}

// DeleteCommitteeMember removes a member from a committee
func (s *committeeServicesrvc) DeleteCommitteeMember(ctx context.Context, p *committeeservice.DeleteCommitteeMemberPayload) error {

	slog.DebugContext(ctx, "committeeMemberService.delete-committee-member",
		"committee_uid", p.UID,
		"member_uid", p.MemberUID,
		"x_sync", p.XSync,
	)

	// Parse ETag to get revision for optimistic locking
	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag",
			"error", err,
			"etag", p.IfMatch,
			"committee_uid", p.UID,
			"member_uid", p.MemberUID,
		)
		return wrapError(ctx, err)
	}

	// Execute delete use case
	errDelete := s.committeeWriterOrchestrator.DeleteMember(ctx, p.MemberUID, parsedRevision, p.XSync, p.SkipNotification)
	if errDelete != nil {
		return wrapError(ctx, errDelete)
	}

	return nil
}

// GetInvite retrieves a single invite by UID
func (s *committeeServicesrvc) GetInvite(ctx context.Context, p *committeeservice.GetInvitePayload) (*committeeservice.CommitteeInviteWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.get-invite",
		"committee_uid", p.UID,
		"invite_uid", p.InviteUID,
	)

	invite, _, err := s.storage.GetInvite(ctx, p.InviteUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if invite.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("invite not found in this committee"))
	}

	s.enrichInviteFromCommittee(ctx, invite, p.UID)

	return s.convertInviteDomainToResponse(invite), nil
}

// CreateInvite creates a new invite for a committee
func (s *committeeServicesrvc) CreateInvite(ctx context.Context, p *committeeservice.CreateInvitePayload) (*committeeservice.CommitteeInviteWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.create-invite",
		"committee_uid", p.UID,
		"invitee_email", redaction.RedactEmail(p.InviteeEmail),
	)

	// Verify committee exists.
	committeeBase, _, err := s.storage.GetBase(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Best-effort: settings drive organization_required; missing settings means false.
	committeeSettings, _, settingsErr := s.storage.GetSettings(ctx, p.UID)
	if settingsErr != nil {
		slog.WarnContext(ctx, "CreateInvite: failed to get committee settings for organization_required",
			"committee_uid", p.UID, "error", settingsErr)
	}
	orgRequired := committeeBase.EnableVoting || (committeeSettings != nil && committeeSettings.BusinessEmailRequired)

	var inviteOrgID, inviteOrgName, inviteOrgWebsite *string
	if p.Organization != nil {
		inviteOrgID = p.Organization.ID
		inviteOrgName = p.Organization.Name
		inviteOrgWebsite = p.Organization.Website
	}
	inviteOrganization := organizationPtrFromFields(inviteOrgID, inviteOrgName, inviteOrgWebsite)

	invite := &model.CommitteeInvite{
		UID:                  uuid.New().String(),
		CommitteeUID:         p.UID,
		CommitteeName:        committeeBase.Name,
		OrganizationRequired: orgRequired,
		InviteeEmail:         p.InviteeEmail,
		Organization:         inviteOrganization,
		Status:               "pending",
		CreatedAt:            time.Now().UTC(),
	}
	if p.Role != nil {
		invite.Role = *p.Role
	}

	// Check uniqueness — allow re-inviting if the existing invite is revoked
	_, errUnique := s.storage.UniqueInvite(ctx, invite)
	if errUnique != nil {
		var conflictErr errors.Conflict
		if !stderrors.As(errUnique, &conflictErr) {
			return nil, wrapError(ctx, errUnique)
		}

		// Uniqueness conflict: look for a revoked invite to reinstate
		existing, errList := s.storage.ListInvites(ctx, p.UID)
		if errList != nil {
			return nil, wrapError(ctx, errList)
		}
		var revokedInvite *model.CommitteeInvite
		for _, inv := range existing {
			if strings.EqualFold(inv.InviteeEmail, p.InviteeEmail) && inv.Status == "revoked" {
				revokedInvite = inv
				break
			}
		}
		if revokedInvite == nil {
			return nil, wrapError(ctx, errUnique)
		}

		// Reinstate the revoked invite by setting it back to pending
		_, rev, errGet := s.storage.GetInvite(ctx, revokedInvite.UID)
		if errGet != nil {
			return nil, wrapError(ctx, errGet)
		}
		revokedInvite.Status = "pending"
		revokedInvite.CommitteeName = committeeBase.Name
		revokedInvite.OrganizationRequired = orgRequired
		if p.Role != nil {
			revokedInvite.Role = *p.Role
		}
		if p.Organization != nil {
			revokedInvite.Organization = organizationPtrFromFields(inviteOrgID, inviteOrgName, inviteOrgWebsite)
		}
		if errUpdate := s.storage.UpdateInvite(ctx, revokedInvite, rev); errUpdate != nil {
			return nil, wrapError(ctx, errUpdate)
		}

		s.publishInviteIndexerMessage(ctx, model.ActionUpdated, revokedInvite, p.XSync)
		s.publishInviteAccessControlMessage(ctx, model.ActionUpdated, revokedInvite, p.XSync)
		s.dispatchInviteEmail(ctx, committeeBase, revokedInvite)

		return s.convertInviteDomainToResponse(revokedInvite), nil
	}

	if err := s.storage.CreateInvite(ctx, invite); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishInviteIndexerMessage(ctx, model.ActionCreated, invite, p.XSync)
	s.publishInviteAccessControlMessage(ctx, model.ActionCreated, invite, p.XSync)
	s.dispatchInviteEmail(ctx, committeeBase, invite)

	return s.convertInviteDomainToResponse(invite), nil
}

// inviteDispatchTimeout is the total budget for a single invite dispatch (name
// lookup + send). The name-resolve sub-timeout below consumes a portion of this
// budget, leaving the remainder for the actual SendInvite call.
const inviteDispatchTimeout = 5 * time.Second

// inviteNameResolveTimeout is the slice of the dispatch budget reserved for the
// best-effort auth-service name lookup. Must be less than inviteDispatchTimeout.
const inviteNameResolveTimeout = 2 * time.Second

// resolveInviteeDisplayName looks up a combined display name for the invitee via the
// auth service when the invitee already has an LFID. Returns an empty string when the
// invitee has no LFID yet or any lookup step fails — callers should treat an empty
// return as "name unknown" and proceed without it.
func (s *committeeServicesrvc) resolveInviteeDisplayName(ctx context.Context, email string) string {
	if s.userReader == nil {
		return ""
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	username, err := s.userReader.UsernameByEmail(ctx, email)
	if err != nil {
		var notFound errors.NotFound
		if !stderrors.As(err, &notFound) {
			slog.WarnContext(ctx, "username lookup failed for invite recipient — sending without name",
				"email", redaction.RedactEmail(email), "error", err)
		}
		return ""
	}
	if username == "" {
		return ""
	}
	meta, err := s.userReader.UserMetadataByPrincipal(ctx, username)
	if err != nil {
		slog.WarnContext(ctx, "user metadata lookup failed for invite recipient — sending without name",
			"username", redaction.Redact(username), "error", err)
		return ""
	}
	if meta == nil {
		return ""
	}
	if name := strings.TrimSpace(meta.Name); name != "" {
		return name
	}
	return strings.TrimSpace(meta.GivenName + " " + meta.FamilyName)
}

// dispatchInviteEmail publishes a send-invite request to the invite service so the
// invitee receives an email. Best-effort: failures are logged and do not fail the
// caller, since the invite record has already been persisted.
func (s *committeeServicesrvc) dispatchInviteEmail(ctx context.Context, committee *model.CommitteeBase, invite *model.CommitteeInvite) {
	if s.inviteSender == nil {
		slog.DebugContext(ctx, "invite sender not configured — skipping invite dispatch",
			"committee_uid", committee.UID, "invite_uid", invite.UID)
		return
	}

	// Single budget for the whole dispatch: name lookup (sub-timeout) + SendInvite.
	// Both operations share the same parent context so the total cannot exceed
	// inviteDispatchTimeout even when both are slow.
	dispatchCtx, dispatchCancel := context.WithTimeout(ctx, inviteDispatchTimeout)
	defer dispatchCancel()

	// Resolve the invitee's display name via the auth service when they already have
	// an LFID. Best-effort: lookup failures are logged and the invite still sends.
	resolveCtx, resolveCancel := context.WithTimeout(dispatchCtx, inviteNameResolveTimeout)
	recipientName := s.resolveInviteeDisplayName(resolveCtx, invite.InviteeEmail)
	resolveCancel()
	// Role on the invite record is the committee role applied after acceptance.
	// The Role field on SendInviteRequest is the invite-service permission grant
	// — its vocabulary is Manage/View/Member, not committee roles like "chair".
	// Match the parallel "add committee member" path in message_handler.go
	// sendMemberInvite and pass "Member".
	_, err := s.inviteSender.SendInvite(dispatchCtx, inviteapi.SendInviteRequest{
		Recipient: &inviteapi.Recipient{
			Email: strings.TrimSpace(invite.InviteeEmail),
			Name:  recipientName,
		},
		Inviter: &inviteapi.Inviter{
			Name: "A committee administrator",
		},
		Resource: &inviteapi.Resource{
			UID:  committee.UID,
			Name: committee.Name,
			Type: "group",
		},
		Role:      "Member",
		ReturnURL: strings.TrimRight(s.lfxSelfServeBaseURL, "/") + "/project/groups/" + committee.UID,
	})
	if err != nil {
		slog.WarnContext(ctx, "failed to dispatch committee invite email",
			"error", err, "committee_uid", committee.UID, "invite_uid", invite.UID)
		return
	}
	slog.DebugContext(ctx, "dispatched committee invite email",
		"committee_uid", committee.UID, "invite_uid", invite.UID)
}

// RevokeInvite revokes a pending or declined invite
func (s *committeeServicesrvc) RevokeInvite(ctx context.Context, p *committeeservice.RevokeInvitePayload) error {
	slog.DebugContext(ctx, "committeeService.revoke-invite",
		"committee_uid", p.UID,
		"invite_uid", p.InviteUID,
	)

	invite, rev, err := s.storage.GetInvite(ctx, p.InviteUID)
	if err != nil {
		return wrapError(ctx, err)
	}

	if invite.CommitteeUID != p.UID {
		return wrapError(ctx, errors.NewNotFound("invite not found in this committee"))
	}

	if invite.Status == "accepted" || invite.Status == "revoked" {
		return wrapError(ctx, errors.NewConflict("invite has already been processed"))
	}

	invite.Status = "revoked"
	s.enrichInviteFromCommittee(ctx, invite, p.UID)
	if err := s.storage.UpdateInvite(ctx, invite, rev); err != nil {
		return wrapError(ctx, err)
	}

	s.publishInviteIndexerMessage(ctx, model.ActionUpdated, invite, false)
	s.publishInviteAccessControlMessage(ctx, model.ActionUpdated, invite, false)

	return nil
}

// AcceptInvite accepts a pending or previously-declined invite and creates a committee member
func (s *committeeServicesrvc) AcceptInvite(ctx context.Context, p *committeeservice.AcceptInvitePayload) (*committeeservice.CommitteeMemberFullWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.accept-invite",
		"committee_uid", p.UID,
		"invite_uid", p.InviteUID,
	)

	invite, rev, err := s.storage.GetInvite(ctx, p.InviteUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if invite.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("invite not found in this committee"))
	}

	// Enforce invite ownership: only the invitee can accept their own invite
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	if !strings.EqualFold(email, invite.InviteeEmail) {
		return nil, wrapError(ctx, errors.NewForbidden("you are not the invitee for this invite"))
	}

	if invite.Status == "accepted" || invite.Status == "revoked" {
		return nil, wrapError(ctx, errors.NewConflict("invite has already been processed"))
	}

	// Create the committee member first — if this fails the invite remains pending/declined
	// and the invitee can retry without being stuck in an inconsistent state.
	// PrincipalContextID is the invitee's LFX username (Heimdall principal claim).
	var acceptOrgID, acceptOrgName, acceptOrgWebsite *string
	if p.Body != nil && p.Body.Organization != nil {
		acceptOrgID = p.Body.Organization.ID
		acceptOrgName = p.Body.Organization.Name
		acceptOrgWebsite = p.Body.Organization.Website
	}
	memberOrganization := acceptInviteOrganization(invite, acceptOrgID, acceptOrgName, acceptOrgWebsite)

	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: invite.CommitteeUID,
			Email:        invite.InviteeEmail,
			Role:         model.CommitteeMemberRole{Name: invite.Role},
			Status:       "Active",
			Organization: memberOrganization,
		},
	}
	s.enrichMember(ctx, member)

	response, err := s.committeeWriterOrchestrator.CreateMember(ctx, member, false)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Member created successfully — now mark the invite accepted.
	invite.Status = "accepted"
	s.enrichInviteFromCommittee(ctx, invite, p.UID)
	if err := s.storage.UpdateInvite(ctx, invite, rev); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishInviteIndexerMessage(ctx, model.ActionUpdated, invite, false)
	// Re-publish the access control message on accept: the invitee may now have an LFID
	// account (e.g. they registered via the LFID invite flow), so this resolves and writes
	// the invitee tuple if it wasn't written at invite creation time.
	s.publishInviteAccessControlMessage(ctx, model.ActionUpdated, invite, false)

	return s.convertMemberDomainToFullResponse(response), nil
}

// DeclineInvite declines a pending invite
func (s *committeeServicesrvc) DeclineInvite(ctx context.Context, p *committeeservice.DeclineInvitePayload) (*committeeservice.CommitteeInviteWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.decline-invite",
		"committee_uid", p.UID,
		"invite_uid", p.InviteUID,
	)

	invite, rev, err := s.storage.GetInvite(ctx, p.InviteUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if invite.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("invite not found in this committee"))
	}

	// Enforce invite ownership: only the invitee can decline their own invite
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	if !strings.EqualFold(email, invite.InviteeEmail) {
		return nil, wrapError(ctx, errors.NewForbidden("you are not the invitee for this invite"))
	}

	if invite.Status != "pending" {
		return nil, wrapError(ctx, errors.NewConflict("invite has already been processed"))
	}

	invite.Status = "declined"
	s.enrichInviteFromCommittee(ctx, invite, p.UID)
	if err := s.storage.UpdateInvite(ctx, invite, rev); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishInviteIndexerMessage(ctx, model.ActionUpdated, invite, false)
	s.publishInviteAccessControlMessage(ctx, model.ActionUpdated, invite, false)

	return s.convertInviteDomainToResponse(invite), nil
}

// GetApplication retrieves a single application by UID
func (s *committeeServicesrvc) GetApplication(ctx context.Context, p *committeeservice.GetApplicationPayload) (*committeeservice.CommitteeApplicationWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.get-application",
		"committee_uid", p.UID,
		"application_uid", p.ApplicationUID,
	)

	application, _, err := s.storage.GetApplication(ctx, p.ApplicationUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if application.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("application not found in this committee"))
	}

	return s.convertApplicationDomainToResponse(application), nil
}

// SubmitApplication submits an application to join a committee
func (s *committeeServicesrvc) SubmitApplication(ctx context.Context, p *committeeservice.SubmitApplicationPayload) (*committeeservice.CommitteeApplicationWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.submit-application",
		"committee_uid", p.UID,
	)

	// Verify committee exists and get base to check join_mode
	base, _, err := s.storage.GetBase(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if base.JoinMode != "application" {
		return nil, wrapError(ctx, errors.NewForbidden("committee does not accept applications"))
	}

	// Resolve the applicant's email via the auth-service NATS lookup.
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	slog.DebugContext(ctx, "committeeService.submit-application: resolved applicant",
		"committee_uid", p.UID,
		"applicant_email_redacted", redaction.RedactEmail(email),
	)

	application := &model.CommitteeApplication{
		UID:            uuid.New().String(),
		CommitteeUID:   p.UID,
		ApplicantEmail: email,
		Status:         "pending",
		CreatedAt:      time.Now().UTC(),
	}
	if p.Message != nil {
		application.Message = *p.Message
	}

	// Check uniqueness — allow reapplying if the existing application is rejected
	_, errUnique := s.storage.UniqueApplication(ctx, application)
	if errUnique != nil {
		var conflictErr errors.Conflict
		if !stderrors.As(errUnique, &conflictErr) {
			return nil, wrapError(ctx, errUnique)
		}

		// Uniqueness conflict: look for a rejected application to reinstate
		existing, errList := s.storage.ListApplications(ctx, p.UID)
		if errList != nil {
			return nil, wrapError(ctx, errList)
		}
		var rejectedApp *model.CommitteeApplication
		for _, app := range existing {
			if strings.EqualFold(app.ApplicantEmail, email) && app.Status == "rejected" {
				rejectedApp = app
				break
			}
		}
		if rejectedApp == nil {
			return nil, wrapError(ctx, errUnique)
		}

		// Reinstate the rejected application by setting it back to pending
		_, rev, errGet := s.storage.GetApplication(ctx, rejectedApp.UID)
		if errGet != nil {
			return nil, wrapError(ctx, errGet)
		}
		rejectedApp.Status = "pending"
		rejectedApp.ReviewerNotes = ""
		if p.Message != nil {
			rejectedApp.Message = *p.Message
		}
		if errUpdate := s.storage.UpdateApplication(ctx, rejectedApp, rev); errUpdate != nil {
			return nil, wrapError(ctx, errUpdate)
		}

		s.publishApplicationIndexerMessage(ctx, model.ActionUpdated, rejectedApp, p.XSync)
		if p.Notify {
			s.publishApplicationEvent(ctx, model.ActionCreated, rejectedApp)
		}

		return s.convertApplicationDomainToResponse(rejectedApp), nil
	}

	if err := s.storage.CreateApplication(ctx, application); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishApplicationIndexerMessage(ctx, model.ActionCreated, application, p.XSync)
	if p.Notify {
		s.publishApplicationEvent(ctx, model.ActionCreated, application)
	}

	return s.convertApplicationDomainToResponse(application), nil
}

// ApproveApplication approves a pending application and creates a committee member
func (s *committeeServicesrvc) ApproveApplication(ctx context.Context, p *committeeservice.ApproveApplicationPayload) (*committeeservice.CommitteeMemberFullWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.approve-application",
		"committee_uid", p.UID,
		"application_uid", p.ApplicationUID,
	)

	application, rev, err := s.storage.GetApplication(ctx, p.ApplicationUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if application.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("application not found in this committee"))
	}

	if application.Status != "pending" {
		return nil, wrapError(ctx, errors.NewConflict("application has already been processed"))
	}

	// Create the committee member first — if this fails the application remains pending
	// and the reviewer can retry without being stuck in an inconsistent state.
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: application.CommitteeUID,
			Email:        application.ApplicantEmail,
			Status:       "Active",
		},
	}

	// Resolve username and profile fields from the applicant's email.
	s.enrichMember(ctx, member)
	s.enrichMemberOrganization(ctx, member)

	// When the caller opts in to the application-accepted email (notify: true)
	// and the applicant has a resolved LFID, suppress the generic member-added
	// role notification — the accepted email already covers the approval.
	// Email-only applicants (no LFID) are excluded from this suppression so the
	// invite-service path still fires for them.
	if p.Notify && member.Username != "" {
		member.SkipNotification = true
	}

	response, err := s.committeeWriterOrchestrator.CreateMember(ctx, member, false)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Member created successfully — now mark the application approved.
	application.Status = "approved"
	if p.ReviewerNotes != nil {
		application.ReviewerNotes = *p.ReviewerNotes
	}

	if err := s.storage.UpdateApplication(ctx, application, rev); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishApplicationIndexerMessage(ctx, model.ActionUpdated, application, false)
	if p.Notify {
		s.publishApplicationEvent(ctx, model.ActionUpdated, application)
	}

	return s.convertMemberDomainToFullResponse(response), nil
}

// RejectApplication rejects a pending application
func (s *committeeServicesrvc) RejectApplication(ctx context.Context, p *committeeservice.RejectApplicationPayload) (*committeeservice.CommitteeApplicationWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.reject-application",
		"committee_uid", p.UID,
		"application_uid", p.ApplicationUID,
	)

	application, rev, err := s.storage.GetApplication(ctx, p.ApplicationUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if application.CommitteeUID != p.UID {
		return nil, wrapError(ctx, errors.NewNotFound("application not found in this committee"))
	}

	if application.Status != "pending" {
		return nil, wrapError(ctx, errors.NewConflict("application has already been processed"))
	}

	application.Status = "rejected"
	if p.ReviewerNotes != nil {
		application.ReviewerNotes = *p.ReviewerNotes
	}

	if err := s.storage.UpdateApplication(ctx, application, rev); err != nil {
		return nil, wrapError(ctx, err)
	}

	s.publishApplicationIndexerMessage(ctx, model.ActionUpdated, application, false)
	if p.Notify {
		s.publishApplicationEvent(ctx, model.ActionUpdated, application)
	}

	return s.convertApplicationDomainToResponse(application), nil
}

// JoinCommittee allows self-join when join_mode is "open"
func (s *committeeServicesrvc) JoinCommittee(ctx context.Context, p *committeeservice.JoinCommitteePayload) (*committeeservice.CommitteeMemberFullWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.join-committee", "committee_uid", p.UID)

	// Verify committee exists and get base to check join_mode
	base, _, err := s.storage.GetBase(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	if base.JoinMode != "open" {
		return nil, wrapError(ctx, errors.NewForbidden("committee join_mode is not open"))
	}

	// PrincipalContextID is the caller's LFX username (Heimdall principal claim).
	// Coordinated with the LFXV2-1964 migration so FGA tuple subjects match authorization checks.
	username, _ := ctx.Value(constants.PrincipalContextID).(string)
	if username == "" {
		return nil, wrapError(ctx, errors.NewValidation("unable to determine user username from identity"))
	}

	// Resolve the user's email via the auth-service NATS lookup.
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	// Create member via the existing orchestrator
	member := &model.CommitteeMember{
		CommitteeMemberBase: model.CommitteeMemberBase{
			CommitteeUID: p.UID,
			Email:        email,
			Status:       "Active",
		},
	}
	s.enrichMember(ctx, member)
	s.enrichMemberOrganization(ctx, member)

	response, err := s.committeeWriterOrchestrator.CreateMember(ctx, member, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	return s.convertMemberDomainToFullResponse(response), nil
}

// LeaveCommittee allows a member to leave a committee
func (s *committeeServicesrvc) LeaveCommittee(ctx context.Context, p *committeeservice.LeaveCommitteePayload) error {
	slog.DebugContext(ctx, "committeeService.leave-committee", "committee_uid", p.UID)

	// Resolve the user's email via the auth-service NATS lookup.
	email, err := s.resolveCallerEmail(ctx)
	if err != nil {
		return wrapError(ctx, err)
	}

	// Find the member by listing all members and matching email
	members, err := s.storage.ListMembersByCommittee(ctx, p.UID)
	if err != nil {
		return wrapError(ctx, err)
	}

	var memberToRemove *model.CommitteeMember
	for _, m := range members {
		if strings.EqualFold(m.Email, email) {
			memberToRemove = m
			break
		}
	}

	if memberToRemove == nil {
		return wrapError(ctx, errors.NewNotFound("you are not a member of this committee"))
	}

	// Get revision for optimistic locking
	rev, err := s.storage.GetMemberRevision(ctx, memberToRemove.UID)
	if err != nil {
		return wrapError(ctx, err)
	}

	// Use orchestrator (not direct storage) to ensure event publishing and cleanup
	if err := s.committeeWriterOrchestrator.DeleteMember(ctx, memberToRemove.UID, rev, p.XSync, false); err != nil {
		return wrapError(ctx, err)
	}

	return nil
}

// Check if the service is able to take inbound requests.
func (s *committeeServicesrvc) Readyz(ctx context.Context) (res []byte, err error) {
	// Check NATS readiness
	if err := s.storage.IsReady(ctx); err != nil {
		slog.ErrorContext(ctx, "service not ready", "error", err)
		return nil, err // This will automatically return ServiceUnavailable
	}

	return []byte("OK\n"), nil
}

// Check if the service is alive.
func (s *committeeServicesrvc) Livez(ctx context.Context) (res []byte, err error) {
	// This always returns as long as the service is still running. As this
	// endpoint is expected to be used as a Kubernetes liveness check, this
	// service must likewise self-detect non-recoverable errors and
	// self-terminate.
	return []byte("OK\n"), nil
}

// resolveCallerEmail looks up the primary email for the authenticated caller by converting
// their LFX username (principal) to an Auth0 sub and sending it to auth-service via NATS
// (lfx.auth-service.user_emails.read).
func (s *committeeServicesrvc) resolveCallerEmail(ctx context.Context) (string, error) {
	if s.userReader == nil {
		return "", errors.NewServiceUnavailable("user reader is not configured")
	}

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	if principal == "" {
		return "", errors.NewValidation("unable to determine user identity from token")
	}

	authSub := authpkg.MapUsernameToAuthSub(principal)
	if authSub == "" {
		return "", errors.NewValidation("unable to determine user identity from token")
	}

	userEmails, err := s.userReader.EmailsByAuthToken(ctx, authSub)
	if err != nil {
		return "", err
	}

	if userEmails.PrimaryEmail == "" {
		return "", errors.NewValidation("no primary email found for user")
	}

	return userEmails.PrimaryEmail, nil
}

// enrichInviteFromCommittee populates invite fields derived from the committee.
// It sets CommitteeName when missing and refreshes OrganizationRequired from the
// committee's current settings (voting enabled or business email required).
// Best-effort: a GetBase failure leaves the invite fully unchanged. A GetSettings
// failure leaves OrganizationRequired unchanged (CommitteeName may already have
// been backfilled). All errors are logged.
func (s *committeeServicesrvc) enrichInviteFromCommittee(ctx context.Context, invite *model.CommitteeInvite, committeeUID string) {
	cb, _, err := s.storage.GetBase(ctx, committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "enrichInviteFromCommittee: failed to get committee base",
			"committee_uid", committeeUID, "error", err)
		return
	}
	if invite.CommitteeName == "" {
		invite.CommitteeName = cb.Name
	}
	settings, _, settingsErr := s.storage.GetSettings(ctx, committeeUID)
	if settingsErr != nil {
		// Leave OrganizationRequired unchanged on a transient settings failure rather than
		// clobbering a correctly-stored value with one derived from nil settings.
		slog.WarnContext(ctx, "enrichInviteFromCommittee: failed to get committee settings",
			"committee_uid", committeeUID, "error", settingsErr)
		return
	}
	invite.OrganizationRequired = cb.EnableVoting || settings.BusinessEmailRequired
}

// publishInviteIndexerMessage publishes an indexer message for invite operations.
// Publishing is best-effort: failures are logged but do not fail the request.
// IndexingConfig is required because the indexer is data-agnostic; publishers supply all indexing metadata.
func (s *committeeServicesrvc) publishInviteIndexerMessage(ctx context.Context, action model.MessageAction, invite *model.CommitteeInvite, sync bool) {
	tags := invite.Tags()
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             invite.UID,
		AccessCheckObject:    fmt.Sprintf("committee_invite:%s", invite.UID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", invite.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", invite.CommitteeUID)},
		SortName:             invite.InviteeEmail,
		NameAndAliases:       []string{invite.InviteeEmail},
		Fulltext:             invite.InviteeEmail,
		Tags:                 tags,
	}

	var data any
	if action == model.ActionDeleted {
		data = invite.UID
	} else {
		public := false
		indexingConfig.Public = &public
		data = invite
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action:         action,
		Tags:           tags,
		IndexingConfig: indexingConfig,
	}

	built, err := indexerMessage.Build(ctx, data)
	if err != nil {
		slog.WarnContext(ctx, "failed to build invite indexer message",
			"error", err,
			"action", string(action),
			"invite_uid", invite.UID,
		)
		return
	}

	if pubErr := s.publisher.Indexer(ctx, constants.IndexCommitteeInviteSubject, built, sync); pubErr != nil {
		slog.WarnContext(ctx, "failed to publish invite indexer message",
			"error", pubErr,
			"action", string(action),
			"invite_uid", invite.UID,
		)
	}
}

// publishInviteAccessControlMessage publishes an FGA access control message for a committee invite.
// For create/update it writes update_access tuples so that:
//   - the parent committee relation is set (enables auditor from committee visibility), and
//   - the invitee relation is set when the email resolves to an LFID username.
//
// For delete it writes a delete_access message to clean up all tuples for the invite object.
// Publishing is best-effort: failures are logged but do not fail the request.
func (s *committeeServicesrvc) publishInviteAccessControlMessage(ctx context.Context, action model.MessageAction, invite *model.CommitteeInvite, sync bool) {
	var msg fgatypes.GenericFGAMessage

	if action == model.ActionDeleted {
		msg = fgatypes.GenericFGAMessage{
			ObjectType: "committee_invite",
			Operation:  "delete_access",
			Data:       fgatypes.GenericDeleteData{UID: invite.UID},
		}
	} else {
		data := fgatypes.GenericAccessData{
			UID: invite.UID,
			// References the parent committee so that auditor from committee resolves.
			References: map[string][]string{
				constants.RelationCommittee: {invite.CommitteeUID},
			},
		}

		// Resolve the invitee email to an LFID username and, if found, include the
		// invitee relation tuple. Mirrors the committee member path: unresolved emails
		// (no LFID account yet) skip the tuple; it will be written on invite acceptance.
		if s.userReader != nil {
			if username, err := s.userReader.UsernameByEmail(ctx, invite.InviteeEmail); err == nil && username != "" {
				data.Relations = map[string][]string{
					constants.RelationInvitee: {username},
				}
			} else if err != nil {
				slog.DebugContext(ctx, "invite access control: username lookup failed, invitee tuple skipped",
					"error", err,
					"invite_uid", invite.UID,
					"email", redaction.RedactEmail(invite.InviteeEmail),
				)
			}
		}

		// ExcludeRelations prevents fga-sync from deleting a previously-written invitee
		// tuple when we have no resolved username (e.g. transient user-service outage or
		// unregistered email). Mirrors the committee member ExcludeRelations pattern.
		if data.Relations == nil {
			data.ExcludeRelations = []string{constants.RelationInvitee}
		}

		msg = fgatypes.GenericFGAMessage{
			ObjectType: "committee_invite",
			Operation:  "update_access",
			Data:       data,
		}
	}

	subject := fgaconstants.GenericUpdateAccessSubject
	if action == model.ActionDeleted {
		subject = fgaconstants.GenericDeleteAccessSubject
	}

	if pubErr := s.publisher.Access(ctx, subject, msg, sync); pubErr != nil {
		slog.WarnContext(ctx, "failed to publish invite access control message",
			"error", pubErr,
			"action", string(action),
			"invite_uid", invite.UID,
		)
	}
}

// publishApplicationIndexerMessage publishes an indexer message for application operations.
// Publishing is best-effort: failures are logged but do not fail the request.
// IndexingConfig is required because the indexer is data-agnostic; publishers supply all indexing metadata.
func (s *committeeServicesrvc) publishApplicationIndexerMessage(ctx context.Context, action model.MessageAction, application *model.CommitteeApplication, sync bool) {
	tags := application.Tags()
	indexingConfig := &indexerTypes.IndexingConfig{
		ObjectID:             application.UID,
		AccessCheckObject:    fmt.Sprintf("committee:%s", application.CommitteeUID),
		AccessCheckRelation:  "viewer",
		HistoryCheckObject:   fmt.Sprintf("committee:%s", application.CommitteeUID),
		HistoryCheckRelation: "auditor",
		ParentRefs:           []string{fmt.Sprintf("committee:%s", application.CommitteeUID)},
		Fulltext:             application.Message,
		Tags:                 tags,
	}

	var data any
	if action == model.ActionDeleted {
		data = application.UID
	} else {
		public := false
		indexingConfig.Public = &public
		data = application
	}

	indexerMessage := model.CommitteeIndexerMessage{
		Action:         action,
		Tags:           tags,
		IndexingConfig: indexingConfig,
	}

	built, err := indexerMessage.Build(ctx, data)
	if err != nil {
		slog.WarnContext(ctx, "failed to build application indexer message",
			"error", err,
			"action", string(action),
			"application_uid", application.UID,
		)
		return
	}

	if pubErr := s.publisher.Indexer(ctx, constants.IndexCommitteeApplicationSubject, built, sync); pubErr != nil {
		slog.WarnContext(ctx, "failed to publish application indexer message",
			"error", pubErr,
			"action", string(action),
			"application_uid", application.UID,
		)
	}
}

// publishApplicationEvent publishes a domain event for application state changes so that
// downstream notification handlers can react to them. Best-effort: failures are logged but
// do not fail the HTTP request.
func (s *committeeServicesrvc) publishApplicationEvent(ctx context.Context, action model.MessageAction, application *model.CommitteeApplication) {
	event := model.CommitteeEvent{}
	built, err := event.Build(ctx, model.ResourceCommitteeApplication, action, application)
	if err != nil {
		slog.WarnContext(ctx, "failed to build application event",
			"error", err,
			"action", string(action),
			"application_uid", application.UID,
		)
		return
	}
	if err := s.publisher.Event(ctx, built.Subject, built, false); err != nil {
		slog.WarnContext(ctx, "failed to publish application event",
			"error", err,
			"action", string(action),
			"application_uid", application.UID,
		)
	}
}

// validateIdentityFields returns a validation error if any writer or auditor entry has
// neither a username nor an email address, since such entries cannot be resolved or stored.
// Email-only entries are allowed — enrichAllRoleFields may fill in the username if the email
// resolves, but an entry is still valid even when the email is unresolvable (Username stays "").
// Username-only entries (no email) are allowed — the caller-supplied LFID is persisted as-is.
func validateIdentityFields(writers, auditors []*committeeservice.CommitteeUser) error {
	check := func(role string, users []*committeeservice.CommitteeUser) error {
		for i, u := range users {
			if u == nil {
				continue
			}
			hasUsername := u.Username != nil && strings.TrimSpace(*u.Username) != ""
			hasEmail := u.Email != nil && strings.TrimSpace(*u.Email) != ""
			if !hasUsername && !hasEmail {
				return errors.NewValidation(fmt.Sprintf("%s[%d]: username or email is required", role, i))
			}
		}
		return nil
	}
	if err := check("writers", writers); err != nil {
		return err
	}
	return check("auditors", auditors)
}

// enrichAllRoleFields overwrites the Username, Name, and Avatar fields on every CommitteeUser
// across all supplied slices with authoritative values from the auth service.
// Each unique email is looked up exactly once; at most 8 lookups run concurrently.
// Misses (unknown email or lookup not found) clear Username to "" so no stale LFID is persisted.
// Entries with only a caller-supplied Username and no Email are left untouched —
// they pass through and are persisted as-is.
// Username transport errors fail the request so incorrect LFIDs are never silently kept.
// Metadata (name/avatar) errors only log a warning; display fields do not block the write.
func (s *committeeServicesrvc) enrichAllRoleFields(ctx context.Context, slices ...[]*committeeservice.CommitteeUser) error {
	if s.userReader == nil {
		return errors.NewServiceUnavailable("user reader is not configured")
	}

	byEmail := make(map[string][]*committeeservice.CommitteeUser)

	for _, slice := range slices {
		for _, u := range slice {
			if u == nil {
				continue
			}
			normEmail := ""
			if u.Email != nil {
				normEmail = strings.ToLower(strings.TrimSpace(*u.Email))
			}
			if normEmail == "" {
				continue
			}
			byEmail[normEmail] = append(byEmail[normEmail], u)
		}
	}

	if len(byEmail) == 0 {
		return nil
	}

	// enrichResult holds the resolved identity and profile for one email address.
	type enrichResult struct {
		username string
		metadata *model.UserMetadata
	}

	results := make(map[string]enrichResult, len(byEmail))
	var mu sync.Mutex
	const maxConcurrent = 8
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrent)

	for email := range byEmail {
		g.Go(func() error {
			username, err := s.userReader.UsernameByEmail(gCtx, email)
			if err != nil {
				var notFound errors.NotFound
				if stderrors.As(err, &notFound) {
					mu.Lock()
					results[email] = enrichResult{}
					mu.Unlock()
					return nil
				}
				return err
			}
			if username == "" {
				// Empty username with no error is treated as not found — no valid LFID to persist.
				mu.Lock()
				results[email] = enrichResult{}
				mu.Unlock()
				return nil
			}

			// Username resolved — now fetch authoritative profile data.
			// Metadata failures are non-fatal: display fields must not block the write.
			var meta *model.UserMetadata
			m, metaErr := s.userReader.UserMetadataByPrincipal(gCtx, username)
			if metaErr != nil {
				slog.WarnContext(gCtx, "user metadata lookup failed; name/avatar will not be enriched",
					"email", redaction.RedactEmail(email), "username", redaction.Redact(username), "error", metaErr)
			} else {
				meta = m
			}

			mu.Lock()
			results[email] = enrichResult{username: username, metadata: meta}
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return errors.NewUnexpected("enriching committee user role fields failed", err)
	}

	// Apply resolved username, name, and avatar. Only overwrite name/avatar when the auth service
	// returned a non-empty value so a partial metadata response cannot erase stored display fields.
	// UserMetadata carries additional fields (JobTitle, Organization, etc.) that CommitteeUser does
	// not currently model; they are fetched now so the domain struct is complete for future callers.
	for email, users := range byEmail {
		r := results[email]
		for _, u := range users {
			username := r.username
			u.Username = &username
			if r.metadata != nil {
				if r.metadata.Name != "" {
					name := r.metadata.Name
					u.Name = &name
				}
				if r.metadata.Picture != "" {
					picture := r.metadata.Picture
					u.Avatar = &picture
				}
			}
		}
	}
	return nil
}

// enrichMember resolves the LFID username and profile metadata for a member from their email
// address. When email is present the auth-service lookup always runs, overriding any
// caller-supplied plain LFID so only registered usernames are persisted.
// All lookups are best-effort: failures log a warning and leave the field unchanged so the
// caller's write is never blocked by an enrichment error.
// FirstName and LastName are only overwritten when the auth service returns a non-empty value
// and the caller did not supply them, so caller-provided display names are preserved.
func (s *committeeServicesrvc) enrichMember(ctx context.Context, member *model.CommitteeMember) {
	if s.userReader == nil {
		return
	}
	email := strings.ToLower(strings.TrimSpace(member.Email))
	if email == "" {
		return
	}

	// Clear any caller-supplied value so a failed lookup never leaves a plain LFID at rest.
	member.Username = ""

	// enrichMember is intentionally best-effort: transport errors warn and continue rather than
	// failing the request. Individual member writes (create/update/approve) should not be blocked
	// by a transient auth-service outage — the member is stored without an enriched LFID.
	username, err := s.userReader.UsernameByEmail(ctx, email)
	if err != nil {
		var notFound errors.NotFound
		if !stderrors.As(err, &notFound) {
			slog.WarnContext(ctx, "username lookup failed; member will be stored without LFID",
				"email", redaction.RedactEmail(email), "error", err)
		}
		return
	}
	if username == "" {
		return
	}
	member.Username = username

	meta, metaErr := s.userReader.UserMetadataByPrincipal(ctx, username)
	if metaErr != nil {
		slog.WarnContext(ctx, "user metadata lookup failed; member profile will not be enriched",
			"username", redaction.Redact(username), "error", metaErr)
		return
	}
	if meta == nil {
		return
	}
	if member.FirstName == "" && meta.GivenName != "" {
		member.FirstName = meta.GivenName
	}
	if member.LastName == "" && meta.FamilyName != "" {
		member.LastName = meta.FamilyName
	}
	// Empty picture clears a removed photo; a failed lookup returned early above (fail-soft).
	member.Avatar = meta.Picture
}

// lookupUserMetadata fetches profile metadata from auth-service, trying each lookup key until one succeeds.
// Keys may be an LFID username, JWT principal, or auth0| sub — auth-service user_metadata.read accepts all of these.
func (s *committeeServicesrvc) lookupUserMetadata(ctx context.Context, keys ...string) *model.UserMetadata {
	if s.userReader == nil {
		return nil
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		meta, err := s.userReader.UserMetadataByPrincipal(ctx, key)
		if err == nil && meta != nil {
			return meta
		}
	}
	return nil
}

// enrichMemberOrganization fills organization.Name from auth-service profile metadata when
// missing. It does not set ID or website; org-gated committees still require a complete
// organization from the invite record or accept payload before member validation runs.
func (s *committeeServicesrvc) enrichMemberOrganization(ctx context.Context, member *model.CommitteeMember) {
	if member.Organization.ID != "" {
		return
	}
	if member.Organization.Name != "" {
		return
	}

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	metadataKeys := make([]string, 0, 3)
	if member.Username != "" {
		metadataKeys = append(metadataKeys, member.Username)
	}
	if principal != "" {
		metadataKeys = append(metadataKeys, principal)
		if authSub := authpkg.MapUsernameToAuthSub(principal); authSub != "" && authSub != principal {
			metadataKeys = append(metadataKeys, authSub)
		}
	}
	if meta := s.lookupUserMetadata(ctx, metadataKeys...); meta != nil && strings.TrimSpace(meta.Organization) != "" {
		member.Organization.Name = strings.TrimSpace(meta.Organization)
	}
}

// NewCommitteeService returns the committee-service service implementation with dependencies.
func NewCommitteeService(
	createCommitteeUseCase service.CommitteeWriter,
	readCommitteeUseCase service.CommitteeReader,
	authService port.Authenticator,
	storage port.CommitteeReaderWriter,
	publisher port.CommitteePublisher,
	inviteSender port.InviteSender,
	lfxSelfServeBaseURL string,
	userReader port.UserReader,
	linkReader service.CommitteeLinkDataReader,
	linkWriter service.CommitteeLinkDataWriter,
	docReader service.CommitteeDocumentDataReader,
	docWriter service.CommitteeDocumentDataWriter,
	weeklyBriefReader service.GroupWeeklyBriefDataReader,
	weeklyBriefGenerator service.GroupWeeklyBriefGenerator,
	weeklyBriefWriter service.GroupWeeklyBriefDataWriter,
	orgSeatReader port.OrgCommitteeSeatReader,
) committeeservice.Service {
	return &committeeServicesrvc{
		committeeWriterOrchestrator: createCommitteeUseCase,
		committeeReaderOrchestrator: readCommitteeUseCase,
		auth:                        authService,
		storage:                     storage,
		publisher:                   publisher,
		inviteSender:                inviteSender,
		lfxSelfServeBaseURL:         lfxSelfServeBaseURL,
		userReader:                  userReader,
		linkReader:                  linkReader,
		linkWriter:                  linkWriter,
		docReader:                   docReader,
		docWriter:                   docWriter,
		weeklyBriefReader:           weeklyBriefReader,
		weeklyBriefGenerator:        weeklyBriefGenerator,
		weeklyBriefWriter:           weeklyBriefWriter,
		orgSeatReader:               orgSeatReader,
	}
}

// GetCurrentWeeklyBrief returns the working-group weekly brief for the UTC
// Sun→Sat window selected by model.WeeklyWindow (on a Saturday this is the
// current, not-yet-completed week), plus optional throttle counters.
// On a miss, both fields are nil and the HTTP status is 200 (per BFF contract).
func (s *committeeServicesrvc) GetCurrentWeeklyBrief(ctx context.Context, p *committeeservice.GetCurrentWeeklyBriefPayload) (*committeeservice.GroupWeeklyBriefCurrentResult, error) {
	slog.DebugContext(ctx, "committeeService.get-current-weekly-brief",
		"committee_uid", p.UID,
	)

	// Authorization (committee viewer relation) is enforced at the edge by
	// Heimdall before the request reaches this service; no in-code check here.

	// Verify the committee exists so a typo'd UID returns 404, not 200/null.
	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	if s.weeklyBriefReader == nil {
		return nil, wrapError(ctx, errors.NewServiceUnavailable("weekly brief reader is not configured"))
	}

	brief, throttleBytes, err := s.weeklyBriefReader.GetCurrent(ctx, p.UID, time.Now().UTC())
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	res := &committeeservice.GroupWeeklyBriefCurrentResult{}
	if brief != nil {
		res.Brief = domainGroupWeeklyBriefToGoa(brief)
	}
	if brief != nil && len(throttleBytes) > 0 {
		throttle := &model.GroupWeeklyBriefThrottle{}
		if err := json.Unmarshal(throttleBytes, throttle); err == nil {
			res.Throttle = domainGroupWeeklyBriefThrottleToGoa(throttle)
		} else {
			slog.WarnContext(ctx, "failed to unmarshal weekly-brief throttle entry", "error", err)
		}
	}
	return res, nil
}

// domainGroupWeeklyBriefToGoa converts a domain GroupWeeklyBrief to its Goa
// response type.
func domainGroupWeeklyBriefToGoa(b *model.GroupWeeklyBrief) *committeeservice.GroupWeeklyBriefWithReadonlyAttributes {
	uid := b.UID
	committeeUID := b.CommitteeUID
	// RFC3339Nano so window_end exposes the model's inclusive nanosecond end
	// (…23:59:59.999999999Z); plain RFC3339 would truncate it to seconds and
	// misrepresent the documented window. window_start has no sub-second part,
	// so Nano renders it without a fractional component.
	windowStart := b.WindowStart.UTC().Format(time.RFC3339Nano)
	windowEnd := b.WindowEnd.UTC().Format(time.RFC3339Nano)
	state := string(b.State)
	regenCount := b.RegenerationCount
	privPresent := b.PrivateSourcePresent

	out := &committeeservice.GroupWeeklyBriefWithReadonlyAttributes{
		UID:                  &uid,
		CommitteeUID:         &committeeUID,
		WindowStart:          &windowStart,
		WindowEnd:            &windowEnd,
		State:                &state,
		RegenerationCount:    &regenCount,
		PrivateSourcePresent: &privPresent,
	}
	// Only emit CreatedAt/UpdatedAt when set — Validate() doesn't require them,
	// so formatting a zero time would surface "0001-01-01T00:00:00Z" in the
	// response. Mirrors how LastAttemptAt is handled below.
	if !b.CreatedAt.IsZero() {
		v := b.CreatedAt.UTC().Format(time.RFC3339)
		out.CreatedAt = &v
	}
	if !b.UpdatedAt.IsZero() {
		v := b.UpdatedAt.UTC().Format(time.RFC3339)
		out.UpdatedAt = &v
	}
	if b.BriefText != "" {
		v := b.BriefText
		out.BriefText = &v
	}
	if b.PromptVersion != "" {
		v := b.PromptVersion
		out.PromptVersion = &v
	}
	if b.Model != "" {
		v := b.Model
		out.Model = &v
	}
	for _, sr := range b.SourceRefs {
		kind := sr.Kind
		id := sr.ID
		ref := &committeeservice.GroupWeeklyBriefSourceRef{
			Kind: &kind,
			ID:   &id,
		}
		if sr.Title != "" {
			t := sr.Title
			ref.Title = &t
		}
		if sr.Excerpt != "" {
			e := sr.Excerpt
			ref.Excerpt = &e
		}
		out.SourceRefs = append(out.SourceRefs, ref)
	}
	// Edit-audit fields are only present once a chair has saved an edit; mirror
	// the CreatedAt/UpdatedAt zero-time handling above.
	if !b.LastEditedAt.IsZero() {
		v := b.LastEditedAt.UTC().Format(time.RFC3339)
		out.LastEditedAt = &v
	}
	if b.LastEditedBy != "" {
		v := b.LastEditedBy
		out.LastEditedBy = &v
	}
	// Always surface the revision: clients echo it back as the edit/save
	// optimistic-concurrency token (PUT /current).
	rev := b.Revision
	out.Revision = &rev
	return out
}

// domainGroupWeeklyBriefThrottleToGoa converts a domain throttle to its Goa
// response type: the split counters (generates / regenerations) with their
// limits, plus the window reset timestamp.
func domainGroupWeeklyBriefThrottleToGoa(t *model.GroupWeeklyBriefThrottle) *committeeservice.GroupWeeklyBriefThrottle {
	gUsed := t.GeneratesUsed
	gLimit := model.GroupWeeklyBriefGenerateLimit
	rUsed := t.RegenerationsUsed
	rLimit := model.GroupWeeklyBriefRegenerationLimit
	out := &committeeservice.GroupWeeklyBriefThrottle{
		GeneratesUsed:      &gUsed,
		GeneratesLimit:     &gLimit,
		RegenerationsUsed:  &rUsed,
		RegenerationsLimit: &rLimit,
	}
	if !t.WindowResetsAt.IsZero() {
		v := t.WindowResetsAt.UTC().Format(time.RFC3339)
		out.WindowResetsAt = &v
	}
	return out
}

// GenerateWeeklyBrief is the POST /committees/{uid}/weekly-briefs/generate
// handler. It runs the synchronous claim phase (edited-guard, throttle limits,
// persist the brief in the "generating" state) and publishes a generate-requested
// event; the durable consumer then runs the source gather + LLM + finalize
// asynchronously. The endpoint responds 202 with the brief in "generating" — the
// client polls GET /current to observe the terminal "generated"/"error" state.
func (s *committeeServicesrvc) GenerateWeeklyBrief(ctx context.Context, p *committeeservice.GenerateWeeklyBriefPayload) (*committeeservice.GroupWeeklyBriefGenerateResult, error) {
	slog.DebugContext(ctx, "committeeService.generate-weekly-brief",
		"committee_uid", p.UID,
		"force", p.Force,
	)

	// Authorization (committee writer relation) is enforced at the edge by
	// Heimdall before the request reaches this service; no in-code check here.

	// Verify the committee exists so a typo'd UID returns 404, not 429.
	base, _, err := s.committeeReaderOrchestrator.GetBase(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	if base == nil {
		return nil, wrapError(ctx, errors.NewNotFound("committee not found"))
	}

	if s.weeklyBriefGenerator == nil {
		return nil, wrapError(ctx, errors.NewServiceUnavailable("weekly brief generator is not configured"))
	}
	// The publisher is required: without it we can't enqueue the async fulfill
	// step, which would leave the brief stuck in "generating". Fail fast before
	// claiming rather than returning 202 for work nothing will pick up.
	if s.publisher == nil {
		return nil, wrapError(ctx, errors.NewServiceUnavailable("weekly brief publisher is not configured"))
	}

	// Use a single "now" for both the claim and the event so the async phase
	// computes exactly the same window as the synchronous claim.
	now := time.Now().UTC()

	out, errClaim := s.weeklyBriefGenerator.Claim(ctx, service.GroupWeeklyBriefGenerateInput{
		CommitteeUID:  p.UID,
		CommitteeName: base.Name,
		ProjectName:   base.ProjectName,
		Force:         p.Force,
		Now:           now,
	})
	if errClaim != nil {
		return nil, wrapError(ctx, errClaim)
	}

	// Publish the generate-requested event for the durable consumer to fulfill.
	// If publishing fails the brief is left "generating" with nothing to advance
	// it, so surface 503 and let the caller retry.
	event := service.GenerateWeeklyBriefRequestedEvent{
		CommitteeUID:  p.UID,
		CommitteeName: base.Name,
		ProjectName:   base.ProjectName,
		Force:         p.Force,
		RequestedAt:   now,
	}
	if errPub := s.publisher.Event(ctx, constants.GenerateWeeklyBriefRequestedSubject, event, false); errPub != nil {
		slog.ErrorContext(ctx, "failed to publish weekly-brief generate-requested event",
			"committee_uid", p.UID, "error", errPub)
		return nil, wrapError(ctx, errors.NewServiceUnavailable("failed to enqueue weekly-brief generation", errPub))
	}

	res := &committeeservice.GroupWeeklyBriefGenerateResult{}
	if out.Brief != nil {
		res.Brief = domainGroupWeeklyBriefToGoa(out.Brief)
	}
	if out.Throttle != nil {
		res.Throttle = domainGroupWeeklyBriefThrottleToGoa(out.Throttle)
	}
	return res, nil
}

// UpdateCurrentWeeklyBrief is the PUT /committees/{uid}/weekly-briefs/current
// handler. It saves chair-edited brief text for the current UTC Sun→Sat window,
// transitioning the brief to "edited" and preserving source_refs. Optimistic
// concurrency is enforced on the revision token the caller read from
// GET /current: a stale token yields 409 with the current revision so the
// client can refetch and retry; a missing brief yields 404; empty text 400.
func (s *committeeServicesrvc) UpdateCurrentWeeklyBrief(ctx context.Context, p *committeeservice.UpdateCurrentWeeklyBriefPayload) (*committeeservice.GroupWeeklyBriefWithReadonlyAttributes, error) {
	slog.DebugContext(ctx, "committeeService.update-current-weekly-brief",
		"committee_uid", p.UID,
		"revision", p.Revision,
	)

	// Authorization (committee writer relation) is enforced at the edge by
	// Heimdall before the request reaches this service; no in-code check here.

	// Fail fast on misconfiguration before any storage I/O.
	if s.weeklyBriefWriter == nil {
		return nil, wrapError(ctx, errors.NewServiceUnavailable("weekly brief writer is not configured"))
	}

	// Verify the committee exists so a typo'd UID returns 404 for the committee
	// rather than 404 for a missing brief.
	base, _, err := s.committeeReaderOrchestrator.GetBase(ctx, p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	if base == nil {
		return nil, wrapError(ctx, errors.NewNotFound("committee not found"))
	}

	// PrincipalContextID is the caller's LFX username (Heimdall principal claim),
	// recorded as last_edited_by. Reject a missing principal rather than persist
	// an empty editor — this endpoint's audit trail depends on it, and it mirrors
	// the guard the sibling write handlers use (CreateCommitteeLink, etc.).
	editedBy, _ := ctx.Value(constants.PrincipalContextID).(string)
	if editedBy == "" {
		return nil, wrapError(ctx, errors.NewValidation("unable to determine user identity from token"))
	}

	updated, err := s.weeklyBriefWriter.Update(ctx, service.GroupWeeklyBriefUpdateInput{
		CommitteeUID: p.UID,
		BriefText:    p.BriefText,
		Revision:     p.Revision,
		EditedBy:     editedBy,
		Now:          time.Now().UTC(),
	})
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	return domainGroupWeeklyBriefToGoa(updated), nil
}

// ListCommitteeLinks returns all links for a committee, optionally filtered by folder.
func (s *committeeServicesrvc) ListCommitteeLinks(ctx context.Context, p *committeeservice.ListCommitteeLinksPayload) (res []*committeeservice.CommitteeLinkWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.list-committee-links", "committee_uid", p.UID)

	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	links, err := s.linkReader.ListLinks(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	result := make([]*committeeservice.CommitteeLinkWithReadonlyAttributes, 0, len(links))
	for _, l := range links {
		if p.FolderUID != nil && (l.FolderUID == nil || *l.FolderUID != *p.FolderUID) {
			continue
		}
		result = append(result, domainLinkToGoa(l))
	}
	return result, nil
}

// CreateCommitteeLink creates a new link for a committee.
func (s *committeeServicesrvc) CreateCommitteeLink(ctx context.Context, p *committeeservice.CreateCommitteeLinkPayload) (res *committeeservice.CommitteeLinkWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.create-committee-link", "committee_uid", p.UID)

	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	if principal == "" {
		return nil, errors.NewValidation("unable to determine user identity from token")
	}

	link := &model.CommitteeLink{
		CommitteeUID:      *p.UID,
		Name:              p.Name,
		URL:               p.URL,
		CreatedByUsername: principal,
	}
	if p.Description != nil {
		link.Description = *p.Description
	}
	if p.FolderUID != nil {
		link.FolderUID = p.FolderUID
	}

	created, err := s.linkWriter.CreateLink(ctx, link, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	return domainLinkToGoa(created), nil
}

// GetCommitteeLink returns a single link for a committee with an ETag.
func (s *committeeServicesrvc) GetCommitteeLink(ctx context.Context, p *committeeservice.GetCommitteeLinkPayload) (res *committeeservice.GetCommitteeLinkResult, err error) {
	slog.DebugContext(ctx, "committeeService.get-committee-link", "committee_uid", p.UID, "link_uid", p.LinkUID)

	link, revision, err := s.linkReader.GetLink(ctx, *p.UID, *p.LinkUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	revisionStr := fmt.Sprintf("%d", revision)
	return &committeeservice.GetCommitteeLinkResult{
		CommitteeLink: domainLinkToGoa(link),
		Etag:          &revisionStr,
	}, nil
}

// DeleteCommitteeLink removes a link from a committee.
func (s *committeeServicesrvc) DeleteCommitteeLink(ctx context.Context, p *committeeservice.DeleteCommitteeLinkPayload) (err error) {
	slog.DebugContext(ctx, "committeeService.delete-committee-link", "committee_uid", p.UID, "link_uid", p.LinkUID)

	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag", "error", err, "etag", p.IfMatch, "link_uid", p.LinkUID)
		return wrapError(ctx, err)
	}

	if err := s.linkWriter.DeleteLink(ctx, *p.UID, *p.LinkUID, parsedRevision, p.XSync); err != nil {
		return wrapError(ctx, err)
	}
	return nil
}

// ListCommitteeLinkFolders returns all link folders for a committee.
func (s *committeeServicesrvc) ListCommitteeLinkFolders(ctx context.Context, p *committeeservice.ListCommitteeLinkFoldersPayload) (res []*committeeservice.CommitteeLinkFolderWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.list-committee-link-folders", "committee_uid", p.UID)

	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	folders, err := s.linkReader.ListLinkFolders(ctx, *p.UID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	result := make([]*committeeservice.CommitteeLinkFolderWithReadonlyAttributes, 0, len(folders))
	for _, f := range folders {
		result = append(result, domainFolderToGoa(f))
	}
	return result, nil
}

// GetCommitteeLinkFolder returns a single link folder for a committee with an ETag.
func (s *committeeServicesrvc) GetCommitteeLinkFolder(ctx context.Context, p *committeeservice.GetCommitteeLinkFolderPayload) (res *committeeservice.GetCommitteeLinkFolderResult, err error) {
	slog.DebugContext(ctx, "committeeService.get-committee-link-folder", "committee_uid", p.UID, "folder_uid", p.FolderUID)

	folder, revision, err := s.linkReader.GetLinkFolder(ctx, *p.UID, *p.FolderUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	revisionStr := fmt.Sprintf("%d", revision)
	return &committeeservice.GetCommitteeLinkFolderResult{
		CommitteeLinkFolder: domainFolderToGoa(folder),
		Etag:                &revisionStr,
	}, nil
}

// CreateCommitteeLinkFolder creates a new link folder for a committee.
func (s *committeeServicesrvc) CreateCommitteeLinkFolder(ctx context.Context, p *committeeservice.CreateCommitteeLinkFolderPayload) (res *committeeservice.CommitteeLinkFolderWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.create-committee-link-folder", "committee_uid", p.UID)

	if _, _, err := s.committeeReaderOrchestrator.GetBase(ctx, *p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	if principal == "" {
		return nil, errors.NewValidation("unable to determine user identity from token")
	}

	folder := &model.CommitteeLinkFolder{
		CommitteeUID:      *p.UID,
		Name:              p.Name,
		CreatedByUsername: principal,
	}

	created, err := s.linkWriter.CreateLinkFolder(ctx, folder, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	return domainFolderToGoa(created), nil
}

// DeleteCommitteeLinkFolder removes a link folder from a committee.
func (s *committeeServicesrvc) DeleteCommitteeLinkFolder(ctx context.Context, p *committeeservice.DeleteCommitteeLinkFolderPayload) (err error) {
	slog.DebugContext(ctx, "committeeService.delete-committee-link-folder", "committee_uid", p.UID, "folder_uid", p.FolderUID)

	parsedRevision, err := etagValidator(p.IfMatch)
	if err != nil {
		slog.ErrorContext(ctx, "invalid ETag", "error", err, "etag", p.IfMatch, "folder_uid", p.FolderUID)
		return wrapError(ctx, err)
	}

	if err := s.linkWriter.DeleteLinkFolder(ctx, *p.UID, *p.FolderUID, parsedRevision, p.XSync); err != nil {
		return wrapError(ctx, err)
	}
	return nil
}

// domainLinkToGoa converts a domain CommitteeLink to its Goa result type.
func domainLinkToGoa(l *model.CommitteeLink) *committeeservice.CommitteeLinkWithReadonlyAttributes {
	uid := l.UID
	committeeUID := l.CommitteeUID
	name := l.Name
	url := l.URL
	createdAt := l.CreatedAt.UTC().Format(time.RFC3339)
	updatedAt := l.UpdatedAt.UTC().Format(time.RFC3339)

	res := &committeeservice.CommitteeLinkWithReadonlyAttributes{
		UID:          &uid,
		CommitteeUID: &committeeUID,
		FolderUID:    l.FolderUID,
		Name:         &name,
		URL:          &url,
		CreatedAt:    &createdAt,
		UpdatedAt:    &updatedAt,
	}
	if l.Description != "" {
		desc := l.Description
		res.Description = &desc
	}
	if l.CreatedByUsername != "" {
		v := l.CreatedByUsername
		res.CreatedByUsername = &v
	}
	return res
}

// domainFolderToGoa converts a domain CommitteeLinkFolder to its Goa result type.
func domainFolderToGoa(f *model.CommitteeLinkFolder) *committeeservice.CommitteeLinkFolderWithReadonlyAttributes {
	uid := f.UID
	committeeUID := f.CommitteeUID
	name := f.Name
	createdAt := f.CreatedAt.UTC().Format(time.RFC3339)
	updatedAt := f.UpdatedAt.UTC().Format(time.RFC3339)

	res := &committeeservice.CommitteeLinkFolderWithReadonlyAttributes{
		UID:          &uid,
		CommitteeUID: &committeeUID,
		Name:         &name,
		CreatedAt:    &createdAt,
		UpdatedAt:    &updatedAt,
	}
	if f.CreatedByUsername != "" {
		v := f.CreatedByUsername
		res.CreatedByUsername = &v
	}
	return res
}

// ─── Committee Document Endpoints ───

// UploadCommitteeDocument handles multipart file upload for a committee document.
func (s *committeeServicesrvc) UploadCommitteeDocument(ctx context.Context, p *committeeservice.UploadCommitteeDocumentPayload) (res *committeeservice.CommitteeDocumentWithReadonlyAttributes, err error) {
	slog.DebugContext(ctx, "committeeService.upload-committee-document", "committee_uid", p.UID)

	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	if principal == "" {
		return nil, errors.NewValidation("unable to determine user identity from token")
	}

	// Verify committee exists before attaching a document to it.
	if _, _, err := s.storage.GetBase(ctx, p.UID); err != nil {
		return nil, wrapError(ctx, err)
	}

	if p.FolderUID != nil {
		if _, _, err := s.linkReader.GetLinkFolder(ctx, p.UID, *p.FolderUID); err != nil {
			return nil, wrapError(ctx, errors.NewValidation("folder_uid does not exist or does not belong to this committee"))
		}
	}

	doc := &model.CommitteeDocument{
		CommitteeUID:       p.UID,
		Name:               p.Name,
		UploadedByUsername: principal,
	}
	if p.Description != nil {
		doc.Description = *p.Description
	}
	if p.FolderUID != nil {
		doc.FolderUID = p.FolderUID
	}
	doc.FileName = p.FileName
	doc.ContentType = p.ContentType

	created, err := s.docWriter.UploadDocument(ctx, doc, p.File, p.XSync)
	if err != nil {
		return nil, wrapError(ctx, err)
	}
	return domainDocumentToGoa(created), nil
}

// GetCommitteeDocument returns metadata for a single committee document with an ETag.
func (s *committeeServicesrvc) GetCommitteeDocument(ctx context.Context, p *committeeservice.GetCommitteeDocumentPayload) (res *committeeservice.GetCommitteeDocumentResult, err error) {
	slog.DebugContext(ctx, "committeeService.get-committee-document", "committee_uid", p.UID, "document_uid", p.DocumentUID)

	doc, revision, err := s.docReader.GetDocumentMetadata(ctx, *p.UID, *p.DocumentUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	revisionStr := fmt.Sprintf("%d", revision)
	return &committeeservice.GetCommitteeDocumentResult{
		CommitteeDocument: domainDocumentToGoa(doc),
		Etag:              &revisionStr,
	}, nil
}

// DownloadCommitteeDocument returns the raw file data for a committee document.
// The returned io.ReadCloser also implements io.WriterTo so it can set
// Content-Type and Content-Disposition headers before writing bytes to the
// http.ResponseWriter (Goa calls WriteTo after the empty EncodeResponse no-op).
func (s *committeeServicesrvc) DownloadCommitteeDocument(ctx context.Context, p *committeeservice.DownloadCommitteeDocumentPayload) (body io.ReadCloser, err error) {
	slog.DebugContext(ctx, "committeeService.download-committee-document", "committee_uid", p.UID, "document_uid", p.DocumentUID)

	doc, _, err := s.docReader.GetDocumentMetadata(ctx, *p.UID, *p.DocumentUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	fileData, err := s.docReader.GetDocumentFile(ctx, *p.DocumentUID)
	if err != nil {
		return nil, wrapError(ctx, err)
	}

	return &documentDownloadBody{
		data:        fileData,
		contentType: doc.ContentType,
		fileName:    doc.FileName,
	}, nil
}

// DeleteCommitteeDocument deletes a committee document using optimistic locking via If-Match.
func (s *committeeServicesrvc) DeleteCommitteeDocument(ctx context.Context, p *committeeservice.DeleteCommitteeDocumentPayload) (err error) {
	slog.DebugContext(ctx, "committeeService.delete-committee-document", "committee_uid", p.UID, "document_uid", p.DocumentUID)

	revision, err := etagValidator(&p.IfMatch)
	if err != nil {
		return wrapError(ctx, err)
	}

	if err := s.docWriter.DeleteDocument(ctx, p.UID, p.DocumentUID, revision, p.XSync); err != nil {
		return wrapError(ctx, err)
	}
	return nil
}

// domainDocumentToGoa converts a domain CommitteeDocument to its Goa result type.
func domainDocumentToGoa(d *model.CommitteeDocument) *committeeservice.CommitteeDocumentWithReadonlyAttributes {
	uid := d.UID
	committeeUID := d.CommitteeUID
	name := d.Name
	fileName := d.FileName
	fileSize := d.FileSize
	contentType := d.ContentType
	createdAt := d.CreatedAt.UTC().Format(time.RFC3339)
	updatedAt := d.UpdatedAt.UTC().Format(time.RFC3339)

	res := &committeeservice.CommitteeDocumentWithReadonlyAttributes{
		UID:          &uid,
		CommitteeUID: &committeeUID,
		Name:         &name,
		FileName:     &fileName,
		FileSize:     &fileSize,
		ContentType:  &contentType,
		CreatedAt:    &createdAt,
		UpdatedAt:    &updatedAt,
	}
	if d.Description != "" {
		v := d.Description
		res.Description = &v
	}
	if d.UploadedByUsername != "" {
		v := d.UploadedByUsername
		res.UploadedByUsername = &v
	}
	if d.FolderUID != nil {
		res.FolderUID = d.FolderUID
	}
	return res
}

// documentDownloadBody is an io.ReadCloser that also implements io.WriterTo.
// When Goa calls WriteTo(w) with the http.ResponseWriter, it sets the
// Content-Type and Content-Disposition headers before writing file bytes,
// ensuring headers are sent before the body without touching generated code.
type documentDownloadBody struct {
	data        []byte
	contentType string
	fileName    string
	offset      int
}

func (b *documentDownloadBody) Read(p []byte) (n int, err error) {
	if b.offset >= len(b.data) {
		return 0, io.EOF
	}
	n = copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}

func (b *documentDownloadBody) Close() error { return nil }

func (b *documentDownloadBody) WriteTo(w io.Writer) (int64, error) {
	if hw, ok := w.(http.ResponseWriter); ok {
		if b.contentType != "" {
			hw.Header().Set("Content-Type", b.contentType)
		}
		if b.fileName != "" {
			// Sanitize filename for RFC 6266 compliance: replace characters
			// that could cause header injection or malformed headers.
			safeName := strings.Map(func(r rune) rune {
				if r == '"' || r == '\\' || r == '\n' || r == '\r' {
					return '_'
				}
				return r
			}, b.fileName)
			hw.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeName))
		}
	}
	n, err := w.Write(b.data)
	return int64(n), err
}
