// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"

	"github.com/gosimple/slug"
)

// Committee represents the core committee business entity
type Committee struct {
	UID              string    `json:"uid"`
	ProjectUID       string    `json:"project_uid"`
	Name             string    `json:"name"`
	Category         string    `json:"category"`
	Description      string    `json:"description,omitempty"`
	Website          *string   `json:"website,omitempty"`
	EnableVoting     bool      `json:"enable_voting"`
	SSOGroupEnabled  bool      `json:"sso_group_enabled"`
	SSOGroupName     string    `json:"sso_group_name,omitempty"`
	RequiresReview   bool      `json:"requires_review"`
	Public           bool      `json:"public"`
	Calendar         Calendar  `json:"calendar,omitempty"`
	DisplayName      string    `json:"display_name,omitempty"`
	ParentUID        *string   `json:"parent_uid,omitempty"`
	TotalMembers     int       `json:"total_members"`
	TotalVotingRepos int       `json:"total_voting_repos"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	CommitteeSettings
}

// Calendar represents committee calendar settings
type Calendar struct {
	Public bool `json:"public"`
}

func (c *Committee) SSOGroupNameBuild(ctx context.Context, projectSlug string) error {

	ind := 1
	// not the first attempt to create the SSO group
	if c.SSOGroupName != "" {
		currentGroupName := strings.Split(c.SSOGroupName, "-")

		i, errInd := strconv.Atoi(currentGroupName[len(currentGroupName)-1])
		if errInd != nil {
			slog.ErrorContext(ctx, "invalid SSO group name index",
				"current_group_name", currentGroupName,
				"index", ind,
			)
			return errors.NewValidation("invalid SSO group name index")
		}
		ind = i + 1
	}

	c.SSOGroupName = slug.Make(fmt.Sprintf("%s-%s-%d", projectSlug, c.Name, ind))

	return nil

}
