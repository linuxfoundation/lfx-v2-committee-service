// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	errs "github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

const v1AuditMaxBytes = 1 << 20

// v1AuditSourcePattern leaves room for the _sha256/_event suffixes of derived
// legacy actor namespaces within the indexer's 64-character source limit.
var v1AuditSourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,56}$`)

// v1AuditSourceValid reports whether a source namespace is usable. Names ending in
// a derived-namespace suffix are reserved so derived actor refs cannot collide.
func v1AuditSourceValid(source string) bool {
	return v1AuditSourcePattern.MatchString(source) &&
		!strings.HasSuffix(source, v1AuditHashedActorSuffix) && !strings.HasSuffix(source, v1AuditEventActorSuffix)
}

// V1AuditMappings is a fixed offline mapping manifest. No current-resource or
// directory lookup is performed. Resource revisions are not supplied here: each
// event's revision is derived from its source occurrence time (see planV1AuditRow).
//
// V2OriginActors lists the recorded v1 actor IDs that the v2-to-v1 sync writes
// with, so changes made in v2 can be recognized. NativeHistorySince (RFC 3339 in
// UTC, ending in "Z") is when native v2 committee history began in this
// environment; INSERT and UPDATE rows by those actors at or after it are already
// recorded in v2 and are skipped. DELETE rows never are: their recorded actor is
// the previous last modifier, not the deleter. Without a cutover, native history
// is not live and such rows remain the only record.
type V1AuditMappings struct {
	Version            int               `json:"version"`
	Source             string            `json:"source"`
	Projects           map[string]string `json:"projects"`
	Committees         map[string]string `json:"committees"`
	Members            map[string]string `json:"members"`
	Actors             map[string]string `json:"actors"`
	V2OriginActors     []string          `json:"v2_origin_actors,omitempty"`
	NativeHistorySince string            `json:"native_history_since,omitempty"`
}

// V1AuditOptions bounds and scopes an offline preview; zero MaxRecords means 10000.
type V1AuditOptions struct {
	Source      string
	CommitteeID string
	MaxRecords  int
	Mappings    *V1AuditMappings
}

// V1AuditIssue is safe for ordinary report output: no source values or identities.
type V1AuditIssue struct {
	Line   int    `json:"line"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// V1AuditReport contains only counts and fixed reason codes, never personal audit
// payloads. Importable means locally planned, not approved for dispatch.
type V1AuditReport struct {
	DryRun     bool `json:"dry_run"`
	Rows       int  `json:"rows"`
	Importable int  `json:"importable"`
	Skipped    int  `json:"skipped"`
	Invalid    int  `json:"invalid"`
	Events     int  `json:"events"`
	Limited    bool `json:"limited"`
	// SameInstantEvents counts planned events that share a resource and source
	// microsecond, and so a revision, with an earlier distinct event. Both are
	// imported: the indexer keeps both, and history is read in occurred_at,
	// publisher and event_id order. Review them before dispatch.
	SameInstantEvents int            `json:"same_instant_events"`
	Reasons           map[string]int `json:"reasons"`
	Issues            []V1AuditIssue `json:"issues"`
	ActorCandidates   map[string]int `json:"actor_candidates"`
}

// DecodeV1AuditMappings validates a bounded, closed manifest without echoing its
// path, IDs, principals or source values in errors.
func DecodeV1AuditMappings(input io.Reader) (*V1AuditMappings, error) {
	data, err := io.ReadAll(io.LimitReader(input, v1AuditMaxBytes+1))
	if err != nil || len(data) > v1AuditMaxBytes {
		return nil, errs.NewValidation("cannot read bounded audit mapping manifest")
	}
	var mappings V1AuditMappings
	if !strictV1AuditJSON(data) || !v1AuditKnownKeys(data, "version", "source", "projects", "committees", "members", "actors",
		"v2_origin_actors", "native_history_since") ||
		decodeV1AuditJSON(data, &mappings) != nil {
		return nil, errs.NewValidation("invalid audit mapping manifest")
	}
	if err := validateV1AuditMappings(&mappings); err != nil {
		return nil, err
	}
	return &mappings, nil
}

