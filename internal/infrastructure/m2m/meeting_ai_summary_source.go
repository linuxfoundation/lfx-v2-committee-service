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

// MeetingAISummarySourceConfig configures the live AI summary source. All
// fields are sourced from environment variables in providers.go; an empty
// BaseURL disables the client and produces a zero-summary result without error.
type MeetingAISummarySourceConfig struct {
	BaseURL string
	Timeout time.Duration
}

// MeetingAISummarySource is the live MeetingAISummarySource adapter. It speaks
//
//	GET {BaseURL}/query/resources?type=v1_past_meeting_summary
//	    &tags=committee:{uid}
//	    &summary_start_time[gte]={windowStart}&summary_start_time[lte]={windowEnd}
//
// against the query-service, then filters in-process for approved summaries.
// Authentication is by the same M2M *http.Client used by MeetingSource.
type MeetingAISummarySource struct {
	cfg    MeetingAISummarySourceConfig
	client *http.Client
}

// NewMeetingAISummarySource constructs a live AI summary source. The supplied
// *http.Client MUST already carry M2M client_credentials Authorization.
func NewMeetingAISummarySource(cfg MeetingAISummarySourceConfig, client *http.Client) *MeetingAISummarySource {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	} else if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
	}
	return &MeetingAISummarySource{cfg: cfg, client: client}
}

// querySummaryData is the data payload for a v1_past_meeting_summary resource.
// Only the fields required by the weekly-brief generator are decoded; unknown
// attributes are silently ignored.
type querySummaryData struct {
	MeetingAndOccurrenceID string `json:"meeting_and_occurrence_id"`
	ZoomMeetingTopic       string `json:"zoom_meeting_topic"`
	SummaryTitle           string `json:"summary_title"`
	SummaryStartTime       string `json:"summary_start_time"`
	Content                string `json:"content"`
	RequiresApproval       bool   `json:"requires_approval"`
	Approved               bool   `json:"approved"`
	AISummaryAccess        string `json:"ai_summary_access"`
}

// ListAISummariesForWindow fetches approved AI-generated meeting summaries for
// the given committee whose summary_start_time falls in [windowStart, windowEnd].
// Records where approved == false are filtered out in-process. When RequiresApproval
// is false the summary is auto-approved by the Zoom pipeline, so it passes.
func (m *MeetingAISummarySource) ListAISummariesForWindow(ctx context.Context, committeeUID string, windowStart, windowEnd time.Time) ([]port.MeetingAISummaryActivity, error) {
	if m == nil || m.cfg.BaseURL == "" {
		slog.WarnContext(ctx, "meeting AI summary source disabled: QUERY_SERVICE_URL not set")
		return nil, nil
	}

	u, err := url.Parse(m.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid query-service base URL: %w", err)
	}
	u.Path = appendPath(u.Path, "/query/resources")
	q := u.Query()
	q.Set("v", "1")
	q.Set("type", "v1_past_meeting_summary")
	q.Set("tags", "committee:"+committeeUID)
	q.Set("summary_start_time[gte]", windowStart.UTC().Format(time.RFC3339Nano))
	q.Set("summary_start_time[lte]", windowEnd.UTC().Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build AI summary source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("AI summary source request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("AI summary source returned non-2xx: status=%d body=%s", resp.StatusCode, string(body))
	}

	var env queryEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode AI summary source response: %w", err)
	}

	out := make([]port.MeetingAISummaryActivity, 0, len(env.Resources))
	for _, r := range env.Resources {
		if len(r.Data) == 0 || string(r.Data) == "null" {
			// Records with no data payload carry no approval metadata; treat them
			// as missing rather than auto-approved (zero-value RequiresApproval=false
			// would otherwise pass the gate below).
			slog.WarnContext(ctx, "skipping AI summary record with missing data payload", "uid", r.UID)
			continue
		}
		var data querySummaryData
		if err := json.Unmarshal(r.Data, &data); err != nil {
			slog.WarnContext(ctx, "skipping AI summary record with malformed data",
				"uid", r.UID, "error", err)
			continue
		}

		// Only use summaries that have been approved. When requires_approval is
		// false the Zoom pipeline auto-publishes without human review, which still
		// counts as approved for our purposes.
		if !data.Approved && data.RequiresApproval {
			continue
		}

		title := data.SummaryTitle
		if title == "" {
			title = data.ZoomMeetingTopic
		}

		var start time.Time
		if data.SummaryStartTime != "" {
			if t, err := time.Parse(time.RFC3339Nano, data.SummaryStartTime); err == nil {
				start = t
			}
		}

		out = append(out, port.MeetingAISummaryActivity{
			MeetingAndOccurrenceID: data.MeetingAndOccurrenceID,
			Title:                  title,
			StartTime:              start,
			Content:                data.Content,
			Private:                data.AISummaryAccess != "public",
		})
	}
	return out, nil
}
