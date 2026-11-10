// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	committeeservicesvr "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	goahttp "goa.design/goa/v3/http"
)

func TestRequestDecoder_CreateCommittee(t *testing.T) {
	const projectA = "7cad5a8d-19d0-41a4-81a6-043453daf9ee"
	const projectB = "a1b2c3d4-e5f6-4890-abcd-ef1234567890"
	fields := `,"name":"Example Committee","category":"Other"`
	canonical := `"project_uid":"` + projectA + `"`
	variant := `"PROJECT_UID":"` + projectB + `"`
	validBody := `{` + canonical + fields + `}`

	for _, tt := range []struct {
		name        string
		body        string
		contentType string
		status      int
	}{
		{"canonical key", validBody, "application/json", http.StatusCreated},
		{"JSON with charset", validBody, "application/json; charset=utf-8", http.StatusCreated},
		{"default JSON", validBody, "", http.StatusCreated},
		{"trailing whitespace", validBody + " \n\t", "application/json", http.StatusCreated},
		{"escaped canonical key", `{"project\u005fuid":"` + projectA + `"` + fields + `}`, "application/json", http.StatusCreated},
		{"nested unrelated keys", `{` + canonical + fields + `,"extra":{"PROJECT_UID":"` + projectB + `","items":[{"project_uid":null}]}}`, "application/json", http.StatusCreated},
		{"variant after canonical", `{` + canonical + `,` + variant + fields + `}`, "application/json", http.StatusBadRequest},
		{"variant before canonical", `{` + variant + `,` + canonical + fields + `}`, "application/json", http.StatusBadRequest},
		{"variant only", `{` + variant + fields + `}`, "application/json", http.StatusBadRequest},
		{"mixed case", `{` + canonical + `,"Project_Uid":"` + projectB + `"` + fields + `}`, "application/json", http.StatusBadRequest},
		{"escaped variant", `{` + canonical + `,"\u0050ROJECT_UID":"` + projectB + `"` + fields + `}`, "application/json", http.StatusBadRequest},
		{"duplicate canonical", `{` + canonical + `,"project_uid":"` + projectB + `"` + fields + `}`, "application/json", http.StatusBadRequest},
		{"duplicate same value", `{` + canonical + `,` + canonical + fields + `}`, "application/json", http.StatusBadRequest},
		{"escaped duplicate", `{` + canonical + `,"project\u005fuid":"` + projectB + `"` + fields + `}`, "application/json", http.StatusBadRequest},
		{"null variant", `{` + canonical + `,"PROJECT_UID":null` + fields + `}`, "application/json", http.StatusBadRequest},
		{"missing project", `{"name":"Example Committee","category":"Other"}`, "application/json", http.StatusBadRequest},
		{"null project", `{"project_uid":null` + fields + `}`, "application/json", http.StatusBadRequest},
		{"invalid UUID", `{"project_uid":"not-a-uuid"` + fields + `}`, "application/json", http.StatusBadRequest},
		{"wrong project type", `{"project_uid":123` + fields + `}`, "application/json", http.StatusBadRequest},
		{"missing required name", `{` + canonical + `,"category":"Other"}`, "application/json", http.StatusBadRequest},
		{"invalid category", `{` + canonical + `,"name":"Example Committee","category":"invalid"}`, "application/json", http.StatusBadRequest},
		{"malformed JSON", `{` + canonical + `,`, "application/json", http.StatusBadRequest},
		{"empty body", "", "application/json", http.StatusBadRequest},
		{"null body", "null", "application/json", http.StatusBadRequest},
		{"array body", `[` + validBody + `]`, "application/json", http.StatusBadRequest},
		{"string body", `"example"`, "application/json", http.StatusBadRequest},
		{"second object", validBody + ` {"project_uid":"` + projectB + `"}`, "application/json", http.StatusBadRequest},
		{"trailing garbage", validBody + ` invalid`, "application/json", http.StatusBadRequest},
		{"XML encoding", `<body><project_uid>` + projectB + `</project_uid></body>`, "application/xml", http.StatusBadRequest},
		{"gob encoding", "", "application/gob", http.StatusBadRequest},
		{"unsupported media type", validBody, "text/plain", http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// A map models the case-sensitive project lookup at the edge. Any
			// accepted request must deliver that same project to the endpoint.
			var edgeBody map[string]any
			_ = json.Unmarshal([]byte(tt.body), &edgeBody)
			called := false
			mux := goahttp.NewMuxer()
			endpoint := func(_ context.Context, v any) (any, error) {
				called = true
				payload := v.(*committeeservice.CreateCommitteePayload)
				if payload.ProjectUID != projectA || payload.ProjectUID != edgeBody["project_uid"] {
					t.Fatalf("decoded project %q differs from authorized project %v", payload.ProjectUID, edgeBody["project_uid"])
				}
				return &committeeservice.CommitteeFullWithReadonlyAttributes{ProjectUID: &payload.ProjectUID}, nil
			}
			handler := committeeservicesvr.NewCreateCommitteeHandler(endpoint, mux, requestDecoder, goahttp.ResponseEncoder,
				func(_ context.Context, _ http.ResponseWriter, err error) { t.Errorf("transport error: %v", err) }, nil)
			committeeservicesvr.MountCreateCommitteeHandler(mux, handler)
			req := httptest.NewRequest(http.MethodPost, "/committees?v=1", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			rec := httptest.NewRecorder()
			requestBodyLimitMiddleware()(mux).ServeHTTP(rec, req)
			if rec.Code != tt.status || called != (tt.status == http.StatusCreated) {
				t.Fatalf("status = %d, endpoint called = %t; want %d; response: %s", rec.Code, called, tt.status, rec.Body.String())
			}
		})
	}
}