// validateV1AuditMappings checks decoded and direct-caller manifests alike.
func validateV1AuditMappings(mappings *V1AuditMappings) error {
	if mappings.Version != 1 || !v1AuditSourceValid(mappings.Source) {
		return errs.NewValidation("invalid audit mapping manifest")
	}
	for _, mapping := range []map[string]string{mappings.Projects, mappings.Committees, mappings.Members} {
		for source, target := range mapping {
			parsed, err := uuid.Parse(target)
			if !v1AuditMappingKey(source) || err != nil || parsed == uuid.Nil || parsed.String() != target {
				return errs.NewValidation("invalid audit resource mapping")
			}
		}
	}
	for source, ref := range mappings.Actors {
		kind, id, ok := strings.Cut(ref, ":")
		if !v1AuditMappingKey(source) || !ok || (kind != "user" && kind != "service") || !v1AuditOpaqueID(id) {
			return errs.NewValidation("invalid audit actor mapping")
		}
	}
	seenActors := make(map[string]bool, len(mappings.V2OriginActors))
	for _, actor := range mappings.V2OriginActors {
		if !v1AuditMappingKey(actor) || seenActors[actor] {
			return errs.NewValidation("invalid audit v2-origin actor")
		}
		seenActors[actor] = true
	}
	if mappings.NativeHistorySince != "" {
		// A cutover without actors to apply it to is a configuration mistake.
		if _, err := v1AuditNativeHistorySince(mappings); err != nil || len(mappings.V2OriginActors) == 0 {
			return errs.NewValidation("invalid audit native history cutover")
		}
	}
	return nil
}

// v1AuditNativeHistorySince parses the manifest cutover; zero means native v2
// history is not live. Only UTC ("Z") is accepted: time.Parse also accepts
// offsets such as +24:00, which would silently move the cutover earlier.
func v1AuditNativeHistorySince(mappings *V1AuditMappings) (time.Time, error) {
	if mappings.NativeHistorySince == "" {
		return time.Time{}, nil
	}
	since, err := time.Parse(time.RFC3339Nano, mappings.NativeHistorySince)
	if err != nil || since.IsZero() || !strings.HasSuffix(mappings.NativeHistorySince, "Z") {
		return time.Time{}, errs.NewValidation("invalid audit native history cutover")
	}
	return since.UTC(), nil
}

func v1AuditMappingKey(source string) bool {
	return source != "" && len(source) <= 256 && utf8.ValidString(source)
}

