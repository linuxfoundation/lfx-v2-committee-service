// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

type v1AuditHistorySubcommand struct{ output io.Writer }

func (*v1AuditHistorySubcommand) Name() string { return "v1-audit-history" }

func (*v1AuditHistorySubcommand) Help() string {
	return "preview historical V1 audit mapping from local NDJSON only (always dry-run; no infrastructure)"
}

func (*v1AuditHistorySubcommand) RunsOffline(_ []string) bool { return true }

func (s *v1AuditHistorySubcommand) Run(ctx context.Context, rc commands.RunContext) (runErr error) {
	out := s.output
	if out == nil {
		out = os.Stdout
	}
	fs := flag.NewFlagSet(s.Name(), flag.ContinueOnError)
	// Parser and filesystem errors can contain private paths or flag values.
	// Return fixed diagnostics instead of copying them into the main log sink.
	fs.SetOutput(io.Discard)
	inputPath := fs.String("input", "", "raw platform.committee_audit_log NDJSON file")
	source := fs.String("source", "", "stable environment/source namespace, such as dev-v1")
	mappingPath := fs.String("mappings", "", "offline JSON mapping/revision manifest; absent means inventory only")
	committeeID := fs.String("committee-id", "", "limit to one legacy committee record ID")
	limit := fs.Int("max-records", 10000, "maximum nonempty records read (1..100000)")
	dryRun := fs.Bool("dry-run", true, "must remain true; publishing is not implemented")
	planPath := fs.String("plan-output", "", "optional NEW private draft NDJSON file (contains personal audit data; mode 0600)")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprintln(out, "usage: committee-cli report v1-audit-history --input <file> --source <namespace> [flags]")
			fs.SetOutput(out)
			fs.PrintDefaults()
			return nil
		}
		return errs.NewValidation("invalid offline audit preview arguments")
	}
	if !*dryRun || *inputPath == "" || *source == "" || *limit < 1 || *limit > 100000 || fs.NArg() != 0 {
		return errs.NewValidation("offline audit preview requires input, source and dry-run mode")
	}
	input, err := openV1AuditFile(ctx, *inputPath)
	if ctx.Err() != nil {
		if input != nil {
			_ = input.Close()
		}
		return errs.NewUnexpected("audit preview canceled", ctx.Err())
	}
	if err != nil {
		return errs.NewValidation("cannot open audit input")
	}
	inputReader, stopInput := cancelableV1AuditReader(ctx, input)
	defer stopInput()
	options := service.V1AuditOptions{Source: *source, CommitteeID: *committeeID, MaxRecords: *limit}
	if *mappingPath != "" {
		mapping, err := openV1AuditFile(ctx, *mappingPath)
		if ctx.Err() != nil {
			if mapping != nil {
				_ = mapping.Close()
			}
			return errs.NewUnexpected("audit preview canceled", ctx.Err())
		}
		if err != nil {
			return errs.NewValidation("cannot open audit mapping manifest")
		}
		mappingReader, stopMapping := cancelableV1AuditReader(ctx, mapping)
		options.Mappings, err = service.DecodeV1AuditMappings(mappingReader)
		stopMapping()
		if ctx.Err() != nil {
			return errs.NewUnexpected("audit preview canceled", ctx.Err())
		}
		if err != nil {
			return err
		}
	}
	var emit func(model.V1AuditPlanEvent) error
	var plan *os.File
	keepPlan := false
	if *planPath != "" {
		plan, err = os.OpenFile(*planPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errs.NewValidation("cannot create new private audit draft output")
		}
		defer func() {
			_ = plan.Close()
			if !keepPlan {
				if err := os.Remove(*planPath); err != nil && !os.IsNotExist(err) {
					runErr = errs.NewUnexpected("cannot remove incomplete private audit draft output")
				}
			}
		}()
		encoder := json.NewEncoder(plan)
		emit = func(event model.V1AuditPlanEvent) error { return encoder.Encode(event) }
	}
	report, err := service.PlanV1AuditHistory(ctx, inputReader, options, emit)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if encoder.Encode(report) != nil {
		return errs.NewUnexpected("cannot write audit preview summary")
	}
	if report.Invalid > 0 {
		return errs.NewValidation("audit preview contains invalid records; private draft output discarded")
	}
	if plan != nil {
		if plan.Sync() != nil || plan.Close() != nil {
			return errs.NewUnexpected("cannot finish private audit draft output")
		}
	}
	// A signal during the summary write or sync still discards the draft.
	if ctx.Err() != nil {
		return errs.NewUnexpected("audit preview canceled", ctx.Err())
	}
	keepPlan = plan != nil
	return nil
}

// cancelableV1AuditReader returns a reader whose pending Read ends as soon as ctx
// is canceled, so cancellation always returns through the draft cleanup. Closing
// the file is not enough on its own: a FIFO on macOS is not pollable, and closing
// it does not interrupt a blocked read. A goroutine copies the file into a pipe
// instead, and cancellation closes the pipe. The returned stop function releases
// the pipe and closes the file; a copy still blocked in a read ends with it or
// when the process exits.
func cancelableV1AuditReader(ctx context.Context, file io.ReadCloser) (io.Reader, func()) {
	reader, writer := io.Pipe()
	go func() {
		_, err := io.Copy(writer, file)
		_ = writer.CloseWithError(err)
	}()
	stopCancel := context.AfterFunc(ctx, func() {
		// Closing the write side makes a pending Read return the cancellation.
		_ = writer.CloseWithError(ctx.Err())
		_ = file.Close()
	})
	return reader, func() {
		stopCancel()
		_ = reader.Close()
		_ = file.Close()
	}
}

// openV1AuditFile opens path for reading unless ctx is canceled first. Opening a
// FIFO blocks until a writer connects, which closing cannot interrupt, so the open
// runs in the background; a file opened after cancellation is closed there.
func openV1AuditFile(ctx context.Context, path string) (*os.File, error) {
	type result struct {
		file *os.File
		err  error
	}
	done := make(chan result, 1)
	go func() {
		file, err := os.Open(path)
		done <- result{file, err}
	}()
	select {
	case r := <-done:
		return r.file, r.err
	case <-ctx.Done():
		go func() {
			if r := <-done; r.file != nil {
				_ = r.file.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

var _ commands.OfflineSubcommand = (*v1AuditHistorySubcommand)(nil)
