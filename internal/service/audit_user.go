// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

const auditUserResolveTimeout = 2 * time.Second

// ResolveAuditUserProfile best-effort resolves a username into a full CommitteeUser via auth-service.
func ResolveAuditUserProfile(ctx context.Context, reader port.UserReader, username string) *model.CommitteeUser {
	username = strings.TrimSpace(username)
	if username == "" || reader == nil {
		return nil
	}
	return enrichAuditUserIfMissing(ctx, reader, &model.CommitteeUser{Username: username})
}

func enrichAuditUserIfMissing(ctx context.Context, reader port.UserReader, user *model.CommitteeUser) *model.CommitteeUser {
	if user == nil || strings.TrimSpace(user.Username) == "" || reader == nil {
		return user
	}
	if auditUserProfileComplete(user) {
		return user
	}
	lookupCtx, cancel := context.WithTimeout(ctx, auditUserResolveTimeout)
	defer cancel()
	meta, err := reader.UserMetadataByPrincipal(lookupCtx, user.Username)
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
		enriched.Email = primaryEmailForUsername(lookupCtx, reader, user.Username)
	}
	return enriched
}

func auditUserProfileComplete(u *model.CommitteeUser) bool {
	return strings.TrimSpace(u.Name) != "" &&
		strings.TrimSpace(u.Avatar) != "" &&
		strings.TrimSpace(u.Email) != ""
}

func primaryEmailForUsername(ctx context.Context, reader port.UserReader, username string) string {
	if reader == nil {
		return ""
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	userEmails, err := reader.EmailsByAuthToken(ctx, username)
	if err != nil || userEmails == nil {
		return ""
	}
	return userEmails.PrimaryEmail
}
