// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	committeeservicesvr "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	goahttp "goa.design/goa/v3/http"
)

func TestRequestBodyLimitMiddleware(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		path   string
		size   int
		status int
	}{
		{"empty body", http.MethodPost, "/committees/example/join", 0, http.StatusOK},
		{"JSON at limit", http.MethodPost, "/committees", maxRequestBodyBytes, http.StatusOK},
		{"join above limit", http.MethodPost, "/committees/example/join", maxRequestBodyBytes + 1, http.StatusRequestEntityTooLarge},
		{"application above limit", http.MethodPost, "/committees/example/applications", maxRequestBodyBytes + 1, http.StatusRequestEntityTooLarge},
		{"update above limit", http.MethodPut, "/committees/example", maxRequestBodyBytes + 1, http.StatusRequestEntityTooLarge},
		{"GET above limit", http.MethodGet, "/healthz", maxRequestBodyBytes + 1, http.StatusRequestEntityTooLarge},
		{"unknown route above limit", http.MethodPost, "/unknown", maxRequestBodyBytes + 1, http.StatusRequestEntityTooLarge},
		{"upload at limit", http.MethodPost, "/committees/example/documents", maxUploadBodyBytes, http.StatusOK},
		{"upload above limit", http.MethodPost, "/committees/example/documents", maxUploadBodyBytes + 1, http.StatusRequestEntityTooLarge},
		{"non-upload document route", http.MethodGet, "/committees/example/documents", maxRequestBodyBytes + 1, http.StatusRequestEntityTooLarge},
		{"document subroute", http.MethodPost, "/committees/example/documents/example", maxRequestBodyBytes + 1, http.StatusRequestEntityTooLarge},
	} {
		for _, unknownLength := range []bool{false, true} {
			name := "known length"
			if unknownLength {
				name = "unknown length"
			}
			t.Run(tt.name+"/"+name, func(t *testing.T) {
				body := strings.Repeat("x", tt.size)
				req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(body))
				if unknownLength {
					req.ContentLength = -1
					req.TransferEncoding = []string{"chunked"}
				}
				called := false
				rec := httptest.NewRecorder()
				requestBodyLimitMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					got, err := io.ReadAll(r.Body)
					if err != nil || string(got) != body {
						t.Fatalf("body not preserved: read %d bytes, error %v", len(got), err)
					}
				})).ServeHTTP(rec, req)
				if rec.Code != tt.status || called != (tt.status == http.StatusOK) {
					t.Fatalf("status = %d, handler called = %t; want %d", rec.Code, called, tt.status)
				}
			})
		}
	}

	t.Run("rejects read errors before decoding", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/committees", nil)
		req.Body = io.NopCloser(iotest.ErrReader(errors.New("read failed")))
		rec := httptest.NewRecorder()
		requestBodyLimitMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not be called")
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}

func TestRequestBodyLimitJoinTransport(t *testing.T) {
	for _, prefix := range []string{`{"organization":{"name":"`, `{} `} {
		t.Run(prefix, func(t *testing.T) {
			// Model a huge streaming request without allocating the attack body.
			body := &countingBody{Reader: io.MultiReader(strings.NewReader(prefix), io.LimitReader(spaceReader{}, 1<<30))}
			req := httptest.NewRequest(http.MethodPost, "/committees/7cad5a8d-19d0-41a4-81a6-043453daf9ee/join?v=1", body)
			req.Header.Set("Content-Type", "application/json")
			mux := goahttp.NewMuxer()
			committeeservicesvr.MountJoinCommitteeHandler(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = committeeservicesvr.DecodeJoinCommitteeRequest(mux, goahttp.RequestDecoder)(r)
				t.Fatal("oversized request reached the decoder")
			}))
			rec := httptest.NewRecorder()
			requestBodyLimitMiddleware()(mux).ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("expected 413, got %d", rec.Code)
			}
			if body.read != maxRequestBodyBytes+1 || body.closed {
				t.Fatalf("read %d bytes, closed = %t; want %d bytes and deferred body cleanup", body.read, body.closed, maxRequestBodyBytes+1)
			}
		})
	}
}

func TestRequestBodyLimitDocumentUpload(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", "Example document"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "example.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(part, spaceReader{}, model.MaxDocumentFileSize); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/committees/example/documents", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	requestBodyLimitMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		var payload *committeeservice.UploadCommitteeDocumentPayload
		if err := uploadCommitteeDocumentDecoder(reader, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.File) != model.MaxDocumentFileSize {
			t.Fatalf("decoded file size = %d", len(payload.File))
		}
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRequestBodyLimitPromptRejection(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		name := "known length"
		if chunked {
			name = "chunked"
		}
		t.Run(name, func(t *testing.T) {
			handler := requestBodyLimitMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("oversized request reached the handler")
			}))
			// Match production's ResponseWriter wrapper: MaxBytesReader cannot rely
			// on net/http's private requestTooLarge method passing through it.
			srv := httptest.NewUnstartedServer(otelhttp.NewHandler(handler, "test"))
			srv.Config.ReadTimeout = 10 * time.Second
			srv.Start()
			defer srv.Close()
			conn, err := net.Dial("tcp", srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			// Require rejection well before the server's body-read deadline.
			if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			headers := "Content-Length: 1073741824\r\n"
			if chunked {
				headers = "Transfer-Encoding: chunked\r\n"
			}
			if _, err := fmt.Fprintf(conn, "POST /committees/example/join?v=1 HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n%s\r\n", headers); err != nil {
				t.Fatal(err)
			}
			if chunked {
				if _, err := fmt.Fprintf(conn, "%x\r\n", maxRequestBodyBytes+1); err != nil {
					t.Fatal(err)
				}
				if _, err := io.CopyN(conn, spaceReader{}, maxRequestBodyBytes+1); err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(conn, "\r\n"); err != nil {
					t.Fatal(err)
				}
			}
			// Deliberately withhold the rest of the body (including the zero
			// chunk). Neither rejection path should drain it before replying.
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("expected prompt 413 without finishing the request body: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusRequestEntityTooLarge || !resp.Close {
				t.Fatalf("status = %d, close = %t; want 413 and connection closure", resp.StatusCode, resp.Close)
			}
		})
	}
}