// PlanV1AuditHistory reads NDJSON and emits planned events, each carrying its
// frozen indexer import message, through the optional sink. It has no
// infrastructure ports, publisher or mutation capability. The caller must protect
// that personal output and discard it after any error or invalid/conflicting
// record. No live revisions are allocated or state advanced here.
func PlanV1AuditHistory(ctx context.Context, input io.Reader, options V1AuditOptions, emit func(model.V1AuditPlanEvent) error) (*V1AuditReport, error) {
	if options.MaxRecords == 0 {
		options.MaxRecords = 10000
	}
	if input == nil || !v1AuditSourceValid(options.Source) || options.MaxRecords < 1 || options.MaxRecords > 100000 {
		return nil, errs.NewValidation("invalid offline audit preview options")
	}
	mappings := &V1AuditMappings{Version: 1, Source: options.Source}
	if options.Mappings != nil {
		// Copy caller-owned maps so they stay fixed for the whole preview, then
		// validate direct callers the same way as decoded CLI input.
		mappings = &V1AuditMappings{
			Version: options.Mappings.Version, Source: options.Mappings.Source,
			Projects: maps.Clone(options.Mappings.Projects), Committees: maps.Clone(options.Mappings.Committees),
			Members: maps.Clone(options.Mappings.Members), Actors: maps.Clone(options.Mappings.Actors),
			V2OriginActors:     slices.Clone(options.Mappings.V2OriginActors),
			NativeHistorySince: options.Mappings.NativeHistorySince,
		}
	}
	if err := validateV1AuditMappings(mappings); err != nil {
		return nil, err
	}
	nativeHistorySince, _ := v1AuditNativeHistorySince(mappings)
	if mappings.Source != options.Source {
		return nil, errs.NewValidation("audit mapping source does not match preview source")
	}
	report := &V1AuditReport{DryRun: true, Reasons: make(map[string]int), Issues: []V1AuditIssue{}, ActorCandidates: make(map[string]int)}
	seen := make(map[string][32]byte)
	revisions := make(map[string]string)
	reader := bufio.NewReader(input)
	line := 0
	for {
		if err := ctx.Err(); err != nil {
			return report, errs.NewUnexpected("audit preview canceled", err)
		}
		if report.Rows >= options.MaxRecords {
			// Only probe for more input: a record past the limit is never read,
			// so its size or content cannot fail the bounded preview.
			more, err := v1AuditHasMoreInput(reader)
			if err != nil {
				return report, v1AuditReadError(ctx, "cannot read audit input")
			}
			report.Limited = more
			break
		}
		data, err := readV1AuditLine(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return report, v1AuditReadError(ctx, "cannot read audit input or record exceeds size limit")
		}
		line++
		if len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		report.Rows++
		row, parseErr := decodeV1AuditRow(data)
		if parseErr != nil {
			report.record(line, "invalid", "invalid_source_record")
			continue
		}
		// Source identity is run-wide: a changed repeat is invalid even when
		// either copy is outside the selected committee.
		digest := sha256.Sum256(row.canonical)
		previous, repeated := seen[row.ID]
		if repeated && previous != digest {
			report.record(line, "invalid", "conflicting_source_identity")
			continue
		}
		seen[row.ID] = digest
		if options.CommitteeID != "" && row.RecordID != options.CommitteeID {
			report.record(line, "skipped", "outside_committee_scope")
			continue
		}
		if repeated {
			report.record(line, "skipped", "duplicate_source_record")
			continue
		}
		actorKind, _, _ := strings.Cut(v1AuditActor(row, mappings), ":")
		if _, explicit := mappings.Actors[row.CreatedBy]; explicit && row.CreatedBy != "" {
			actorKind = "mapped"
		} else if actorKind == "user" || actorKind == "service" {
			actorKind = "recorded_" + actorKind
		}
		report.ActorCandidates[actorKind]++
		// A change made in v2 and synced back to v1 is already in native v2
		// history once that is live; importing its v1 row would duplicate it.
		// created_at is when the sync wrote the change to v1, not when it was
		// made in v2, so the cutover must allow for the sync delay (see docs).
		// A DELETE row records the previous last modifier rather than the
		// deleter, so its actor cannot show the delete came from v2. Any other
		// action falls through to planV1AuditRow's validation.
		if !nativeHistorySince.IsZero() && (row.Action == "INSERT" || row.Action == "UPDATE") && !row.CreatedAt.Before(nativeHistorySince) &&
			slices.Contains(mappings.V2OriginActors, row.CreatedBy) {
			report.record(line, "skipped", "recorded_natively_in_v2")
			continue
		}
		events, status, reason := planV1AuditRow(row, mappings)
		if status != "importable" {
			report.record(line, status, reason)
			continue
		}
		for _, event := range events {
			key := v1AuditRevisionKey(event)
			if previous, exists := revisions[key]; exists && previous != event.EventID {
				report.SameInstantEvents++
			}
			revisions[key] = event.EventID
			if emit != nil && emit(event) != nil {
				return report, errs.NewUnexpected("cannot write private audit draft")
			}
		}
		report.Importable++
		report.Events += len(events)
	}
	if err := ctx.Err(); err != nil {
		return report, errs.NewUnexpected("audit preview canceled", err)
	}
	return report, nil
}

// v1AuditReadError reports cancellation when it caused the read failure, such as
// a caller closing the input to interrupt a blocked read.
func v1AuditReadError(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return errs.NewUnexpected("audit preview canceled", err)
	}
	return errs.NewValidation(message)
}

