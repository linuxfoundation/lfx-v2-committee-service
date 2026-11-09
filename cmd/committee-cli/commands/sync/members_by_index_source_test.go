// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
)

func indexSourceMember(uid string) *model.CommitteeMember {
	return &model.CommitteeMember{CommitteeMemberBase: model.CommitteeMemberBase{
		UID: uid, CommitteeUID: "c1", Email: uid + "@example.com", Username: uid,
		Organization: model.CommitteeMemberOrganization{ID: "001000000000001AAA"},
	}}
}

// The backfillers write KV index entries, so they enumerate the members KV holds, not the ones the store of the storage mode
// does: in dual-pg and postgres mode that is PostgreSQL, which can hold members KV lacks and lack members KV holds.
func TestMemberIndexBackfillers_ReadTheKVMembersWhateverTheStorageMode(t *testing.T) {
	subcommands := map[string]struct {
		run     func(context.Context, commands.RunContext) error
		indexed func(*mockMemberWriter) []string
	}{
		"committee":    {(&membersByCommitteeIndexSubcommand{}).Run, func(w *mockMemberWriter) []string { return w.indexed }},
		"organization": {(&membersByOrganizationIndexSubcommand{}).Run, func(w *mockMemberWriter) []string { return w.orgIndexed }},
		"email":        {(&membersByEmailIndexSubcommand{}).Run, func(w *mockMemberWriter) []string { return w.emailIndexed }},
		"username":     {(&membersByUsernameIndexSubcommand{}).Run, func(w *mockMemberWriter) []string { return w.usernameIndexed }},
	}
	for name, sc := range subcommands {
		t.Run(name, func(t *testing.T) {
			stores := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {indexSourceMember("pg-only")}}}
			kv := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {indexSourceMember("kv-only")}}}
			w := &mockMemberWriter{}
			rc := newBackfillRC(stores, w, "--dry-run=false")
			rc.KVCommitteeReader = kv

			require.NoError(t, sc.run(context.Background(), rc))

			keys := sc.indexed(w)
			require.Len(t, keys, 1)
			assert.Contains(t, keys[0], "kv-only", "the member KV holds is indexed")
			assert.NotContains(t, keys[0], "pg-only", "a member only PostgreSQL holds is not")
		})
	}
}

func TestMemberIndexBackfillers_FallBackToTheCommitteeReaderWhereKVIsNotWired(t *testing.T) {
	r := &mockReader{members: map[string][]*model.CommitteeMember{"c1": {indexSourceMember("m1")}}}
	w := &mockMemberWriter{}

	require.NoError(t, (&membersByEmailIndexSubcommand{}).Run(context.Background(), newBackfillRC(r, w, "--dry-run=false")))

	assert.Len(t, w.emailIndexed, 1)
}
