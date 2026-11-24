// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// fakeSettingsIndex serves current committee_settings documents with search_after paging. It
// evaluates the --verify script filter with repairedForTest, returns exact totals (duplicate
// copies included), and can report an incomplete response.
type fakeSettingsIndex struct {
	mu                   sync.Mutex
	docs                 []settingsIndexDoc
	requests             []map[string]any
	partialAllowedParams []string
	timedOut             bool
	terminatedEarly      bool
	failedShards         int
}

// repairedForTest mirrors settingsNotRepairedScript for the fake index.
func repairedForTest(d settingsIndexDoc) bool {
	want := "committee:" + d.ObjectID
	return !d.Public && d.AccessCheckObject == want && d.AccessCheckRelation == "auditor" &&
		d.HistoryCheckObject == want && d.HistoryCheckRelation == "auditor"
}

func (f *fakeSettingsIndex) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		f.mu.Lock()
		f.requests = append(f.requests, req)
		f.partialAllowedParams = append(f.partialAllowedParams, r.URL.Query().Get("allow_partial_search_results"))
		f.mu.Unlock()

		body, err := json.Marshal(req["query"])
		require.NoError(t, err)
		var q struct {
			Bool struct {
				Filter []map[string]map[string]any `json:"filter"`
			} `json:"bool"`
		}
		require.NoError(t, json.Unmarshal(body, &q))
		filter, notRepairedOnly := "", false
		for _, clause := range q.Bool.Filter {
			if id, ok := clause["term"]["object_id"].(string); ok {
				filter = id
			}
			if _, ok := clause["script"]; ok {
				notRepairedOnly = true
			}
		}
		after := ""
		if sa, ok := req["search_after"].([]any); ok && len(sa) == 1 {
			after, _ = sa[0].(string)
		}
		size := int(req["size"].(float64))

		docs := append([]settingsIndexDoc(nil), f.docs...)
		sort.SliceStable(docs, func(i, j int) bool { return docs[i].ObjectID < docs[j].ObjectID })
		var matching []settingsIndexDoc
		for _, doc := range docs {
			if (filter != "" && doc.ObjectID != filter) || (notRepairedOnly && repairedForTest(doc)) {
				continue
			}
			matching = append(matching, doc)
		}
		var hits []map[string]any
		for _, doc := range matching {
			if doc.ObjectID <= after {
				continue
			}
			if len(hits) == size {
				break
			}
			hits = append(hits, map[string]any{"_source": doc})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"timed_out":        f.timedOut,
			"terminated_early": f.terminatedEarly,
			"_shards":          map[string]any{"failed": f.failedShards},
			"hits":             map[string]any{"total": map[string]any{"value": len(matching)}, "hits": hits},
		})
	}
}

func oldSettingsDoc(uid string, public bool) settingsIndexDoc {
	return settingsIndexDoc{
		ObjectID: uid, Public: public,
		AccessCheckObject: "committee_settings:" + uid, AccessCheckRelation: "auditor",
		HistoryCheckObject: "committee_settings:" + uid, HistoryCheckRelation: "auditor",
	}
}

func repairedSettingsDoc(uid string) settingsIndexDoc {
	return settingsIndexDoc{
		ObjectID:          uid,
		AccessCheckObject: "committee:" + uid, AccessCheckRelation: "auditor",
		HistoryCheckObject: "committee:" + uid, HistoryCheckRelation: "auditor",
	}
}

// indexerActions returns the published indexer messages as "<action> <committee uid>".
func indexerActions(t *testing.T, pub *mockPublisher) []string {
	t.Helper()
	var actions []string
	for _, raw := range pub.indexerMsgs {
		msg, ok := raw.(*model.CommitteeIndexerMessage)
		require.True(t, ok)
		uid, _ := msg.Data.(string)
		if msg.IndexingConfig != nil {
			uid = msg.IndexingConfig.ObjectID
		}
		actions = append(actions, string(msg.Action)+" "+uid)
	}
	return actions
}

