// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// maxBulkMemberWorkers bounds how many rows of one bulk import run at once. It is a constant and
// never derived from the request, so one import cannot fan out against auth-service, member-service
// or storage.
const maxBulkMemberWorkers = 4

// orgResolution is a successful b2b_org lookup result.
type orgResolution struct {
	sfid  string
	found bool
}

// orgResolutionMemo caches successful b2b_org resolutions across the rows of one bulk import.
// Resolver errors are never cached, so every row keeps the fail-open behavior of a single create.
// A nil memo resolves directly.
type orgResolutionMemo struct {
	mu            sync.Mutex
	byUID         map[string]orgResolution
	byNameWebsite map[[2]string]orgResolution
}

func newOrgResolutionMemo() *orgResolutionMemo {
	return &orgResolutionMemo{
		byUID:         make(map[string]orgResolution),
		byNameWebsite: make(map[[2]string]orgResolution),
	}
}

// resolveByUID resolves uid through resolver, reusing an earlier successful result for the same uid.
func (m *orgResolutionMemo) resolveByUID(ctx context.Context, resolver port.B2BOrgResolver, uid string) (string, bool, error) {
	if m == nil {
		return resolver.ResolveByUID(ctx, uid)
	}
	m.mu.Lock()
	cached, ok := m.byUID[uid]
	m.mu.Unlock()
	if ok {
		return cached.sfid, cached.found, nil
	}

	sfid, found, err := resolver.ResolveByUID(ctx, uid)
	if err != nil {
		return "", false, err
	}
	m.mu.Lock()
	m.byUID[uid] = orgResolution{sfid: sfid, found: found}
	m.mu.Unlock()
	return sfid, found, nil
}

// resolveSFID resolves an organization by name and website through resolver, reusing an earlier
// successful result for the same pair.
func (m *orgResolutionMemo) resolveSFID(ctx context.Context, resolver port.B2BOrgFallbackResolver, name, website string) (string, bool, error) {
	if m == nil {
		return resolver.ResolveSFID(ctx, name, website)
	}
	key := [2]string{name, website}
	m.mu.Lock()
	cached, ok := m.byNameWebsite[key]
	m.mu.Unlock()
	if ok {
		return cached.sfid, cached.found, nil
	}

	sfid, found, err := resolver.ResolveSFID(ctx, name, website)
	if err != nil {
		return "", false, err
	}
	m.mu.Lock()
	m.byNameWebsite[key] = orgResolution{sfid: sfid, found: found}
	m.mu.Unlock()
	return sfid, found, nil
}

// classifyBulkMemberError keeps unproven conflicts retryable: a reservation alone
// is not evidence that a member exists. Verified existing outcomes are handled
// separately using the member returned by recoverable persistence.
func classifyBulkMemberError(err error) (model.BulkMemberStatus, model.BulkMemberReason, bool) {
	var (
		validationErr errs.Validation
		notFoundErr   errs.NotFound
		forbiddenErr  errs.Forbidden
	)
	switch {
	case errors.As(err, &validationErr), errors.As(err, &notFoundErr), errors.As(err, &forbiddenErr):
		return model.BulkMemberFailed, model.BulkReasonInvalid, false
	default:
		return model.BulkMemberFailed, model.BulkReasonTemporary, true
	}
}

// bulkMemberErrorMessage returns a client-safe message for a row error. Domain errors carry messages
// written for clients; anything else (context errors, unexpected failures) gets a generic message.
func bulkMemberErrorMessage(err error) string {
	var (
		conflictErr    errs.Conflict
		validationErr  errs.Validation
		notFoundErr    errs.NotFound
		forbiddenErr   errs.Forbidden
		unavailableErr errs.ServiceUnavailable
	)
	switch {
	case errors.As(err, &conflictErr):
		return conflictErr.ClientMessage()
	case errors.As(err, &validationErr):
		return validationErr.ClientMessage()
	case errors.As(err, &notFoundErr):
		return notFoundErr.ClientMessage()
	case errors.As(err, &forbiddenErr):
		return forbiddenErr.ClientMessage()
	case errors.As(err, &unavailableErr):
		return unavailableErr.ClientMessage()
	default:
		return "temporary failure; retry later"
	}
}

