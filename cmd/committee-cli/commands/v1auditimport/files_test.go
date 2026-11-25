// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package v1auditimport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport"
	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands/v1auditimport/v1auditimporttest"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/service"
)

func TestOpenEvents(t *testing.T) {
	dir := t.TempDir()
	path, events := v1auditimporttest.WriteEvents(t, dir)
	opened, err := v1auditimport.OpenEvents(context.Background(), path)
	require.NoError(t, err)
	defer opened.Close()
	require.Equal(t, len(events), opened.Count)

	// Each scan rewinds and reads the whole, unchanged file.
	for range 2 {
		var read bytes.Buffer
		require.NoError(t, opened.Scan(func(r io.Reader) error { _, err := io.Copy(&read, r); return err }))
		original, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, original, read.Bytes())
	}
	// A scan that stops early still checks the rest of the file.
	require.NoError(t, opened.Scan(func(io.Reader) error { return nil }))

	// Editing or removing the original after it is opened changes nothing: the
	// bytes validated are the bytes scanned.
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, bytes.Replace(original, []byte("committee"), []byte("Committee"), 1), 0600))
	require.NoError(t, os.Remove(path))
	var read bytes.Buffer
	require.NoError(t, opened.Scan(func(r io.Reader) error { _, err := io.Copy(&read, r); return err }))
	require.Equal(t, original, read.Bytes())

	// Cancellation stops validating and copying a valid file.
	require.NoError(t, os.WriteFile(path, original, 0600))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = v1auditimport.OpenEvents(canceled, path)
	require.ErrorIs(t, err, context.Canceled)

	_, err = v1auditimport.OpenEvents(context.Background(), filepath.Join(dir, "private-marker"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-marker")
	_, err = v1auditimport.OpenEvents(context.Background(), dir)
	require.ErrorContains(t, err, "regular file")
}

func TestOpenJournal(t *testing.T) {
	dir := t.TempDir()
	path, events := v1auditimporttest.WriteEvents(t, dir)
	opened, err := v1auditimport.OpenEvents(context.Background(), path)
	require.NoError(t, err)
	defer opened.Close()
	id := v1auditimporttest.EventID(t, events[0])
	record := service.V1AuditImportRecord{EventID: id, ObjectType: events[0].ObjectType, Status: service.V1AuditImportPublished,
		At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Stream: "index-events", Sequence: 7}
	recordLine := func(status service.V1AuditImportStatus) []byte {
		next := record
		next.Status = status
		line, err := json.Marshal(next)
		require.NoError(t, err)
		return line
	}
	// newJournal creates a journal holding one published record; each subtest
	// has its own, so any subtest runs alone.
	newJournal := func(t *testing.T) string {
		t.Helper()
		statePath := filepath.Join(t.TempDir(), "state.ndjson")
		journal, err := v1auditimport.OpenJournal(statePath, opened, true)
		require.NoError(t, err)
		require.NoError(t, journal.Record(record))
		journal.Close()
		return statePath
	}
	status := func(t *testing.T, statePath string, create bool) service.V1AuditImportStatus {
		t.Helper()
		journal, err := v1auditimport.OpenJournal(statePath, opened, create)
		require.NoError(t, err)
		defer journal.Close()
		return journal.State.Status(id)
	}
	appendRaw := func(t *testing.T, statePath string, data []byte) {
		t.Helper()
		file, err := os.OpenFile(statePath, os.O_WRONLY|os.O_APPEND, 0)
		require.NoError(t, err)
		_, err = file.Write(data)
		require.NoError(t, err)
		require.NoError(t, file.Close())
	}

	t.Run("a read-only open of a missing journal creates nothing", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "state.ndjson")
		journal, err := v1auditimport.OpenJournal(statePath, opened, false)
		require.NoError(t, err)
		require.Empty(t, journal.State.Status(id))
		require.Error(t, journal.Record(record), "a read-only journal cannot be written")
		journal.Close()
		require.NoFileExists(t, statePath)
		require.NoFileExists(t, statePath+".lock")
	})

	t.Run("create, lock, record and replay", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "state.ndjson")
		journal, err := v1auditimport.OpenJournal(statePath, opened, true)
		require.NoError(t, err)
		info, err := os.Stat(statePath)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())

		_, err = v1auditimport.OpenJournal(statePath, opened, false)
		require.ErrorContains(t, err, "in use", "a second run is refused while the first holds the lock")

		require.NoError(t, journal.Record(record))
		journal.Close()
		require.NoFileExists(t, statePath+".lock")
		require.Equal(t, service.V1AuditImportPublished, status(t, statePath, false))
	})

	t.Run("every name of the journal shares its lock", func(t *testing.T) {
		if !fileLock {
			t.Skip("the fallback lock is a <path>.lock file")
		}
		statePath := newJournal(t)
		symlink := filepath.Join(t.TempDir(), "symlink.ndjson")
		require.NoError(t, os.Symlink(statePath, symlink))
		hardlink := filepath.Join(filepath.Dir(statePath), "hardlink.ndjson")
		require.NoError(t, os.Link(statePath, hardlink))

		journal, err := v1auditimport.OpenJournal(statePath, opened, true)
		require.NoError(t, err)
		for _, alias := range []string{symlink, hardlink} {
			for _, create := range []bool{true, false} {
				_, err := v1auditimport.OpenJournal(alias, opened, create)
				require.ErrorContains(t, err, "in use", "%s create=%v", filepath.Base(alias), create)
			}
		}
		journal.Close()
		// Released on close: no lock file is left to clean up.
		journal, err = v1auditimport.OpenJournal(hardlink, opened, true)
		require.NoError(t, err)
		journal.Close()
		require.NoFileExists(t, statePath+".lock")
	})

	t.Run("previews share the lock", func(t *testing.T) {
		if !fileLock {
			t.Skip("the fallback lock is exclusive for previews too")
		}
		statePath := newJournal(t)
		first, err := v1auditimport.OpenJournal(statePath, opened, false)
		require.NoError(t, err)
		defer first.Close()
		second, err := v1auditimport.OpenJournal(statePath, opened, false)
		require.NoError(t, err)
		second.Close()
	})

	t.Run("the fallback lock file is exclusive and released on close", func(t *testing.T) {
		if fileLock {
			t.Skip("this platform locks the file itself")
		}
		statePath := newJournal(t)
		first, err := v1auditimport.OpenJournal(statePath, opened, false)
		require.NoError(t, err)
		require.FileExists(t, statePath+".lock")
		_, err = v1auditimport.OpenJournal(statePath, opened, false)
		require.ErrorContains(t, err, "in use")
		first.Close()
		require.NoFileExists(t, statePath+".lock")
	})

	t.Run("a new journal is complete under its final name, with no leftovers", func(t *testing.T) {
		dir := t.TempDir()
		statePath := filepath.Join(dir, "state.ndjson")
		journal, err := v1auditimport.OpenJournal(statePath, opened, true)
		require.NoError(t, err)
		defer journal.Close()
		data, err := os.ReadFile(statePath)
		require.NoError(t, err)
		header, err := service.V1AuditImportJournalHeader(opened.SHA256)
		require.NoError(t, err)
		require.Equal(t, header, data, "the header is there before the journal has its final name")
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		names := []string{}
		for _, entry := range entries {
			if entry.Name() != "state.ndjson.lock" { // the fallback lock, held while open
				names = append(names, entry.Name())
			}
		}
		require.Equal(t, []string{"state.ndjson"}, names, "the temporary name is removed")
		require.NoError(t, journal.Record(record))
		data, err = os.ReadFile(statePath)
		require.NoError(t, err)
		require.True(t, bytes.HasPrefix(data, header) && bytes.Count(data, []byte("\n")) == 2)
	})

	t.Run("a preview reads an owner-read-only journal", func(t *testing.T) {
		statePath := newJournal(t)
		require.NoError(t, os.Chmod(statePath, 0400))
		require.Equal(t, service.V1AuditImportPublished, status(t, statePath, false))
	})

	t.Run("an append cut off mid-line is dropped before the next record", func(t *testing.T) {
		statePath := newJournal(t)
		appendRaw(t, statePath, []byte(`{"event_id":"partial","sta`))
		journal, err := v1auditimport.OpenJournal(statePath, opened, true)
		require.NoError(t, err)
		require.NoError(t, journal.Record(service.V1AuditImportRecord{EventID: id, ObjectType: record.ObjectType,
			Status: service.V1AuditImportPersisted, At: record.At}))
		journal.Close()

		data, err := os.ReadFile(statePath)
		require.NoError(t, err)
		require.NotContains(t, string(data), "partial")
		require.Equal(t, service.V1AuditImportPersisted, status(t, statePath, false))
	})

	t.Run("a complete record missing its newline is never applied", func(t *testing.T) {
		statePath := newJournal(t)
		appendRaw(t, statePath, recordLine(service.V1AuditImportPersisted))
		// Not applied by a preview, by the run that repairs it, or afterwards.
		require.Equal(t, service.V1AuditImportPublished, status(t, statePath, false))
		require.Equal(t, service.V1AuditImportPublished, status(t, statePath, true))
		require.Equal(t, service.V1AuditImportPublished, status(t, statePath, true))
		data, err := os.ReadFile(statePath)
		require.NoError(t, err)
		require.True(t, bytes.HasSuffix(data, []byte("\n")))
	})

	t.Run("an unterminated header is refused, not truncated", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "state.ndjson")
		header, err := service.V1AuditImportJournalHeader(opened.SHA256)
		require.NoError(t, err)
		content := bytes.TrimSuffix(header, []byte("\n"))
		require.NoError(t, os.WriteFile(statePath, content, 0600))
		_, err = v1auditimport.OpenJournal(statePath, opened, true)
		require.ErrorContains(t, err, "header")
		after, err := os.ReadFile(statePath)
		require.NoError(t, err)
		require.Equal(t, content, after)
	})

	t.Run("a journal of another events file is refused and left untouched", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "other.ndjson")
		header, err := service.V1AuditImportJournalHeader(strings.Repeat("b", 64))
		require.NoError(t, err)
		content := slices.Concat(header, []byte(`{"event_id":"partial","sta`))
		require.NoError(t, os.WriteFile(other, content, 0600))
		_, err = v1auditimport.OpenJournal(other, opened, true)
		require.ErrorContains(t, err, "different events file")
		after, err := os.ReadFile(other)
		require.NoError(t, err)
		require.Equal(t, content, after)
		require.NoFileExists(t, other+".lock", "a failed open releases its lock")
	})

	t.Run("a journal readable by others is refused", func(t *testing.T) {
		statePath := newJournal(t)
		require.NoError(t, os.Chmod(statePath, 0644))
		_, err := v1auditimport.OpenJournal(statePath, opened, false)
		require.ErrorContains(t, err, "only by its owner")
	})
}

func TestReadAuthorization(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), mode))
		require.NoError(t, os.Chmod(path, mode))
		return path
	}
	for _, content := range []string{"token-1\n", "Bearer token-1", "  Bearer token-1  \n"} {
		got, err := v1auditimport.ReadAuthorization(write("ok", content, 0600))
		require.NoError(t, err)
		require.Equal(t, "Bearer token-1", got)
	}
	for name, path := range map[string]string{
		"group readable": write("shared", "private-marker", 0640),
		"empty":          write("empty", "\n", 0600),
		"two tokens":     write("two", "private-marker other", 0600),
		"too large":      write("large", strings.Repeat("a", 61<<10), 0600),
		"missing":        filepath.Join(dir, "private-marker"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := v1auditimport.ReadAuthorization(path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-marker")
		})
	}
}
