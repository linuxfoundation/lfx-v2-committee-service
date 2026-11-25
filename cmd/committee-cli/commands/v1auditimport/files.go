// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package v1auditimport holds the file handling shared by the v1 audit history
// import commands: the frozen events file, the state journal and its lock, and
// the dispatch credential file. Errors are fixed strings and never echo paths
// or file content.
package v1auditimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// maxAuthorizationBytes keeps the dispatch credential inside the allowance the
// frozen events reserve for it.
const maxAuthorizationBytes = 60 << 10

// Events is a validated frozen events file. OpenEvents takes a private copy of
// it, so the bytes validated and hashed are exactly the bytes later sent or
// reconciled, whatever happens to the original file during a run.
type Events struct {
	copy     *os.File
	copyPath string // set only while the copy still has a name to remove
	SHA256   string
	Count    int
}

// OpenEvents copies a frozen events file (--plan-output of report
// v1-audit-history) into a private temporary file (mode 0600, unlinked at once
// where the platform allows) and validates and hashes that copy.
func OpenEvents(ctx context.Context, path string) (*Events, error) {
	if err := requireRegular(path); err != nil {
		return nil, errs.NewValidation("import events file must be a regular file")
	}
	source, err := os.Open(path)
	if err != nil {
		return nil, errs.NewValidation("cannot open import events file")
	}
	defer func() { _ = source.Close() }()
	if info, err := source.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, errs.NewValidation("import events file must be a regular file")
	}
	copied, err := os.CreateTemp("", "committee-v1-audit-import-*")
	if err != nil {
		return nil, errs.NewUnexpected("cannot create private copy of import events")
	}
	events := &Events{copy: copied, copyPath: copied.Name()}
	// The copy holds personal audit data: drop its name immediately, so only
	// this process can reach it and it disappears when the process ends.
	if os.Remove(events.copyPath) == nil {
		events.copyPath = ""
	}
	// Validate and copy in one pass: the copy holds exactly the bytes inspected
	// and hashed, and a bad line or cancellation stops the copy there.
	writeFailed := false
	sink := writerFunc(func(p []byte) (int, error) {
		n, err := copied.Write(p)
		if err != nil {
			writeFailed = true
		}
		return n, err
	})
	inspected, err := service.InspectV1AuditImportEvents(ctx, io.TeeReader(source, sink))
	if writeFailed {
		events.Close()
		return nil, errs.NewUnexpected("cannot copy import events file")
	}
	if err != nil {
		events.Close()
		return nil, err
	}
	if copied.Sync() != nil {
		events.Close()
		return nil, errs.NewUnexpected("cannot copy import events file")
	}
	events.SHA256, events.Count = inspected.SHA256, inspected.Events
	return events, nil
}

// writerFunc adapts a function to io.Writer.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Scan rewinds the private copy and passes it to fn. As a backstop it then
// fails if the bytes read differ from those validated.
func (e *Events) Scan(fn func(io.Reader) error) error {
	if _, err := e.copy.Seek(0, io.SeekStart); err != nil {
		return errs.NewUnexpected("cannot read private copy of import events")
	}
	hash := sha256.New()
	reader := io.TeeReader(e.copy, hash)
	if err := fn(reader); err != nil {
		return err
	}
	if _, err := io.Copy(hash, e.copy); err != nil {
		return errs.NewUnexpected("cannot read private copy of import events")
	}
	if hex.EncodeToString(hash.Sum(nil)) != e.SHA256 {
		return errs.NewValidation("private copy of import events changed while it was in use")
	}
	return nil
}

// Close closes and, where it still has a name, removes the private copy.
func (e *Events) Close() {
	_ = e.copy.Close()
	if e.copyPath != "" {
		_ = os.Remove(e.copyPath)
	}
}

// Journal is the append-only state journal of one import, bound to its events
// file and locked so only one command uses it at a time.
type Journal struct {
	State    *service.V1AuditImportState
	file     *os.File
	writable bool
	release  func()
}

