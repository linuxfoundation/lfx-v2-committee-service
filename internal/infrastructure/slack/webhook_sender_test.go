// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withTestAllowlist swaps the package-level host allowlist to admit the
// httptest server for the duration of the test, restoring on cleanup.
func withTestAllowlist(t *testing.T, srv *httptest.Server) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	orig := allowedHosts
	allowedHosts = map[string]bool{u.Hostname(): true}
	t.Cleanup(func() { allowedHosts = orig })
}

// captureBodyServer runs an httptest.NewTLSServer that records the last
// request body it received and returns 200 OK.
func captureBodyServer(t *testing.T) (*httptest.Server, *[]byte) {
	t.Helper()
	var captured []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		captured = b
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

// TestWebhookSender_Send exercises WebhookSender.Send against a stubbed
// Slack Incoming Webhook and against the URL allowlist checks.
//
// Slack mrkdwn escape spec:
// https://api.slack.com/reference/surfaces/formatting#escaping
// & → &amp;, < → &lt;, > → &gt;. No other characters are escaped.
func TestWebhookSender_Send(t *testing.T) {
	tests := []struct {
		name string
		text string
		// overrideURL, when non-empty, is used instead of the test server
		// URL and no capturing server is spun up. Used to cover the
		// scheme / host allowlist rejection branches.
		overrideURL string
		wantPosted  string
		wantErr     bool
		// extraAssert runs after wantPosted is checked, for cases that
		// need a second assertion on the delivered payload.
		extraAssert func(t *testing.T, posted string)
	}{
		{
			// An unescaped `<!channel>` in brief text would page the
			// entire Slack channel; escaping the angle brackets prevents
			// Slack from parsing it as a broadcast token.
			name:       "escapes @channel broadcast token",
			text:       "Heads up <!channel> please read",
			wantPosted: "Heads up &lt;!channel&gt; please read",
			extraAssert: func(t *testing.T, posted string) {
				assert.NotContains(t, posted, "<!channel>")
			},
		},
		{
			name:       "escapes @here broadcast token",
			text:       "<!here>",
			wantPosted: "&lt;!here&gt;",
		},
		{
			// An unescaped `<https://evil.example|Click here>` would
			// render as a deceptive hyperlink in Slack. Escaping the
			// angle brackets keeps the URL visible as text.
			name:       "escapes deceptive link markup",
			text:       "<https://evil.example|Click here>",
			wantPosted: "&lt;https://evil.example|Click here&gt;",
			extraAssert: func(t *testing.T, posted string) {
				// The literal `<…|…>` link markup must not survive.
				assert.False(t, strings.HasPrefix(posted, "<https://"))
				assert.False(t, strings.HasSuffix(posted, ">"))
			},
		},
		{
			// `&` must be escaped first so that `<` → `&lt;` and
			// `>` → `&gt;` are not double-encoded on their own leading
			// ampersand.
			name:       "escapes ampersand before angle brackets",
			text:       "A & B < C > D",
			wantPosted: "A &amp; B &lt; C &gt; D",
		},
		{
			// Slack's escape spec covers only & < > . Over-escaping quotes
			// (as html.EscapeString does) would render as literal
			// `&quot;` / `&#39;` in Slack. Guard against a future refactor
			// that swaps in the wrong helper.
			name:       "leaves quotes and apostrophes literal",
			text:       `Alice said "hello" — it's fine`,
			wantPosted: `Alice said "hello" — it's fine`,
		},
		{
			// Text with no control chars must be delivered byte-for-byte.
			name:       "plain text round trips unchanged",
			text:       "## Weekly brief\n\nHello, Slack!",
			wantPosted: "## Weekly brief\n\nHello, Slack!",
		},
		{
			name:        "rejects non-https scheme",
			overrideURL: "http://hooks.slack.com/services/T/B/x",
			wantErr:     true,
		},
		{
			name:        "rejects unknown host",
			overrideURL: "https://evil.example/services/T/B/x",
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.overrideURL != "" {
				// Allowlist / scheme rejection branch — no server needed.
				sender := NewWebhookSender(nil)
				err := sender.Send(context.Background(), tc.overrideURL, "irrelevant")
				require.Error(t, err)
				return
			}

			srv, captured := captureBodyServer(t)
			withTestAllowlist(t, srv)

			sender := NewWebhookSender(srv.Client())
			err := sender.Send(context.Background(), srv.URL, tc.text)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			var p slackPayload
			require.NoError(t, json.Unmarshal(*captured, &p))
			assert.Equal(t, tc.wantPosted, p.Text)
			if tc.extraAssert != nil {
				tc.extraAssert(t, p.Text)
			}
		})
	}
}