var errV1AuditLineTooLong = errs.NewValidation("audit record exceeds size limit")

// readV1AuditLine returns the next line without its line ending, or io.EOF once
// input is exhausted. Lines longer than v1AuditMaxBytes are rejected without
// buffering more than the limit.
func readV1AuditLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > v1AuditMaxBytes+2 {
			return nil, errV1AuditLineTooLong
		}
		line = append(line, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(line) == 0 {
			return nil, io.EOF
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		if len(line) > v1AuditMaxBytes {
			return nil, errV1AuditLineTooLong
		}
		return line, nil
	}
}

// v1AuditHasMoreInput reports whether anything other than white space remains,
// using the same Unicode rule as bytes.TrimSpace for blank lines. It streams one
// rune at a time, so a record past the limit is never read whole.
func v1AuditHasMoreInput(reader *bufio.Reader) (bool, error) {
	for {
		r, _, err := reader.ReadRune()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !unicode.IsSpace(r) {
			return true, nil
		}
	}
}

func (r *V1AuditReport) record(line int, status, reason string) {
	if status == "invalid" {
		r.Invalid++
	} else {
		r.Skipped++
	}
	r.Reasons[reason]++
	r.Issues = append(r.Issues, V1AuditIssue{Line: line, Status: status, Reason: reason})
}

func v1AuditRevisionKey(event model.V1AuditPlanEvent) string {
	data, _ := json.Marshal([]any{event.ResourceType, event.ResourceUID, event.ResourceRevision})
	return string(data)
}

func decodeV1AuditJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return errs.NewValidation("invalid audit JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errs.NewValidation("audit JSON must contain exactly one value")
	}
	return nil
}

func strictV1AuditJSON(data []byte) bool {
	if !utf8.Valid(data) || v1AuditLoneSurrogate(data) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return v1AuditJSONValue(decoder, 0) && decoder.Decode(new(any)) == io.EOF
}

// v1AuditLoneSurrogate reports whether a JSON string escapes an unpaired UTF-16
// surrogate such as \uD800. encoding/json silently decodes those to U+FFFD, which
// could make different source keys or text compare equal. Escaped backslashes
// are skipped; malformed escapes are left for the decoder to reject.
func v1AuditLoneSurrogate(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		switch {
		case !inString:
			inString = data[i] == '"'
		case data[i] == '"':
			inString = false
		case data[i] == '\\':
			i++
			if i >= len(data) || data[i] != 'u' {
				continue
			}
			code, ok := v1AuditHex4(data, i+1)
			if !ok {
				continue
			}
			i += 4
			switch {
			case code >= 0xDC00 && code <= 0xDFFF:
				return true
			case code >= 0xD800 && code <= 0xDBFF:
				if i+2 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return true
				}
				low, ok := v1AuditHex4(data, i+3)
				if !ok || low < 0xDC00 || low > 0xDFFF {
					return true
				}
				i += 6
			}
		}
	}
	return false
}

func v1AuditHex4(data []byte, start int) (rune, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	var code rune
	for _, c := range data[start : start+4] {
		var digit byte
		switch {
		case c >= '0' && c <= '9':
			digit = c - '0'
		case c >= 'a' && c <= 'f':
			digit = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			digit = c - 'A' + 10
		default:
			return 0, false
		}
		code = code<<4 | rune(digit)
	}
	return code, true
}

func v1AuditJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok || seen[key] {
				return false
			}
			seen[key] = true
			if !v1AuditJSONValue(decoder, depth+1) {
				return false
			}
		}
		token, err = decoder.Token()
		return err == nil && token == json.Delim('}')
	case json.Delim('['):
		for decoder.More() {
			if !v1AuditJSONValue(decoder, depth+1) {
				return false
			}
		}
		token, err = decoder.Token()
		return err == nil && token == json.Delim(']')
	default:
		return true
	}
}

func v1AuditOpaqueID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e || strings.ContainsRune(":#@{}", r) {
			return false
		}
	}
	return true
}

func v1AuditKnownKeys(data []byte, keys ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	for key := range fields {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func v1AuditHash(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
