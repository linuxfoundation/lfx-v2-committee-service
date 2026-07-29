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
	authpkg "github.com/linuxfoundation/lfx-v2-committee-service/pkg/auth"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
)

const documentAuditUserResolveTimeout = 2 * time.Second

// resolveRequestingUser resolves the JWT principal into a CommitteeUser for document audit fields.
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

	lookupCtx, cancel := context.WithTimeout(ctx, documentAuditUserResolveTimeout)
	defer cancel()

	meta, err := s.userReader.UserMetadataByPrincipal(lookupCtx, principal)
	if err != nil {
		slog.WarnContext(ctx, "failed to resolve user profile for audit stamp; stamping username/email only",
			"username", principal, "error", err)
		return &model.CommitteeUser{Username: principal, Email: email}
	}

	user := &model.CommitteeUser{Username: principal}
	if meta != nil {
		user.Name = meta.Name
		if user.Name == "" {
			user.Name = strings.TrimSpace(meta.GivenName + " " + meta.FamilyName)
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
	authSub := authpkg.MapUsernameToAuthSub(username)
	if authSub == "" {
		return ""
	}
	userEmails, err := s.userReader.EmailsByAuthToken(ctx, authSub)
	if err != nil || userEmails == nil {
		return ""
	}
	return userEmails.PrimaryEmail
}

func (s *committeeServicesrvc) enrichAuditUserIfMissing(ctx context.Context, user *model.CommitteeUser) *model.CommitteeUser {
	if user == nil || strings.TrimSpace(user.Username) == "" || strings.TrimSpace(user.Name) != "" || s.userReader == nil {
		return user
	}
	lookupCtx, cancel := context.WithTimeout(ctx, documentAuditUserResolveTimeout)
	defer cancel()
	meta, err := s.userReader.UserMetadataByPrincipal(lookupCtx, user.Username)
	if err != nil || meta == nil {
		return user
	}
	enriched := model.CloneCommitteeUser(user)
	if meta.Name != "" {
		enriched.Name = meta.Name
	} else if full := strings.TrimSpace(meta.GivenName + " " + meta.FamilyName); full != "" {
		enriched.Name = full
	}
	if enriched.Avatar == "" {
		enriched.Avatar = meta.Picture
	}
	if enriched.Email == "" {
		enriched.Email = s.primaryEmailForUsername(lookupCtx, user.Username)
	}
	return enriched
}

func (s *committeeServicesrvc) normalizeDocumentAuditUsers(ctx context.Context, createdBy, updatedBy **model.CommitteeUser, legacyCreatedByUsername, legacyUploadedByUsername string) {
	model.NormalizeLegacyAuditUsers(createdBy, updatedBy, legacyCreatedByUsername, legacyUploadedByUsername)
	if createdBy != nil && *createdBy != nil {
		*createdBy = s.enrichAuditUserIfMissing(ctx, *createdBy)
	}
	if updatedBy != nil && createdBy != nil && *createdBy != nil {
		if *updatedBy == nil || (*updatedBy).Username == (*createdBy).Username {
			*updatedBy = model.CloneCommitteeUser(*createdBy)
		} else {
			*updatedBy = s.enrichAuditUserIfMissing(ctx, *updatedBy)
		}
	}
}

func (s *committeeServicesrvc) stampDocumentAuditUsers(ctx context.Context) (*model.CommitteeUser, *model.CommitteeUser) {
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