func TestRepairSettingsIndexSubcommand_Run(t *testing.T) {
	notFound := errs.NewNotFound("not found")
	stored := func() *mockReader {
		return &mockReader{
			uids:     []string{"c1"},
			bases:    map[string]*model.CommitteeBase{"c1": {UID: "c1", ProjectUID: "proj-1", Public: true}, "u1": {UID: "u1"}},
			settings: map[string]*model.CommitteeSettings{"c1": {UID: "c1"}, "s1": {UID: "s1"}},
			baseErr: map[string]error{
				"o1": notFound,
				"s1": notFound,
				"b1": errors.New("storage unavailable"),
			},
			settingsErr: map[string]error{
				"o1": notFound,
				"u1": notFound,
				"b1": notFound,
			},
		}
	}

	for _, tc := range []struct {
		name        string
		args        []string
		docs        []settingsIndexDoc
		wantActions []string
		wantErr     string
	}{
		{
			name: "apply re-anchors stored settings and deletes confirmed orphans",
			args: []string{"--apply"},
			// c1 is stored. o1 has no committee or settings and u1 (public) has a committee but no
			// settings: neither document has a settings record, so both are deleted. s1 has settings
			// but no committee, so it is kept for review.
			docs:        []settingsIndexDoc{oldSettingsDoc("c1", true), oldSettingsDoc("o1", false), oldSettingsDoc("u1", true), oldSettingsDoc("s1", false)},
			wantActions: []string{"updated c1", "deleted o1", "deleted u1"},
			wantErr:     "(0 failed, 1 unresolved)",
		},
		{
			name:        "a storage error never deletes a document",
			args:        []string{"--apply"},
			docs:        []settingsIndexDoc{oldSettingsDoc("c1", false), oldSettingsDoc("b1", false)},
			wantActions: []string{"updated c1"},
			wantErr:     "could not be repaired",
		},
		{
			name: "dry run publishes nothing",
			docs: []settingsIndexDoc{oldSettingsDoc("c1", true), oldSettingsDoc("o1", false)},
		},
		{
			name:        "committee-uid limits both passes",
			args:        []string{"--apply", "--committee-uid", "o1"},
			docs:        []settingsIndexDoc{oldSettingsDoc("c1", true), oldSettingsDoc("o1", false)},
			wantActions: []string{"deleted o1"},
		},
		{
			name: "verify passes when every document is repaired",
			args: []string{"--verify"},
			docs: []settingsIndexDoc{repairedSettingsDoc("c1"), repairedSettingsDoc("c2")},
		},
		{
			name:    "verify fails on the old anchor",
			args:    []string{"--verify"},
			docs:    []settingsIndexDoc{repairedSettingsDoc("c1"), oldSettingsDoc("c2", false)},
			wantErr: "1 of 2 committee settings document(s)",
		},
		{
			// A stale duplicate latest copy must not hide behind a repaired copy of the same committee.
			name:    "verify counts duplicate copies",
			args:    []string{"--verify"},
			docs:    []settingsIndexDoc{repairedSettingsDoc("c1"), oldSettingsDoc("c1", true)},
			wantErr: "1 of 2 committee settings document(s)",
		},
		{
			name: "verify fails on a public document",
			args: []string{"--verify"},
			docs: []settingsIndexDoc{func() settingsIndexDoc {
				doc := repairedSettingsDoc("c1")
				doc.Public = true
				return doc
			}()},
			wantErr: "1 of 1 committee settings document(s)",
		},
		{
			name:    "apply and verify are exclusive",
			args:    []string{"--apply", "--verify"},
			wantErr: "cannot be combined",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := &fakeSettingsIndex{docs: tc.docs}
			pub := &mockPublisher{}
			cmd := &repairSettingsIndexSubcommand{openSearch: newTestOpenSearchClient(t, index.handler(t))}

			err := cmd.Run(context.Background(), commands.RunContext{CommitteeReader: stored(), Publisher: pub, Args: tc.args})

			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantActions, indexerActions(t, pub))
			for _, raw := range pub.indexerMsgs {
				msg := raw.(*model.CommitteeIndexerMessage)
				if msg.Action != model.ActionUpdated {
					continue
				}
				uid := msg.IndexingConfig.ObjectID
				assert.Equal(t, "committee:"+uid, msg.IndexingConfig.AccessCheckObject)
				assert.Equal(t, "committee:"+uid, msg.IndexingConfig.HistoryCheckObject)
				assert.Equal(t, "auditor", msg.IndexingConfig.HistoryCheckRelation)
				require.NotNil(t, msg.IndexingConfig.Public)
				assert.False(t, *msg.IndexingConfig.Public)
			}
		})
	}
}

