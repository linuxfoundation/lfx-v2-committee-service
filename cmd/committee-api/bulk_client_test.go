// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	client "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/client"
	server "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	"github.com/stretchr/testify/require"
	goahttp "goa.design/goa/v3/http"
	"gopkg.in/yaml.v3"
)

func TestBulkCLIInput(t *testing.T) {
	const uid = "7cad5a8d-19d0-41a4-81a6-043453daf9ee"
	const row = `{"email":"first.last@example.com"}`
	for _, tt := range []struct{ name, body, wantError string }{
		{"null first", `{"members":[null,` + row + `]}`, "members[0]"},
		{"null middle", `{"members":[` + row + `,null,` + row + `]}`, "members[1]"},
		{"null last", `{"members":[` + row + `,null]}`, "members[1]"},
		{"email only", `{"members":[` + row + `]}`, ""},
		{"nested defaults", `{"members":[{"email":"first.last@example.com","role":{},"voting":{}}]}`, ""},
		{"null optional", `{"members":[{"email":"first.last@example.com","appointed_by":null,"status":null}]}`, ""},
		{"explicit empty", `{"members":[{"email":"first.last@example.com","appointed_by":""}]}`, "appointed_by"},
		{"invalid status", `{"members":[{"email":"first.last@example.com","status":"invalid"}]}`, "status"},
		{"invalid role", `{"members":[{"email":"first.last@example.com","role":{"name":"invalid"}}]}`, "role.name"},
		{"invalid voting", `{"members":[{"email":"first.last@example.com","voting":{"status":"invalid"}}]}`, "voting.status"},
		{"order", `{"members":[` + row + `,{"email":"first.last+2@example.com"}]}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var cliPayload *committeeservice.BulkCreateCommitteeMembersPayload
			var err error
			require.NotPanics(t, func() {
				cliPayload, err = client.BuildBulkCreateCommitteeMembersPayload(tt.body, uid, "1", "", "", "", "")
			})
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.Nil(t, cliPayload)
				return
			}
			require.NoError(t, err)
			var httpPayload *committeeservice.BulkCreateCommitteeMembersPayload
			mux := goahttp.NewMuxer()
			handler := server.NewBulkCreateCommitteeMembersHandler(func(_ context.Context, payload any) (any, error) {
				httpPayload = payload.(*committeeservice.BulkCreateCommitteeMembersPayload)
				return &committeeservice.CommitteeMemberBulkCreateResult{Results: []*committeeservice.CommitteeMemberBulkCreateRowResult{}}, nil
			}, mux, requestDecoder, goahttp.ResponseEncoder, nil, nil)
			server.MountBulkCreateCommitteeMembersHandler(mux, handler)
			req := httptest.NewRequest(http.MethodPost, "/committees/"+uid+"/members/bulk?v=1", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.NotNil(t, httpPayload)
			require.Equal(t, httpPayload.Members, cliPayload.Members, "CLI omission/default semantics must match HTTP")
			require.Equal(t, "None", cliPayload.Members[0].AppointedBy)
			require.Equal(t, "Active", cliPayload.Members[0].Status)
		})
	}
}

func TestBulkMemberExamples(t *testing.T) {
	for _, file := range []string{"openapi.json", "openapi3.json", "openapi.yaml", "openapi3.yaml"} {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile("../../gen/http/" + file)
			require.NoError(t, err)
			var document map[string]any
			if strings.HasSuffix(file, ".json") {
				require.NoError(t, json.Unmarshal(data, &document))
			} else {
				require.NoError(t, yaml.Unmarshal(data, &document))
			}
			schemas, ok := document["definitions"].(map[string]any)
			if !ok {
				schemas = document["components"].(map[string]any)["schemas"].(map[string]any)
			}
			example := schemas["CommitteeMemberBulkCreateResult"].(map[string]any)["example"]
			encoded, err := json.Marshal(example)
			require.NoError(t, err)
			var result struct {
				Added, Skipped, Failed int
				Results                []struct {
					Index          int
					Status, Reason string
					Retryable      bool
					Member         map[string]any
				}
			}
			require.NoError(t, json.Unmarshal(encoded, &result))
			counts := map[string]int{}
			for i, row := range result.Results {
				require.Equal(t, i, row.Index)
				counts[row.Status]++
				if row.Status == "added" {
					require.Empty(t, row.Reason)
					require.False(t, row.Retryable)
					require.NotEmpty(t, row.Member)
				} else {
					require.NotEmpty(t, row.Reason)
					require.Empty(t, row.Member)
				}
			}
			require.Equal(t, result.Added, counts["added"])
			require.Equal(t, result.Skipped, counts["skipped"])
			require.Equal(t, result.Failed, counts["failed"])
		})
	}
}
