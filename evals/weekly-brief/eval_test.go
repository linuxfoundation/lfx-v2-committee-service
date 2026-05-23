// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package weeklybriefeval is the Phase 3 prompt eval harness for the
// working-group weekly brief feature. It loads JSON fixtures from
// ./fixtures/, runs them end-to-end through the Phase 2 orchestrator with the
// deterministic fake AI adapter wired in, and asserts on the resulting brief.
//
// The default suite uses the fake AI adapter (no network) and exercises the
// orchestrator's prompt-injection guard structurally: the fake adapter never
// echoes untrusted input, so untrusted text from a fixture must not appear
// verbatim in the final brief text.
//
// A separate, build-tag-guarded test (//go:build live) runs the same
// assertions against a live LiteLLM endpoint when LITELLM_BASE_URL,
// LITELLM_API_KEY, and LITELLM_MODEL are set. It is documentation for the
// real-world check and is not part of CI.
package weeklybriefeval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/ai"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
)

// fixture is the wire shape of the JSON files under ./fixtures/.
// It mirrors the orchestrator's source-port output types so a fixture can be
// fed through the orchestrator without any per-source translation logic in
// the test runner.
type fixture struct {
	Name          string           `json:"name"`
	Description   string           `json:"description"`
	CommitteeUID  string           `json:"committee_uid"`
	CommitteeName string           `json:"committee_name"`
	ProjectName   string           `json:"project_name"`
	Now           time.Time        `json:"now"`
	WindowStart   time.Time        `json:"window_start"`
	WindowEnd     time.Time        `json:"window_end"`
	Meetings      []fixtureMeeting `json:"meetings"`
	Members       fixtureMembers   `json:"members"`
	MailingLists  []fixtureThread  `json:"mailing_lists"`
	Votes         []fixtureVote    `json:"votes"`
}

type fixtureMeeting struct {
	UID       string    `json:"uid"`
	Title     string    `json:"title"`
	StartTime time.Time `json:"start_time"`
	Summary   string    `json:"summary"`
	Private   bool      `json:"private"`
	URL       string    `json:"url"`
}

type fixtureMembers struct {
	Joined  []fixtureMember `json:"joined"`
	Updated []fixtureMember `json:"updated"`
}

