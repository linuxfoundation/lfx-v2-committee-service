// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package m2m

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// DefaultSurveyType is the query-service resource type for surveys.
const DefaultSurveyType = "survey"

// SurveySourceConfig configures the live survey source. An empty BaseURL
// disables the adapter and returns zero surveys without error.
type SurveySourceConfig struct {
	BaseURL string
	Type    string
	Timeout time.Duration
}

// SurveySource is the live SurveySource adapter. It queries:
//
//	GET {BaseURL}/query/resources
//	    ?type={Type}&tags=committee_uid:{uid}
//	    &date_field=survey_cutoff_date&date_from={windowStart}&date_to={windowEnd}
//
// Per-committee response counts are extracted from the committees[] array in
// each resource's data payload. Authentication is by a *http.Client carrying
// M2M client_credentials Authorization (NOT the caller's bearer token).
type SurveySource struct {
	cfg    SurveySourceConfig
	client *http.Client
}

// NewSurveySource constructs a live survey source. The supplied *http.Client
// MUST already carry M2M client_credentials Authorization.
func NewSurveySource(cfg SurveySourceConfig, client *http.Client) *SurveySource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Type == "" {
		cfg.Type = DefaultSurveyType
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &SurveySource{cfg: cfg, client: client}
}

type querySurveyCommittee struct {
	CommitteeUID    string `json:"committee_uid"`
	TotalRecipients int    `json:"total_recipients"`
	TotalResponses  int    `json:"total_responses"`
}

type querySurveyData struct {
	// UID is the survey v2 UUID — per the survey-service indexer contract,
	// data.uid is the v2 UUID and data.id is the v1 ID. Always read from uid.
	UID          string                 `json:"uid"`
	Title        string                 `json:"survey_title"`
	Status       string                 `json:"survey_status"`
	CutoffDate   string                 `json:"survey_cutoff_date"`
	CollectorURL string                 `json:"collector_url"`
	Committees   []querySurveyCommittee `json:"committees"`
}

// ListSurveyActivityForWindow fetches surveys tagged with the committee UID
// whose cutoff date falls within [windowStart, windowEnd]. Per-committee
// response counts are extracted from the committees[] array in each record.
func (s *SurveySource) ListSurveyActivityForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]port.SurveyActivity, error) {
	if s == nil || s.cfg.BaseURL == "" {
		slog.WarnContext(ctx, "survey source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}

	u, err := url.Parse(s.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u.Path = appendPath(u.Path, "/query/resources")
	q := u.Query()
	q.Set("v", "1")
	q.Set("type", s.cfg.Type)
	q.Set("tags", "committee_uid:"+committeeUID)
	// Ask the query service to filter by cutoff date within the window.
	q.Set("date_field", "survey_cutoff_date")
	q.Set("date_from", windowStart.UTC().Format(time.RFC3339Nano))
	q.Set("date_to", windowEnd.UTC().Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build survey-source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("survey source request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("survey source returned non-2xx: status=%d body=%s", resp.StatusCode, string(body))
	}

	var env queryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode survey-source response: %w", err)
	}

	out := make([]port.SurveyActivity, 0, len(env.Resources))
	for _, r := range env.Resources {
		var data querySurveyData
		if len(r.Data) > 0 {
			if err := json.Unmarshal(r.Data, &data); err != nil {
				slog.WarnContext(ctx, "skipping survey record with malformed data",
					"uid", r.UID, "error", err)
				continue
			}
		}

		cutoff, err := time.Parse(time.RFC3339, data.CutoffDate)
		if err != nil {
			// Try RFC3339Nano in case the indexer emits nanoseconds.
			cutoff, err = time.Parse(time.RFC3339Nano, data.CutoffDate)
			if err != nil {
				slog.WarnContext(ctx, "skipping survey with unparseable cutoff date",
					"uid", r.UID, "cutoff_date", data.CutoffDate, "error", err)
				continue
			}
		}

		// Client-side guard: only keep surveys whose cutoff falls in the window
		// and has already passed — excluding surveys still collecting responses.
		if cutoff.Before(windowStart) || cutoff.After(windowEnd) || cutoff.After(time.Now()) {
			continue
		}

		// Find per-committee counts from the committees array.
		var recipients, responses int
		for _, c := range data.Committees {
			if c.CommitteeUID == committeeUID {
				recipients = c.TotalRecipients
				responses = c.TotalResponses
				break
			}
		}

		out = append(out, port.SurveyActivity{
			SurveyUID:       data.UID,
			Title:           data.Title,
			Status:          data.Status,
			CutoffDate:      cutoff.UTC(),
			TotalRecipients: recipients,
			TotalResponses:  responses,
			URL:             data.CollectorURL,
		})
	}
	return out, nil
}