func TestRepairSettingsIndexSubcommand_RunsOffline(t *testing.T) {
	cmd := &repairSettingsIndexSubcommand{}
	assert.True(t, cmd.RunsOffline([]string{"--verify"}), "verify only reads OpenSearch")
	assert.True(t, cmd.RunsOffline([]string{"--opensearch-url", "http://localhost:9200", "--verify=true"}))
	assert.False(t, cmd.RunsOffline([]string{"--apply"}), "repairs need storage and NATS")
	assert.False(t, cmd.RunsOffline(nil), "the dry run reads storage")
}

func TestListSettingsIndexDocs(t *testing.T) {
	var docs []settingsIndexDoc
	for i := 0; i < settingsIndexPageSize+3; i++ {
		docs = append(docs, repairedSettingsDoc(fmt.Sprintf("c%04d", i)))
	}
	index := &fakeSettingsIndex{docs: docs}
	client := newTestOpenSearchClient(t, index.handler(t))

	got, err := listSettingsIndexDocs(context.Background(), client, testOpenSearchIndex, "")
	require.NoError(t, err)
	assert.Len(t, got, settingsIndexPageSize+3, "search_after paging must return every document")
	require.Len(t, index.requests, 2)
	assert.NotContains(t, index.requests[0], "search_after")
	assert.Equal(t, []any{got[settingsIndexPageSize-1].ObjectID}, index.requests[1]["search_after"])

	body, err := json.Marshal(index.requests[0]["query"])
	require.NoError(t, err)
	assert.Contains(t, string(body), `"deleted_at"`, "delete tombstones must be excluded")
	assert.Contains(t, string(body), `{"term":{"latest":true}}`, "older copies (latest=false) must be excluded")
	assert.Contains(t, string(body), `"committee_settings"`)
}

func TestSearchSettingsIndex_RejectsIncompleteResults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		index *fakeSettingsIndex
	}{
		{"timed out", &fakeSettingsIndex{timedOut: true}},
		{"terminated early", &fakeSettingsIndex{terminatedEarly: true}},
		{"failed shards", &fakeSettingsIndex{failedShards: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.index.docs = []settingsIndexDoc{oldSettingsDoc("c1", false)}
			client := newTestOpenSearchClient(t, tc.index.handler(t))
			cmd := &repairSettingsIndexSubcommand{openSearch: client}

			err := cmd.Run(context.Background(), commands.RunContext{Args: []string{"--verify"}})
			require.ErrorContains(t, err, "incomplete settings results")
			_, err = listSettingsIndexDocs(context.Background(), client, testOpenSearchIndex, "")
			require.ErrorContains(t, err, "incomplete settings results")
		})
	}
	index := &fakeSettingsIndex{docs: []settingsIndexDoc{repairedSettingsDoc("c1")}}
	_, err := listSettingsIndexDocs(context.Background(), newTestOpenSearchClient(t, index.handler(t)), testOpenSearchIndex, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"false"}, index.partialAllowedParams, "every search must refuse partial results")
}
