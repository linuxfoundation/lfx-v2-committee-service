// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
)

const v1PreviewRow = `{"id":"00000000-0000-4000-8000-000000000010","record_id":"committee-1","record_type":"COMMITTEE","action":"INSERT","created_at":"2025-03-17T21:27:04.533187Z","created_by":"actor-1","created_by_username":"first-last","new_record":{"sfid":"committee-1","project_name__c":"project-1","mailing_list__c":"Committee example","chat_webhook_url":"private-marker"}}`

const v1PreviewMappings = `{"version":1,"source":"fixture-v1","projects":{"project-1":"00000000-0000-4000-8000-000000000002"},"committees":{"committee-1":"00000000-0000-4000-8000-000000000001"},"revisions":{"00000000-0000-4000-8000-000000000010/committee":1}}`

func TestV1AuditHistorySubcommand_Run(t *testing.T) {
	for _, scenario := range []string{
		"inventory", "private draft", "no overwrite", "invalid discards draft", "canceled discards draft", "not dry run",
		"canceled before input writer connects", "canceled before mappings writer connects", "canceled during mappings read",
		"canceled during summary", "private path error", "help", "unknown flag", "malformed max records", "extra argument",
	} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			input, mapping, plan := filepath.Join(dir, "input.ndjson"), filepath.Join(dir, "mapping.json"), filepath.Join(dir, "draft.ndjson")
			data := v1PreviewRow
			if scenario == "invalid discards draft" {
				data += "\n{}"
			}
			require.NoError(t, os.WriteFile(input, []byte(data), 0600))
			require.NoError(t, os.WriteFile(mapping, []byte(v1PreviewMappings), 0600))
			args := []string{"--input", input, "--source", "fixture-v1"}
			if scenario != "inventory" {
				args = append(args, "--mappings", mapping, "--plan-output", plan)
			}
			switch scenario {
			case "no overwrite":
				require.NoError(t, os.WriteFile(plan, []byte("existing-private-marker"), 0600))
			case "not dry run":
				args = append(args, "--dry-run=false")
			case "private path error":
				args = []string{"--input", filepath.Join(dir, "private-marker"), "--source", "fixture-v1"}
			case "help":
				args = []string{"--help"}
			case "unknown flag":
				args = []string{"--unknown", "private-marker"}
			case "malformed max records":
				args = []string{"--max-records=private-marker"}
			case "extra argument":
				args = []string{"private-marker"}
			case "canceled before input writer connects", "canceled before mappings writer connects":
				// Opening a FIFO with no writer blocks in the kernel.
				fifo := filepath.Join(dir, "fifo")
				makeV1AuditFIFO(t, fifo)
				target := input
				if scenario == "canceled before mappings writer connects" {
					target = mapping
				}
				args[slices.Index(args, target)] = fifo
				// If the open ignored cancellation, connecting a writer ends it instead
				// of hanging, and the assertion below fails.
				time.AfterFunc(5*time.Second, func() {
					if writer, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
						_ = writer.Close()
					}
				})
			case "canceled discards draft", "canceled during mappings read":
				if runtime.GOOS == "windows" {
					t.Skip("needs /dev/fd to pass a blocking pipe as a file path")
				}
				reader, writer, pipeErr := os.Pipe()
				require.NoError(t, pipeErr)
				t.Cleanup(func() { _, _ = reader.Close(), writer.Close() })
				// With the writer kept open, the read blocks after this content: a
				// complete input row (written to the draft first) or a partial manifest.
				target, content := input, v1PreviewRow+"\n"
				if scenario == "canceled during mappings read" {
					target, content = mapping, `{"version":1,`
				}
				_, pipeErr = writer.WriteString(content)
				require.NoError(t, pipeErr)
				args[slices.Index(args, target)] = fmt.Sprintf("/dev/fd/%d", reader.Fd())
				// If cancellation did not unblock the read, closing the writer ends it
				// with a read error instead of hanging, and the assertion below fails.
				time.AfterFunc(5*time.Second, func() { _ = writer.Close() })
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "canceled discards draft", "canceled before input writer connects", "canceled before mappings writer connects",
				"canceled during mappings read":
				time.AfterFunc(50*time.Millisecond, cancel)
			}
			var output bytes.Buffer
			command := &v1AuditHistorySubcommand{output: &output}
			if scenario == "canceled during summary" {
				command.output = &v1CancelingWriter{Writer: &output, cancel: cancel}
			}
			require.True(t, command.RunsOffline(args))
			// No NATS, database, writer, directory or publisher is provided.
			started := time.Now()
			err := command.Run(ctx, commands.RunContext{Args: args})
			// Cancellation must end the run promptly, not after the 5-second safety
			// unblock of a pipe or FIFO.
			require.Less(t, time.Since(started), 2*time.Second)
			switch scenario {
			case "inventory":
				require.NoError(t, err)
				require.Contains(t, output.String(), `"missing_committee_mapping": 1`)
			case "private draft":
				require.NoError(t, err)
				info, statErr := os.Stat(plan)
				require.NoError(t, statErr)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())
				content, readErr := os.ReadFile(plan)
				require.NoError(t, readErr)
				require.Contains(t, string(content), `"actor_ref":"user:first-last"`)
				require.Contains(t, string(content), `"schema":"committee.legacy.v1"`)
				require.NotContains(t, string(content), "private-marker")
			case "no overwrite":
				require.Error(t, err)
				content, readErr := os.ReadFile(plan)
				require.NoError(t, readErr)
				require.Equal(t, "existing-private-marker", string(content))
			case "help":
				require.NoError(t, err)
				require.Contains(t, output.String(), "dry-run")
			case "canceled discards draft", "canceled before input writer connects", "canceled before mappings writer connects",
				"canceled during mappings read", "canceled during summary":
				require.ErrorContains(t, err, "audit preview canceled")
				_, statErr := os.Stat(plan)
				require.True(t, os.IsNotExist(statErr), "a canceled run must discard the draft")
			case "invalid discards draft":
				// The valid first row was written to the draft before it was discarded.
				require.Error(t, err)
				require.Contains(t, output.String(), `"events": 1`)
				require.Contains(t, output.String(), `"invalid": 1`)
				_, statErr := os.Stat(plan)
				require.True(t, os.IsNotExist(statErr))
			default:
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-marker")
				_, statErr := os.Stat(plan)
				require.True(t, os.IsNotExist(statErr))
			}
			for _, private := range []string{"first-last", "Committee example", "private-marker", "actor-1"} {
				require.NotContains(t, output.String(), private)
			}
		})
	}
}

