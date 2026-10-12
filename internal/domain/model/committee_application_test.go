// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model_test

import (
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func TestCommitteeApplication_Tags(t *testing.T) {
	tests := []struct {
		name       string
		app        *model.CommitteeApplication
		wantTags   []string
		wantAbsent []string
	}{
		{
			name: "emits username tag when ApplicantUsername is set",
			app: &model.CommitteeApplication{
				UID:               "app-uid-1",
				CommitteeUID:      "committee-uid-1",
				ApplicantEmail:    "first.last@example.com",
				ApplicantUsername: "first-last",
				Status:            "pending",
				CreatedAt:         time.Now(),
			},
			wantTags: []string{
				"app-uid-1",
				"committee_application_uid:app-uid-1",
				"committee_uid:committee-uid-1",
				"applicant_email:first.last@example.com",
				"username:first-last",
				"status:pending",
			},
		},
		{
			name: "omits username tag when ApplicantUsername is empty",
			app: &model.CommitteeApplication{
				UID:            "app-uid-2",
				CommitteeUID:   "committee-uid-1",
				ApplicantEmail: "first.last@example.com",
				Status:         "pending",
				CreatedAt:      time.Now(),
			},
			wantAbsent: []string{"username:"},
			wantTags: []string{
				"applicant_email:first.last@example.com",
				"status:pending",
			},
		},
		{
			name: "email is lowercased in tag",
			app: &model.CommitteeApplication{
				UID:            "app-uid-3",
				CommitteeUID:   "committee-uid-1",
				ApplicantEmail: "First.Last@Example.com",
				Status:         "pending",
				CreatedAt:      time.Now(),
			},
			wantTags: []string{
				"applicant_email:first.last@example.com",
			},
		},
		{
			name: "nil receiver returns nil",
			app:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := tt.app.Tags()

			for _, want := range tt.wantTags {
				assert.Contains(t, tags, want, "expected tag %q to be present", want)
			}
			for _, absent := range tt.wantAbsent {
				for _, tag := range tags {
					assert.NotContains(t, tag, absent, "expected no tag containing %q", absent)
				}
			}
		})
	}
}