// OpenJournal locks and replays the journal at path. A missing journal is
// created (mode 0600) only when create is set; otherwise an empty state is used
// and nothing is written. The journal must belong to the events file.
//
// On Unix the lock is an advisory lock on the journal file itself (exclusive to
// write, shared to preview), so every path or link to the file shares it, and
// the system releases it when the process ends. Elsewhere it is a <path>.lock
// file, which a killed run leaves behind.
func OpenJournal(path string, events *Events, create bool) (*Journal, error) {
	if err := requireRegular(path); err != nil {
		return nil, errs.NewValidation("import state journal must be a regular file")
	}
	journal := &Journal{writable: create, release: func() {}}
	fail := func(err error) (*Journal, error) {
		journal.Close()
		return nil, err
	}

	// A preview only reads the journal, so it opens it read-only.
	flags := os.O_RDONLY
	if create {
		flags = os.O_RDWR | os.O_APPEND
	}
	file, err := os.OpenFile(path, flags, 0)
	switch {
	case errors.Is(err, os.ErrNotExist) && !create:
		journal.State = service.NewV1AuditImportState(events.SHA256)
		return journal, nil
	case errors.Is(err, os.ErrNotExist):
		return createJournal(journal, path, events)
	case err != nil:
		return nil, errs.NewValidation("cannot open import state journal")
	}
	journal.file = file
	release, err := lockJournal(path, file, create)
	if err != nil {
		return fail(errs.NewValidation("import state journal is in use by another run (or, outside Unix, its lock file is stale)"))
	}
	journal.release = release

	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fail(errs.NewValidation("import state journal must be a regular file readable only by its owner"))
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return fail(errs.NewUnexpected("cannot read import state journal"))
	}
	state, err := service.ReadV1AuditImportState(bytes.NewReader(data))
	if err != nil {
		return fail(err)
	}
	if state.EventsSHA256 != events.SHA256 {
		return fail(errs.NewValidation("import state journal belongs to a different events file"))
	}
	if create && len(data) > 0 && data[len(data)-1] != '\n' {
		// Drop an interrupted final append so the next record starts a line.
		if file.Truncate(int64(bytes.LastIndexByte(data, '\n')+1)) != nil || file.Sync() != nil {
			return fail(errs.NewUnexpected("cannot repair import state journal"))
		}
	}
	journal.State = state
	return journal, nil
}

// createJournal builds a new journal under a temporary name in the same
// directory: header written and synced, exclusive lock held. Only then is it
// linked to its final name, which fails if the name already exists, so another
// run never sees a journal without its header and two runs cannot both create
// one.
func createJournal(journal *Journal, path string, events *Events) (*Journal, error) {
	fail := func(err error) (*Journal, error) {
		journal.Close()
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".new-*")
	if err != nil {
		return nil, errs.NewValidation("cannot create import state journal")
	}
	temp := file.Name()
	defer func() { _ = os.Remove(temp) }()
	journal.file = file
	header, err := service.V1AuditImportJournalHeader(events.SHA256)
	if err != nil {
		return fail(err)
	}
	if _, err := file.Write(header); err != nil || file.Sync() != nil {
		return fail(errs.NewUnexpected("cannot write import state journal"))
	}
	release, err := lockJournal(path, file, true)
	if err != nil {
		return fail(errs.NewValidation("import state journal is in use by another run (or, outside Unix, its lock file is stale)"))
	}
	journal.release = release
	if err := os.Link(temp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fail(errs.NewValidation("import state journal was created by another run; rerun"))
		}
		return fail(errs.NewUnexpected("cannot create import state journal"))
	}
	// Make the new name durable before anything is published and journaled.
	if syncDir(filepath.Dir(path)) != nil {
		return fail(errs.NewUnexpected("cannot sync import state journal directory"))
	}
	// O_APPEND is not needed: the temporary file is new and every write follows
	// the header at the end of the file.
	journal.State = service.NewV1AuditImportState(events.SHA256)
	return journal, nil
}

// Record appends one record and syncs it before the state is updated.
func (j *Journal) Record(record service.V1AuditImportRecord) error {
	if j.file == nil || !j.writable {
		return errs.NewUnexpected("import state journal is read-only in this run")
	}
	line, err := json.Marshal(record)
	if err != nil {
		return errs.NewUnexpected("cannot encode import state record")
	}
	if _, err := j.file.Write(append(line, '\n')); err != nil || j.file.Sync() != nil {
		return errs.NewUnexpected("cannot write import state journal")
	}
	return nil
}

// Close releases the journal's lock and closes it.
func (j *Journal) Close() {
	j.release()
	if j.file != nil {
		_ = j.file.Close()
	}
}

// requireRegular fails if path exists and is not a regular file. Checking
// before opening keeps a named pipe from blocking the open; callers check the
// opened file again, since the path can change in between.
func requireRegular(path string) error {
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	return nil
}

// ReadAuthorization reads the dispatch credential from a file readable only by
// its owner. The file holds the bearer token, with or without "Bearer ".
func ReadAuthorization(path string) (string, error) {
	if err := requireRegular(path); err != nil {
		return "", errs.NewValidation("authorization file must be a regular file readable only by its owner")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errs.NewValidation("cannot open authorization file")
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errs.NewValidation("authorization file must be a regular file readable only by its owner")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAuthorizationBytes+1))
	if err != nil || len(data) > maxAuthorizationBytes {
		return "", errs.NewValidation("authorization file is unreadable or too large")
	}
	token := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "Bearer "))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", errs.NewValidation("authorization file must hold one bearer token")
	}
	return "Bearer " + token, nil
}