// normalizeBulkMemberEmail matches the normalization of the committee+email uniqueness key.
func normalizeBulkMemberEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CreateMembers adds members to one committee as a coordinated import. The committee, its settings
// and its existing members are read once; rows repeating an earlier row or an existing member are
// skipped; the remaining rows run through the single-member create pipeline with at most
// maxBulkMemberWorkers at a time. The first temporary failure stops new rows from starting.
func (uc *committeeWriterOrchestrator) CreateMembers(ctx context.Context, committeeUID string, members []*model.CommitteeMember, opts BulkMemberCreateOptions) ([]*model.BulkMemberResult, error) {
	if len(members) == 0 || len(members) > model.MaxBulkMembers {
		return nil, errs.NewValidation(fmt.Sprintf("members must contain between 1 and %d rows", model.MaxBulkMembers))
	}

	target, errTarget := uc.loadMemberCreateTarget(ctx, committeeUID)
	if errTarget != nil {
		return nil, errTarget
	}
	target.orgs = newOrgResolutionMemo()
	target.requireResolvedIdentity = true

	_, recoveryCapable := uc.memberCreateRecovery()
	existing := map[string]string{}
	if !recoveryCapable {
		existing = uc.existingMemberUIDsByEmail(ctx, committeeUID)
	}

	results := make([]*model.BulkMemberResult, len(members))
	firstRowByEmail := make(map[string]int, len(members))
	pending := make([]int, 0, len(members))
	for i, member := range members {
		result := &model.BulkMemberResult{Index: i}
		results[i] = result
		if member == nil {
			result.Status, result.Reason = model.BulkMemberFailed, model.BulkReasonInvalid
			result.Message = "member row is empty"
			continue
		}
		member.CommitteeUID = committeeUID
		result.Email = member.Email

		email := normalizeBulkMemberEmail(member.Email)
		if email != "" {
			if first, seen := firstRowByEmail[email]; seen {
				result.Status, result.Reason = model.BulkMemberSkipped, model.BulkReasonDuplicateInRequest
				result.Message = fmt.Sprintf("email repeats row %d", first)
				continue
			}
			firstRowByEmail[email] = i
			if memberUID, ok := existing[email]; ok && !recoveryCapable {
				result.Status, result.Reason = model.BulkMemberSkipped, model.BulkReasonAlreadyMember
				result.Message = "member already exists in the committee"
				result.MemberUID = memberUID
				continue
			}
		}
		pending = append(pending, i)
	}

	// Rows never return errors to the group, so a plain group (not WithContext) lets in-flight rows
	// finish after a temporary failure while the halt flag keeps new rows from starting.
	var halted atomic.Bool
	g := new(errgroup.Group)
	g.SetLimit(maxBulkMemberWorkers)
	for _, i := range pending {
		member, result := members[i], results[i]
		g.Go(func() error {
			uc.createBulkMemberRow(ctx, member, target, opts, result, &halted)
			return nil
		})
	}
	_ = g.Wait()

	var added, skipped, failed int
	for _, result := range results {
		switch result.Status {
		case model.BulkMemberAdded:
			added++
		case model.BulkMemberSkipped:
			skipped++
		default:
			failed++
		}
	}
	slog.InfoContext(ctx, "bulk committee member create finished",
		"committee_uid", committeeUID,
		"rows", len(members),
		"added", added,
		"skipped", skipped,
		"failed", failed,
		"halted", halted.Load(),
	)

	return results, nil
}

// existingMemberUIDsByEmail maps the committee's current members by normalized email. A listing
// failure is not fatal: the storage uniqueness key still turns an existing email into a conflict.
func (uc *committeeWriterOrchestrator) existingMemberUIDsByEmail(ctx context.Context, committeeUID string) map[string]string {
	existing, err := uc.committeeReader.ListMembersByCommittee(ctx, committeeUID)
	if err != nil {
		slog.WarnContext(ctx, "failed to list existing committee members; relying on storage uniqueness",
			"error", err,
			"committee_uid", committeeUID,
		)
		return map[string]string{}
	}
	byEmail := make(map[string]string, len(existing))
	for _, member := range existing {
		if member == nil {
			continue
		}
		if email := normalizeBulkMemberEmail(member.Email); email != "" {
			byEmail[email] = member.UID
		}
	}
	return byEmail
}

// createBulkMemberRow creates one bulk row and records its outcome in result. A temporary failure
// sets halted so rows that have not started yet are reported as not attempted.
func (uc *committeeWriterOrchestrator) createBulkMemberRow(ctx context.Context, member *model.CommitteeMember, target *memberCreateTarget, opts BulkMemberCreateOptions, result *model.BulkMemberResult, halted *atomic.Bool) {
	if halted.Load() || ctx.Err() != nil {
		result.Status, result.Reason, result.Retryable = model.BulkMemberFailed, model.BulkReasonNotAttempted, true
		result.Message = "not attempted because the import stopped early; retry later"
		return
	}

	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "panic while creating bulk committee member",
				"panic", r,
				"stack", string(debug.Stack()),
				"committee_uid", target.committee.UID,
			)
			result.Status, result.Reason, result.Retryable = model.BulkMemberFailed, model.BulkReasonTemporary, true
			result.Message = "temporary failure; retry later"
			result.Member, result.MemberUID = nil, ""
			halted.Store(true)
		}
	}()

	// Confirm and fence existing memberships before enrichment or replacement-row
	// validation. Storage, not a possibly stale pre-list, establishes completion.
	if recovery, ok := uc.memberCreateRecovery(); ok {
		selected, err := recovery.FindMemberCreate(ctx, member.CommitteeUID, member.Email)
		if err != nil {
			result.Status, result.Reason, result.Retryable = classifyBulkMemberError(err)
			result.Message = bulkMemberErrorMessage(err)
			halted.Store(true)
			return
		}
		if selected != nil && !selected.Pending {
			result.Status, result.Reason = model.BulkMemberSkipped, model.BulkReasonAlreadyMember
			result.MemberUID = selected.Member.UID
			result.Message = "member already exists in the committee"
			return
		}
	}

	if opts.Enrich != nil {
		opts.Enrich(ctx, member)
	}

	created, err := uc.createMemberIn(ctx, member, target, opts.Sync, opts.SkipEnrichment)
	if err != nil {
		var conflict errs.Conflict
		if errors.As(err, &conflict) && created == nil {
			// A transactional concurrent create may win after the pre-list.
			// Only an authoritative read proves that this is already a membership.
			if uid := uc.existingMemberUIDsByEmail(ctx, member.CommitteeUID)[normalizeBulkMemberEmail(member.Email)]; uid != "" {
				created = &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{UID: uid}}
			}
		}
		if errors.As(err, &conflict) && created != nil {
			result.Status, result.Reason = model.BulkMemberSkipped, model.BulkReasonAlreadyMember
			result.Message = "member already exists in the committee"
			result.MemberUID = created.UID
			return
		}
		result.Status, result.Reason, result.Retryable = classifyBulkMemberError(err)
		result.Message = bulkMemberErrorMessage(err)
		if result.Reason == model.BulkReasonTemporary {
			halted.Store(true)
		}
		return
	}

	result.Status = model.BulkMemberAdded
	result.MemberUID = created.UID
	result.Member = created
}