// v1CancelingWriter cancels the run while the summary is written, then succeeds.
type v1CancelingWriter struct {
	io.Writer
	cancel context.CancelFunc
}

func (w *v1CancelingWriter) Write(p []byte) (int, error) {
	w.cancel()
	return w.Writer.Write(p)
}

func TestCancelableV1AuditReader(t *testing.T) {
	t.Run("passes input through", func(t *testing.T) {
		reader, stop := cancelableV1AuditReader(context.Background(), io.NopCloser(strings.NewReader(v1PreviewRow)))
		defer stop()
		content, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.Equal(t, v1PreviewRow, string(content))
	})
	t.Run("cancellation ends a read that closing cannot interrupt", func(t *testing.T) {
		// Like a FIFO on macOS: Read stays blocked after Close.
		source := &v1StuckReader{release: make(chan struct{})}
		t.Cleanup(func() { close(source.release) })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reader, stop := cancelableV1AuditReader(ctx, source)
		defer stop()
		time.AfterFunc(50*time.Millisecond, cancel)
		done := make(chan error, 1)
		go func() {
			_, err := reader.Read(make([]byte, 1))
			done <- err
		}()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
			// The source is closed after the pipe, so wait for it rather than
			// assuming the released read also orders the close.
			require.Eventually(t, source.closed.Load, time.Second, 5*time.Millisecond, "cancellation also closes the source")
		case <-time.After(2 * time.Second):
			t.Fatal("a canceled read must return even when the source ignores Close")
		}
	})
}

// v1StuckReader blocks every Read until released, and Close does not unblock it.
type v1StuckReader struct {
	release chan struct{}
	closed  atomic.Bool
}

func (r *v1StuckReader) Read([]byte) (int, error) {
	<-r.release
	return 0, io.EOF
}

func (r *v1StuckReader) Close() error {
	r.closed.Store(true)
	return nil
}