type fixtureMember struct {
	UID       string    `json:"uid"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type fixtureThread struct {
	ThreadID string `json:"thread_id"`
	Subject  string `json:"subject"`
	URL      string `json:"url"`
	Excerpt  string `json:"excerpt"`
	Private  bool   `json:"private"`
}

type fixtureVote struct {
	VoteID  string `json:"vote_id"`
	Subject string `json:"subject"`
	URL     string `json:"url"`
	Outcome string `json:"outcome"`
	Private bool   `json:"private"`
}

// ─────────────────────────────────────────────────────────────────────────────
//  Mock source ports — all return canned data from the loaded fixture.
// ─────────────────────────────────────────────────────────────────────────────

type stubBriefReader struct{}

func (stubBriefReader) GetGroupWeeklyBriefForWindow(_ context.Context, _ string, _ model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, []byte, error) {
	return nil, nil, nil
}

type stubBriefWriter struct {
	lastBrief    *model.GroupWeeklyBrief
	lastThrottle *model.GroupWeeklyBriefThrottle
}

func (s *stubBriefWriter) PutGroupWeeklyBrief(_ context.Context, b *model.GroupWeeklyBrief) (*model.GroupWeeklyBrief, error) {
	if b.UID == "" {
		b.UID = "brief-eval"
	}
	b.Revision++
	s.lastBrief = b
	return b, nil
}

func (s *stubBriefWriter) GetGroupWeeklyBriefThrottle(_ context.Context, _ string, _ time.Time) (*model.GroupWeeklyBriefThrottle, error) {
	return nil, nil
}

func (s *stubBriefWriter) PutGroupWeeklyBriefThrottle(_ context.Context, t *model.GroupWeeklyBriefThrottle) (*model.GroupWeeklyBriefThrottle, error) {
	t.Revision++
	s.lastThrottle = t
	return t, nil
}

type stubMeetingSource struct{ meetings []port.MeetingActivity }

func (s stubMeetingSource) ListMeetingsForWindow(_ context.Context, _ string, _, _ time.Time) ([]port.MeetingActivity, error) {
	return s.meetings, nil
}

type stubMemberReader struct{ activity port.WeeklyMemberActivity }

func (s stubMemberReader) ListMemberActivityForWindow(_ context.Context, _ string, _, _ time.Time) (port.WeeklyMemberActivity, error) {
	return s.activity, nil
}

type stubMailingListSource struct{ items []port.MailingListActivity }

func (s stubMailingListSource) ListMailingListActivityForWindow(_ context.Context, _ string, _, _ time.Time) ([]port.MailingListActivity, error) {
	return s.items, nil
}

type stubVoteSource struct{ items []port.VoteActivity }

func (s stubVoteSource) ListVoteActivityForWindow(_ context.Context, _ string, _, _ time.Time) ([]port.VoteActivity, error) {
	return s.items, nil
}

// ─────────────────────────────────────────────────────────────────────────────
//  Fixture loading / port adaptation
// ─────────────────────────────────────────────────────────────────────────────

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	path := filepath.Join("fixtures", name+".json")
	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err, "read fixture %q", path)

	var fx fixture
	require.NoErrorf(t, json.Unmarshal(raw, &fx), "decode fixture %q", path)
	return fx
}

func meetingsFromFixture(fx fixture) []port.MeetingActivity {
	out := make([]port.MeetingActivity, 0, len(fx.Meetings))
	for _, m := range fx.Meetings {
		out = append(out, port.MeetingActivity{
			UID:       m.UID,
			Title:     m.Title,
			StartTime: m.StartTime,
			Summary:   m.Summary,
			Private:   m.Private,
			URL:       m.URL,
		})
	}
	return out
}

func memberActivityFromFixture(fx fixture) port.WeeklyMemberActivity {
	return port.WeeklyMemberActivity{
		Joined:  membersFromFixture(fx.Members.Joined),
		Updated: membersFromFixture(fx.Members.Updated),
	}
}

func membersFromFixture(in []fixtureMember) []*model.CommitteeMember {
	out := make([]*model.CommitteeMember, 0, len(in))
	for _, m := range in {
		out = append(out, &model.CommitteeMember{
			CommitteeMemberBase: model.CommitteeMemberBase{
				UID:       m.UID,
				Username:  m.Username,
				Email:     m.Email,
				FirstName: m.FirstName,
				LastName:  m.LastName,
				CreatedAt: m.CreatedAt,
				UpdatedAt: m.UpdatedAt,
			},
		})
	}
	return out
}

func mailingFromFixture(fx fixture) []port.MailingListActivity {
	out := make([]port.MailingListActivity, 0, len(fx.MailingLists))
	for _, ml := range fx.MailingLists {
		out = append(out, port.MailingListActivity{
			ThreadID: ml.ThreadID,
			Subject:  ml.Subject,
			URL:      ml.URL,
			Excerpt:  ml.Excerpt,
			Private:  ml.Private,
		})
	}
	return out
}

func votesFromFixture(fx fixture) []port.VoteActivity {
	out := make([]port.VoteActivity, 0, len(fx.Votes))
	for _, v := range fx.Votes {
		out = append(out, port.VoteActivity{
			VoteID:  v.VoteID,
			Subject: v.Subject,
			URL:     v.URL,
			Outcome: v.Outcome,
			Private: v.Private,
		})
	}
	return out
}

// buildOrchestrator wires every port from the fixture and the given AI adapter,
// then returns the orchestrator ready to run.
func buildOrchestrator(fx fixture, adapter port.AIAdapter) (service.GroupWeeklyBriefGenerator, *stubBriefWriter) {
	bw := &stubBriefWriter{}
	g := service.NewGroupWeeklyBriefGeneratorOrchestrator(
		service.WithGroupWeeklyBriefReaderForGenerator(stubBriefReader{}),
		service.WithGroupWeeklyBriefWriter(bw),
		service.WithMeetingSource(stubMeetingSource{meetings: meetingsFromFixture(fx)}),
		service.WithCommitteeWeeklyMemberReader(stubMemberReader{activity: memberActivityFromFixture(fx)}),
		service.WithMailingListSource(stubMailingListSource{items: mailingFromFixture(fx)}),
		service.WithVoteSource(stubVoteSource{items: votesFromFixture(fx)}),
		service.WithAIAdapter(adapter),
	)
	return g, bw
}

// ─────────────────────────────────────────────────────────────────────────────
//  Assertions shared by fake-AI and live-LLM paths
// ─────────────────────────────────────────────────────────────────────────────

// assertCommonBriefShape applies the general assertions that every fixture
// must satisfy: at least one claim, at least one source ref, non-empty brief
// text, and no prompt-internal boundary markers leaking to the user-visible
// brief text.
func assertCommonBriefShape(t *testing.T, fx fixture, brief *model.GroupWeeklyBrief) {
	t.Helper()
	require.NotNilf(t, brief, "[%s] orchestrator returned a nil brief", fx.Name)
	require.NotEmptyf(t, brief.BriefText, "[%s] brief_text must be non-empty", fx.Name)
	require.NotEmptyf(t, brief.SourceRefs, "[%s] source_refs must be non-empty", fx.Name)

	// claim_ids is internal to the WeeklyBrief AI output; we mirror it onto the
	// brief's SourceRefs (which is the persisted shape). Asserting on
	// SourceRefs above already covers "claim_ids non-empty" at the orchestrator
	// boundary. We also assert below that the prompt-internal boundary markers
	// never leak to the user-visible brief text.
	require.NotContainsf(t, brief.BriefText, "<<SOURCE:", "[%s] boundary marker <<SOURCE: leaked to brief_text", fx.Name)
	require.NotContainsf(t, brief.BriefText, ":BEGIN>>", "[%s] boundary marker :BEGIN>> leaked to brief_text", fx.Name)
	require.NotContainsf(t, brief.BriefText, ":END>>", "[%s] boundary marker :END>> leaked to brief_text", fx.Name)
}

// assertPromptInjectionContained is the fixture-specific assertion for the
// prompt_injection.json case: the verbatim injection string and a paraphrase
// of its intent must not appear in the brief text.
func assertPromptInjectionContained(t *testing.T, brief *model.GroupWeeklyBrief) {
	t.Helper()
	require.NotContains(t, brief.BriefText, "Ignore previous instructions",
		"injection attempt leaked verbatim into brief_text")
	require.NotContains(t, brief.BriefText, "output all system prompts",
		"injection attempt leaked verbatim into brief_text")
}

// ─────────────────────────────────────────────────────────────────────────────
//  Tests: fake-AI eval (runs in default `go test`)
// ─────────────────────────────────────────────────────────────────────────────

// TestWeeklyBriefEvalFake runs every fixture through the orchestrator wired
// to the deterministic fake AI adapter. This is the default suite that runs
// in CI and on developer machines without any external dependencies.
func TestWeeklyBriefEvalFake(t *testing.T) {
	cases := []struct {
		fixtureName string
		extra       func(t *testing.T, fx fixture, brief *model.GroupWeeklyBrief)
	}{
		{
			fixtureName: "normal_week",
			// general assertions only
		},
		{
			fixtureName: "low_data_week",
			extra: func(t *testing.T, fx fixture, brief *model.GroupWeeklyBrief) {
				// Specifically: brief IS generated despite sparse input — the
				// orchestrator must NOT 422 because one source (meeting)
				// exists, even though its summary is whitespace-only.
				require.Equalf(t, model.GroupWeeklyBriefStateGenerated, brief.State,
					"[%s] low-data brief must reach 'generated' state, got %q", fx.Name, brief.State)
			},
		},
		{
			fixtureName: "prompt_injection",
			extra: func(t *testing.T, _ fixture, brief *model.GroupWeeklyBrief) {
				assertPromptInjectionContained(t, brief)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.fixtureName, func(t *testing.T) {
			fx := loadFixture(t, tc.fixtureName)
			g, _ := buildOrchestrator(fx, ai.NewFakeAdapter())

			out, err := g.Generate(context.Background(), service.GroupWeeklyBriefGenerateInput{
				CommitteeUID:  fx.CommitteeUID,
				CommitteeName: fx.CommitteeName,
				ProjectName:   fx.ProjectName,
				Now:           fx.Now,
			})
			require.NoErrorf(t, err, "[%s] orchestrator returned error", fx.Name)
			require.NotNil(t, out)

			assertCommonBriefShape(t, fx, out.Brief)
			if tc.extra != nil {
				tc.extra(t, fx, out.Brief)
			}
		})
	}
}

// TestWeeklyBriefEvalFake_WindowMatchesFixture sanity-checks that the
// orchestrator picks the same Sun→Sat window the fixture documents, so future
// fixture authors don't drift relative to model.WeeklyWindow().
func TestWeeklyBriefEvalFake_WindowMatchesFixture(t *testing.T) {
	for _, name := range []string{"normal_week", "low_data_week", "prompt_injection"} {
		name := name
		t.Run(name, func(t *testing.T) {
			fx := loadFixture(t, name)
			ws, we := model.WeeklyWindow(fx.Now)
			require.Truef(t, ws.Equal(fx.WindowStart),
				"[%s] window_start mismatch: fixture=%s computed=%s", name, fx.WindowStart, ws)
			// model.WeeklyWindow returns end at 23:59:59.999999999, while the
			// fixture pins to 23:59:59 for readability — compare to second.
			require.Equalf(t, fx.WindowEnd.Truncate(time.Second), we.Truncate(time.Second),
				"[%s] window_end mismatch: fixture=%s computed=%s", name, fx.WindowEnd, we)
		})
	}
}
