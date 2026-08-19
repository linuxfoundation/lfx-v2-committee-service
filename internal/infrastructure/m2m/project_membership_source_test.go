// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newProjectMembershipSource(t *testing.T, srv *httptest.Server) *ProjectMembershipSource {
	t.Helper()
	return NewProjectMembershipSource(
		ProjectMembershipSourceConfig{BaseURL: srv.URL, Timeout: 5 * time.Second},
		srv.Client(),
	)
}

// ── Disabled (empty BaseURL) → nil, no error ─────────────────────────────────

func TestListMembershipActivityForWindow_DisabledBaseURL_ReturnsNil(t *testing.T) {
	src := NewProjectMembershipSource(ProjectMembershipSourceConfig{BaseURL: ""}, nil)
	got, err := src.ListMembershipActivityForWindow(context.Background(), "proj-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Empty projectUID → nil, no error (soft degrade) ──────────────────────────

func TestListMembershipActivityForWindow_EmptyProjectUID_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Should never be called — empty projectUID degrades before the request.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := newProjectMembershipSource(t, srv)
	got, err := src.ListMembershipActivityForWindow(context.Background(), "",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Field mapping: active membership included, non-active excluded ────────────

func TestListMembershipActivityForWindow_FieldMappingAndStatusFilter(t *testing.T) {
	windowStart := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 8, 17, 23, 59, 59, 0, time.UTC)

	activeData := queryProjectMembershipData{
		UID:            "m-active",
		AccountName:    "Example Corp",
		MembershipTier: "Silver",
		PurchaseDate:   "2026-08-14T00:00:00Z",
		Status:         "Active",
	}
	inactiveData := queryProjectMembershipData{
		UID:            "m-inactive",
		AccountName:    "Inactive Inc",
		MembershipTier: "Gold",
		PurchaseDate:   "2026-08-15T00:00:00Z",
		Status:         "Expired",
	}
	activeJSON, _ := json.Marshal(activeData)
	inactiveJSON, _ := json.Marshal(inactiveData)

	body := []byte(`{"resources":[` +
		`{"id":"os-ignored-1","data":` + string(activeJSON) + `},` +
		`{"id":"os-ignored-2","data":` + string(inactiveJSON) + `}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newProjectMembershipSource(t, srv)
	got, err := src.ListMembershipActivityForWindow(context.Background(), "proj-1", windowStart, windowEnd)

	require.NoError(t, err)
	require.Len(t, got, 1, "only Active memberships should be included")
	assert.Equal(t, "m-active", got[0].MembershipUID)
	assert.Equal(t, "Example Corp", got[0].AccountName)
	assert.Equal(t, "Silver", got[0].Tier)
	assert.Equal(t, "Active", got[0].Status)
	assert.Equal(t, time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC), got[0].PurchaseDate)
}

// ── PurchaseDate parse failure → zero time, record still included ─────────────

func TestListMembershipActivityForWindow_BadPurchaseDate_ZeroTime(t *testing.T) {
	data := queryProjectMembershipData{
		UID:          "m-bad-date",
		AccountName:  "No Date Corp",
		PurchaseDate: "not-a-date",
		Status:       "Active",
	}
	dataJSON, _ := json.Marshal(data)
	body := []byte(`{"resources":[{"id":"x","data":` + string(dataJSON) + `}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newProjectMembershipSource(t, srv)
	got, err := src.ListMembershipActivityForWindow(context.Background(), "proj-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].PurchaseDate.IsZero(), "unparseable purchase_date should produce zero time")
}

// ── Malformed data payload → record skipped ───────────────────────────────────

func TestListMembershipActivityForWindow_MalformedRecord_Skipped(t *testing.T) {
	goodData := queryProjectMembershipData{
		UID:    "m-good",
		Status: "Active",
	}
	goodJSON, _ := json.Marshal(goodData)

	body := []byte(`{"resources":[` +
		`{"id":"bad","data":[1,2,3]},` +
		`{"id":"good","data":` + string(goodJSON) + `}` +
		`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := newProjectMembershipSource(t, srv)
	got, err := src.ListMembershipActivityForWindow(context.Background(), "proj-1",
		time.Now(), time.Now().Add(time.Hour))

	require.NoError(t, err)
	require.Len(t, got, 1, "malformed record must be skipped")
	assert.Equal(t, "m-good", got[0].MembershipUID)
}

// ── Query string includes expected tags and date parameters ───────────────────

func TestListMembershipActivityForWindow_QueryParameters(t *testing.T) {
	windowStart := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 8, 17, 23, 59, 59, 0, time.UTC)

	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[]}`))
	}))
	defer srv.Close()

	src := newProjectMembershipSource(t, srv)
	_, err := src.ListMembershipActivityForWindow(context.Background(), "proj-abc", windowStart, windowEnd)

	require.NoError(t, err)
	assert.Contains(t, capturedURL, "type=project_membership")
	assert.Contains(t, capturedURL, "project_uid%3Aproj-abc")
	assert.Contains(t, capturedURL, "date_field=purchase_date")
}

// ── Non-2xx response → error ──────────────────────────────────────────────────

func TestListMembershipActivityForWindow_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := newProjectMembershipSource(t, srv)
	_, err := src.ListMembershipActivityForWindow(context.Background(), "proj-1",
		time.Now(), time.Now().Add(time.Hour))

	require.Error(t, err)
}
