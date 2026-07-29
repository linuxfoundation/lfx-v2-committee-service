// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/log"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/redaction"
)

const auditUserResolveTimeout = 2 * time.Second

// resolveRequestingUser resolves the JWT principal into a CommitteeUser for stamping created_by/updated_by.
func (s *committeeServicesrvc) resolveRequestingUser(ctx context.Context) *model.CommitteeUser {
	principal, _ := ctx.Value(constants.PrincipalContextID).(string)
	principal = strings.TrimSpace(principal)
	if principal == "" {
		return nil
	}
	email, _ := ctx.Value(constants.EmailContextID).(string)
	email = strings.TrimSpace(email)

	if s.userReader == nil {
		return &model.CommitteeUser{Username: principal, Email: email}
	}

	lookupCtx, cancel := context.WithTimeout(ctx, auditUserResolveTimeout)
	defer cancel()

	meta, err := s.userReader.UserMetadataByPrincipal(lookupCtx, principal)
	if err != nil {
		ctx = log.AppendCtx(ctx, slog.String("username", redaction.Redact(principal)))
		slog.WarnContext(ctx, "failed to resolve user profile for audit stamp; stamping username/email only", "error", err)
		return &model.CommitteeUser{Username: principal, Email: email}
	}

	user := &model.CommitteeUser{Username: principal}
	if meta != nil {
		if name := strings.TrimSpace(meta.Name); name != "" {
			user.Name = name
		} else if full := strings.TrimSpace(meta.GivenName + " " + meta.FamilyName); full != "" {
			user.Name = full
		}
		user.Avatar = meta.Picture
	}

	if resolvedEmail := s.primaryEmailForUsername(lookupCtx, principal); resolvedEmail != "" {
		user.Email = resolvedEmail
	}
	if user.Email == "" {
		user.Email = email
	}

	return user
}

func (s *committeeServicesrvc) primaryEmailForUsername(ctx context.Context, username string) string {
	if s.userReader == nil {
		return ""
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	userEmails, err := s.userReader.EmailsByAuthToken(ctx, username)
	if err != nil || userEmails == nil {
		return ""
	}
	return userEmails.PrimaryEmail
}

func (s *committeeServicesrvc) enrichAuditUserIfMissing(ctx context.Context, user *model.CommitteeUser) *model.CommitteeUser {
	if user == nil || strings.TrimSpace(user.Username) == "" || s.userReader == nil {
		return user
	}
	if auditUserProfileComplete(user) {
		return user
	}
	lookupCtx, cancel := context.WithTimeout(ctx, auditUserResolveTimeout)
	defer cancel()
	meta, err := s.userReader.UserMetadataByPrincipal(lookupCtx, user.Username)
	if err != nil || meta == nil {
		return user
	}
	enriched := model.CloneCommitteeUser(user)
	if strings.TrimSpace(enriched.Name) == "" {
		if name := strings.TrimSpace(meta.Name); name != "" {
			enriched.Name = name
		} else if full := strings.TrimSpace(meta.GivenName + " " + meta.FamilyName); full != "" {
			enriched.Name = full
		}
	}
	if enriched.Avatar == "" {
		enriched.Avatar = meta.Picture
	}
	if enriched.Email == "" {
		enriched.Email = s.primaryEmailForUsername(lookupCtx, user.Username)
	}
	return enriched
}

func auditUserProfileComplete(u *model.CommitteeUser) bool {
	return strings.TrimSpace(u.Name) != "" &&
		strings.TrimSpace(u.Avatar) != "" &&
		strings.TrimSpace(u.Email) != ""
}

func (s *committeeServicesrvc) normalizeLegacyAuditUsers(createdBy, updatedBy *model.CommitteeUser) (*model.CommitteeUser, *model.CommitteeUser) {
	return model.NormalizeLegacyAuditUsers(createdBy, updatedBy, "", "")
}

func (s *committeeServicesrvc) normalizeAuditUsers(ctx context.Context, createdBy, updatedBy *model.CommitteeUser, legacyCreatedByUsername, legacyUploadedByUsername string) (*model.CommitteeUser, *model.CommitteeUser) {
	createdBy, updatedBy = model.NormalizeLegacyAuditUsers(createdBy, updatedBy, legacyCreatedByUsername, legacyUploadedByUsername)
	if createdBy != nil {
		createdBy = s.enrichAuditUserIfMissing(ctx, createdBy)
	}
	if updatedBy == nil && createdBy != nil {
		updatedBy = model.CloneCommitteeUser(createdBy)
	} else if updatedBy != nil {
		updatedBy = s.enrichAuditUserIfMissing(ctx, updatedBy)
	}
	return createdBy, updatedBy
}

func (s *committeeServicesrvc) stampAuditUsers(ctx context.Context) (*model.CommitteeUser, *model.CommitteeUser) {
	creator := s.resolveRequestingUser(ctx)
	if creator == nil {
		return nil, nil
	}
	updated := model.CloneCommitteeUser(creator)
	return creator, updated
}

// ResolveAuditUserProfile best-effort resolves a username into a full CommitteeUser via auth-service.
func ResolveAuditUserProfile(ctx context.Context, reader port.UserReader, username string) *model.CommitteeUser {
	username = strings.TrimSpace(username)
	if username == "" || reader == nil {
		return nil
	}
	svc := &committeeServicesrvc{userReader: reader}
	return svc.enrichAuditUserIfMissing(ctx, &model.CommitteeUser{Username: username})
}

func committeeUserToGoa(u *model.CommitteeUser) *committeeservice.CommitteeUser {
	if u == nil {
		return nil
	}
	cu := &committeeservice.CommitteeUser{}
	if u.Avatar != "" {
		cu.Avatar = &u.Avatar
	}
	if u.Email != "" {
		cu.Email = &u.Email
	}
	if u.Name != "" {
		cu.Name = &u.Name
	}
	if u.Username != "" {
		cu.Username = &u.Username
	}
	return cu
}