func TestRequestDecoder_OtherPayloads(t *testing.T) {
	for _, contentType := range []string{"application/json", "application/xml"} {
		t.Run(contentType, func(t *testing.T) {
			body := `{"organization":{"name":"Example Org"}}`
			if contentType == "application/xml" {
				body = `<body><organization><name>Example Org</name></organization></body>`
			}
			req := httptest.NewRequest(http.MethodPost, "/committees/example/join", strings.NewReader(body))
			req.Header.Set("Content-Type", contentType)
			var payload committeeservicesvr.JoinCommitteeRequestBody
			if err := requestDecoder(req).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Organization == nil || payload.Organization.Name == nil || *payload.Organization.Name != "Example Org" {
				t.Fatalf("unexpected decoded payload: %s", fmt.Sprint(payload))
			}
		})
	}
}

func TestRequestDecoder_BulkCreateCommitteeMembers(t *testing.T) {
	const committeeUID = "7cad5a8d-19d0-41a4-81a6-043453daf9ee"
	const first = `{"email":"first.last@example.com"}`
	const second = `{"email":"first.last+2@example.com"}`
	rows := func(n int) string {
		return `{"members":[` + strings.Repeat(first+",", n-1) + first + `]}`
	}
	for _, tt := range []struct {
		name      string
		body      string
		status    int
		wantRows  int
		wantError string
	}{
		{"null row", `{"members":[null]}`, http.StatusBadRequest, 0, "members[0]"},
		{"null first row", `{"members":[null,` + first + `]}`, http.StatusBadRequest, 0, "members[0]"},
		{"null middle row", `{"members":[` + first + `,null,` + second + `]}`, http.StatusBadRequest, 0, "members[1]"},
		{"null last row", `{"members":[` + first + `,null]}`, http.StatusBadRequest, 0, "members[1]"},
		{"one valid row", rows(1), http.StatusOK, 1, ""},
		{"valid row order", `{"members":[` + first + `,` + second + `]}`, http.StatusOK, 2, ""},
		{"maximum rows", rows(100), http.StatusOK, 100, ""},
		{"empty members", `{"members":[]}`, http.StatusBadRequest, 0, ""},
		{"too many members", rows(101), http.StatusBadRequest, 0, ""},
		{"null members", `{"members":null}`, http.StatusBadRequest, 0, ""},
		{"missing email", `{"members":[{}]}`, http.StatusBadRequest, 0, ""},
		{"null email", `{"members":[{"email":null}]}`, http.StatusBadRequest, 0, ""},
		{"invalid email", `{"members":[{"email":"not-an-email"}]}`, http.StatusBadRequest, 0, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			mux := goahttp.NewMuxer()
			endpoint := func(_ context.Context, v any) (any, error) {
				called = true
				payload := v.(*committeeservice.BulkCreateCommitteeMembersPayload)
				if payload.UID != committeeUID || len(payload.Members) != tt.wantRows {
					t.Fatalf("unexpected bulk payload: %+v", payload)
				}
				if tt.name == "valid row order" && (payload.Members[0].Email != "first.last@example.com" || payload.Members[1].Email != "first.last+2@example.com") {
					t.Fatalf("submitted row order changed: %+v", payload.Members)
				}
				return &committeeservice.CommitteeMemberBulkCreateResult{
					Results: []*committeeservice.CommitteeMemberBulkCreateRowResult{},
					Added:   len(payload.Members),
				}, nil
			}
			handler := committeeservicesvr.NewBulkCreateCommitteeMembersHandler(endpoint, mux, requestDecoder, goahttp.ResponseEncoder,
				func(_ context.Context, _ http.ResponseWriter, err error) { t.Errorf("transport error: %v", err) }, nil)
			committeeservicesvr.MountBulkCreateCommitteeMembersHandler(mux, handler)
			req := httptest.NewRequest(http.MethodPost, "/committees/"+committeeUID+"/members/bulk?v=1", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			requestBodyLimitMiddleware()(mux).ServeHTTP(rec, req)
			if rec.Code != tt.status || called != (tt.status == http.StatusOK) {
				t.Fatalf("status = %d, endpoint called = %t; want %d; response: %s", rec.Code, called, tt.status, rec.Body.String())
			}
			if tt.wantError != "" && !strings.Contains(rec.Body.String(), tt.wantError) {
				t.Fatalf("response must identify %s: %s", tt.wantError, rec.Body.String())
			}
		})
	}
}
