// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"fmt"
	"slices"
	"strings"

	indexertypes "github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// IndexingConfig builds the current committee search projection metadata.
func (base *CommitteeBase) IndexingConfig() *indexertypes.IndexingConfig {
	var names, refs, fulltext []string
	for _, value := range []string{base.Name, base.DisplayName} {
		if value != "" && !slices.Contains(names, value) {
			names = append(names, value)
		}
	}
	fulltext = append(fulltext, names...)
	if base.Description != "" && !slices.Contains(fulltext, base.Description) {
		fulltext = append(fulltext, base.Description)
	}
	if base.ProjectUID != "" {
		refs = append(refs, "project:"+base.ProjectUID)
	}
	if base.ParentUID != nil && *base.ParentUID != "" {
		refs = append(refs, "committee:"+*base.ParentUID)
	}
	public := base.Public
	return &indexertypes.IndexingConfig{
		ObjectID: base.UID, AccessCheckObject: "committee:" + base.UID, AccessCheckRelation: "viewer",
		HistoryCheckObject: "committee:" + base.UID, HistoryCheckRelation: "auditor",
		SortName: base.Name, NameAndAliases: names, ParentRefs: refs,
		Tags: (&Committee{CommitteeBase: *base}).Tags(), Fulltext: strings.Join(fulltext, " "), Public: &public,
	}
}

// IndexingConfig builds the current member projection using its parent committee
// for ordinary and historical authorization. Members are never anonymously public.
func (member *CommitteeMember) IndexingConfig() *indexertypes.IndexingConfig {
	var names []string
	for _, value := range []string{member.CommitteeName, member.FirstName, member.LastName, member.Username} {
		if value != "" {
			names = append(names, value)
		}
	}
	if member.FirstName != "" && member.LastName != "" {
		names = append(names, member.FirstName+" "+member.LastName)
	}
	return &indexertypes.IndexingConfig{
		ObjectID: member.UID, AccessCheckObject: "committee:" + member.CommitteeUID, AccessCheckRelation: "viewer",
		HistoryCheckObject: "committee:" + member.CommitteeUID, HistoryCheckRelation: "auditor",
		SortName: member.FirstName, NameAndAliases: names, ParentRefs: []string{"committee:" + member.CommitteeUID},
		Tags: member.Tags(), Fulltext: fmt.Sprintf("%s %s %s %s", member.FirstName, member.LastName, member.Email, member.Organization.Name),
	}
}