type spaceReader struct{}

func (spaceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

type countingBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *countingBody) Close() error {
	b.closed = true
	return nil
}

func TestAcceptInviteEmptyBodyMiddleware(t *testing.T) {
	committeeUID := "7cad5a8d-19d0-41a4-81a6-043453daf9ee"
	inviteUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	acceptPath := "/committees/" + committeeUID + "/invites/" + inviteUID + "/accept"

	t.Run("injects empty JSON for missing accept body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, acceptPath+"?v=1", nil)
		body := serveThroughMiddleware(t, req)
		if body != "{}" {
			t.Fatalf("expected injected body {}, got %q", body)
		}
	})

	t.Run("preserves provided accept body", func(t *testing.T) {
		want := `{"organization":{"name":"LF"}}`
		req := httptest.NewRequest(http.MethodPost, acceptPath+"?v=1", strings.NewReader(want))
		body := serveThroughMiddleware(t, req)
		if body != want {
			t.Fatalf("expected body preserved, got %q", body)
		}
	})

	t.Run("ignores decline path", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodPost,
			"/committees/"+committeeUID+"/invites/"+inviteUID+"/decline?v=1",
			nil,
		)
		body := serveThroughMiddleware(t, req)
		if body != "" {
			t.Fatalf("expected decline path left unchanged, got %q", body)
		}
	})

	t.Run("rejects oversized accept body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, acceptPath+"?v=1", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
		rec := httptest.NewRecorder()
		requestBodyLimitMiddleware()(acceptInviteEmptyBodyMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not be called")
		}))).ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", rec.Code)
		}
	})
}

// TestJoinCommitteeBodylessTransport pins the wire contract at the transport layer:
// raw HTTP clients (e.g. the Self Serve UI) may omit the join body entirely, and the
// generated server decoder must accept the empty body (io.EOF) and produce a payload
// carrying no organization. Goa-generated Go clients and the generated CLI cannot omit
// the body (they must send at least "{}") — an upstream Goa codegen limitation
// documented on JoinCommitteeOptionalBody — but the server side accepts omission.
func TestJoinCommitteeBodylessTransport(t *testing.T) {
	committeeUID := "7cad5a8d-19d0-41a4-81a6-043453daf9ee"
	joinPath := "/committees/" + committeeUID + "/join"

	decode := func(t *testing.T, req *http.Request) (*committeeservice.JoinCommitteePayload, error) {
		t.Helper()
		var (
			payload *committeeservice.JoinCommitteePayload
			decErr  error
		)
		mux := goahttp.NewMuxer()
		committeeservicesvr.MountJoinCommitteeHandler(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			payload, decErr = committeeservicesvr.DecodeJoinCommitteeRequest(mux, goahttp.RequestDecoder)(r)
		}))
		requestBodyLimitMiddleware()(mux).ServeHTTP(httptest.NewRecorder(), req)
		return payload, decErr
	}

	t.Run("decodes a bodyless join request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, joinPath+"?v=1", nil)
		payload, err := decode(t, req)
		if err != nil {
			t.Fatalf("expected bodyless join request to decode, got error %v", err)
		}
		if payload == nil {
			t.Fatal("expected a decoded payload, got nil")
		}
		if payload.UID != committeeUID {
			t.Fatalf("expected committee uid %q, got %q", committeeUID, payload.UID)
		}
		if payload.Body != nil && payload.Body.Organization != nil {
			t.Fatalf("expected no organization on a bodyless join, got %+v", payload.Body.Organization)
		}
	})

	t.Run("decodes a join request carrying an organization body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, joinPath+"?v=1",
			strings.NewReader(`{"organization":{"name":"Example Org","website":"https://example.com"}}`))
		req.Header.Set("Content-Type", "application/json")
		payload, err := decode(t, req)
		if err != nil {
			t.Fatalf("expected join request with body to decode, got error %v", err)
		}
		if payload == nil || payload.Body == nil || payload.Body.Organization == nil {
			t.Fatalf("expected organization on the decoded payload, got %+v", payload)
		}
		if got := *payload.Body.Organization.Name; got != "Example Org" {
			t.Fatalf("expected organization name %q, got %q", "Example Org", got)
		}
		if got := *payload.Body.Organization.Website; got != "https://example.com" {
			t.Fatalf("expected organization website %q, got %q", "https://example.com", got)
		}
	})
}

func serveThroughMiddleware(t *testing.T, req *http.Request) string {
	var captured string
	rec := httptest.NewRecorder()
	requestBodyLimitMiddleware()(acceptInviteEmptyBodyMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
		captured = string(bodyBytes)
	}))).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected middleware to succeed, got status %d body %q", rec.Code, rec.Body.String())
	}
	return captured
}
