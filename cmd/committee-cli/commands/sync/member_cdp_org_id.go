// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	opensearchgo "github.com/opensearch-project/opensearch-go/v2"

	"github.com/linuxfoundation/lfx-v2-committee-service/cmd/committee-cli/commands"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/infrastructure/opensearch"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/env"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/utils"
)

const (
	defaultOpenSearchURL    = "http://localhost:9200"
	defaultOpenSearchIndex  = "resources"
	committeeMemberType     = "committee_member"
	cdpOrgIDUUIDPattern     = "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
	cdpOrgIDHexPattern      = "[0-9a-fA-F]{32}"
	memberDiscoveryPageSize = 500
)

type memberCDPOrgIDStats struct {
	commands.Stats
	CDPUUIDFound int
	Resolved     int
	Cleared      int
	Unresolved   int
}

type memberCDPOrgIDSubcommand struct {
	// Optional overrides for tests; production wiring leaves these nil.
	resolver   b2bOrgSFIDResolver
	memberUIDs []string
}

func (s *memberCDPOrgIDSubcommand) Name() string { return "member-cdp-org-id" }

func (s *memberCDPOrgIDSubcommand) Help() string {
	return "repair committee members storing a CDP org UUID in organization.id by resolving the b2b_org Salesforce SFID (LFXV2-2647)"
}

type b2bOrgSFIDResolver interface {
	ResolveSFID(ctx context.Context, name, website string) (sfid string, ok bool, err error)
}

func (s *memberCDPOrgIDSubcommand) Run(ctx context.Context, rc commands.RunContext) error {
	slog.DebugContext(ctx, "starting subcommand", "subcommand", s.Name(), "args", rc.Args)

	fs := flag.NewFlagSet("member-cdp-org-id", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: committee-cli sync member-cdp-org-id [flags]\n\nflags:\n")
		fs.PrintDefaults()
		_, _ = fmt.Fprintln(fs.Output())
		_, _ = fmt.Fprintln(fs.Output(), "environment:")
		_, _ = fmt.Fprintln(fs.Output(), "  OPENSEARCH_URL   OpenSearch base URL (default: http://localhost:9200)")
		_, _ = fmt.Fprintln(fs.Output(), "  OPENSEARCH_INDEX OpenSearch resources index (default: resources)")
	}
	committeeUID := fs.String("committee-uid", "", "limit repair to members of a single committee UID")
	memberUID := fs.String("member-uid", "", "limit repair to a single committee member UID")
	sleep := fs.Duration("sleep", 0, "wait between each member write (e.g. 200ms, 1s)")
	dryRun := fs.Bool("dry-run", true, "compute what would be written without writing (pass --dry-run=false to write)")
	clearUnresolved := fs.Bool("clear-unresolved", false, "when SFID cannot be resolved, clear organization.id (keep name/website)")
	openSearchURL := fs.String("opensearch-url", strings.TrimSpace(env.Get("OPENSEARCH_URL", defaultOpenSearchURL)), "override OPENSEARCH_URL")
	openSearchIndex := fs.String("opensearch-index", strings.TrimSpace(env.Get("OPENSEARCH_INDEX", defaultOpenSearchIndex)), "override OPENSEARCH_INDEX")
	if err := fs.Parse(rc.Args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if rc.CommitteeReader == nil {
		return errors.NewUnexpected("CommitteeReader is not wired in RunContext")
	}
	if rc.CommitteeWriterOrchestrator == nil {
		return errors.NewUnexpected("CommitteeWriterOrchestrator is not wired in RunContext")
	}

	rc.DryRun = *dryRun
	ctx = context.WithValue(ctx, constants.AuthorizationContextID, "Bearer lfx-v2-committee-service")

	var resolver b2bOrgSFIDResolver
	if s.resolver != nil {
		resolver = s.resolver
	} else {
		if rc.B2BOrgFallbackResolver == nil {
			return errors.NewUnexpected("B2BOrgFallbackResolver is not wired in RunContext")
		}
		resolver = rc.B2BOrgFallbackResolver
	}

	url := strings.TrimSpace(*openSearchURL)
	if url == "" {
		url = defaultOpenSearchURL
	}
	osClient, err := opensearch.NewClient(url)
	if err != nil {
		return err
	}

	members, err := collectMembersForRepair(ctx, rc, osClient, *openSearchIndex, *committeeUID, *memberUID, s.memberUIDs)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "member-cdp-org-id candidates loaded", "count", len(members))

	stats := memberCDPOrgIDStats{Stats: *commands.NewStats()}
	stats.DryRun = rc.DryRun

	for _, member := range members {
		stats.Total++

		orgID := strings.TrimSpace(member.Organization.ID)
		if !isCDPUUID(orgID) {
			stats.Skipped++
			continue
		}
		stats.CDPUUIDFound++

		sfid, resolved, err := resolver.ResolveSFID(ctx, member.Organization.Name, member.Organization.Website)
		if err != nil {
			slog.WarnContext(ctx, "failed to resolve b2b org SFID for member",
				"member_uid", member.UID,
				"committee_uid", member.CommitteeUID,
				"cdp_org_id", orgID,
				"organization_name", member.Organization.Name,
				"error", err,
			)
			stats.Failed++
			continue
		}

		var wantID string
		switch {
		case resolved:
			wantID = utils.NormalizeAccountSFID(sfid)
		case *clearUnresolved:
			wantID = ""
		default:
			stats.Unresolved++
			slog.InfoContext(ctx, "CDP org id unresolved (no write)",
				"member_uid", member.UID,
				"committee_uid", member.CommitteeUID,
				"cdp_org_id", orgID,
				"organization_name", member.Organization.Name,
				"organization_website", member.Organization.Website,
			)
			continue
		}

		if wantID == orgID {
			stats.Skipped++
			continue
		}

		action := "resolved_sfid"
		if wantID == "" {
			action = "cleared_id"
		}
		slog.InfoContext(ctx, "committee member CDP org id drift detected",
			"member_uid", member.UID,
			"committee_uid", member.CommitteeUID,
			"committee_name", member.CommitteeName,
			"action", action,
			"was_org_id", orgID,
			"now_org_id", wantID,
			"dry_run", rc.DryRun,
		)

		if rc.DryRun {
			if wantID != "" {
				stats.Resolved++
			} else {
				stats.Cleared++
			}
			stats.Updated++
			continue
		}

		fresh, revision, errGet := rc.CommitteeReader.GetMember(ctx, member.UID)
		if errGet != nil || fresh == nil {
			slog.WarnContext(ctx, "failed to re-read member before org id repair", "member_uid", member.UID, "error", errGet)
			stats.Failed++
			continue
		}
		if !isCDPUUID(strings.TrimSpace(fresh.Organization.ID)) {
			stats.Skipped++
			continue
		}
		if strings.TrimSpace(fresh.Organization.Name) != strings.TrimSpace(member.Organization.Name) ||
			strings.TrimSpace(fresh.Organization.Website) != strings.TrimSpace(member.Organization.Website) {
			slog.WarnContext(ctx, "member organization changed before org id repair; skipping stale resolution",
				"member_uid", member.UID,
				"committee_uid", member.CommitteeUID,
			)
			stats.Skipped++
			continue
		}
		if utils.NormalizeAccountSFID(fresh.Organization.ID) == wantID {
			stats.Skipped++
			continue
		}

		fresh.Organization.ID = wantID

		if _, errUpdate := rc.CommitteeWriterOrchestrator.UpdateMember(ctx, fresh, revision, true, true); errUpdate != nil {
			slog.WarnContext(ctx, "failed to update member organization id",
				"member_uid", member.UID, "committee_uid", member.CommitteeUID, "error", errUpdate)
			stats.Failed++
			continue
		}

		if wantID != "" {
			stats.Resolved++
		} else {
			stats.Cleared++
		}
		stats.Updated++

		if *sleep > 0 {
			if err := sleepWithCtx(ctx, *sleep); err != nil {
				return err
			}
		}
	}

	s.logSummary(ctx, &stats)

	if stats.Failed > 0 {
		return errors.NewUnexpected(fmt.Sprintf("%d member(s) failed to repair", stats.Failed))
	}
	return nil
}

// collectMembersForRepair loads candidate members without a full NATS KV bucket scan.
// Default scope queries OpenSearch for committee_member docs whose organization.id is a CDP UUID.
func collectMembersForRepair(
	ctx context.Context,
	rc commands.RunContext,
	osClient *opensearchgo.Client,
	index, committeeUID, memberUID string,
	discoveryUIDs []string,
) ([]*model.CommitteeMember, error) {
	if memberUID != "" {
		member, _, err := rc.CommitteeReader.GetMember(ctx, memberUID)
		if err != nil {
			return nil, errors.NewUnexpected("failed to get member", err)
		}
		return []*model.CommitteeMember{member}, nil
	}

	if committeeUID != "" {
		members, err := rc.CommitteeReader.ListMembersByCommittee(ctx, committeeUID)
		if err != nil {
			return nil, errors.NewUnexpected("failed to list committee members", err)
		}
		return members, nil
	}

	if len(discoveryUIDs) > 0 {
		return loadMembersByUID(ctx, rc, discoveryUIDs)
	}

	if osClient == nil {
		return nil, errors.NewUnexpected("OpenSearch client is required to discover CDP org members")
	}

	uids, err := searchMemberUIDsWithCDPOrgID(ctx, osClient, index)
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "discovered CDP org member candidates from OpenSearch", "count", len(uids))
	return loadMembersByUID(ctx, rc, uids)
}

func loadMembersByUID(ctx context.Context, rc commands.RunContext, uids []string) ([]*model.CommitteeMember, error) {
	members := make([]*model.CommitteeMember, 0, len(uids))
	for _, uid := range uids {
		member, _, err := rc.CommitteeReader.GetMember(ctx, uid)
		if err != nil {
			slog.WarnContext(ctx, "failed to load member candidate from NATS KV",
				"member_uid", uid, "error", err)
			continue
		}
		members = append(members, member)
	}
	return members, nil
}

func searchMemberUIDsWithCDPOrgID(ctx context.Context, client *opensearchgo.Client, index string) ([]string, error) {
	baseQuery := map[string]any{
		"bool": map[string]any{
			"must": []any{
				map[string]any{"term": map[string]any{"latest": true}},
				map[string]any{"term": map[string]any{"object_type": committeeMemberType}},
				map[string]any{
					"bool": map[string]any{
						"should": []any{
							map[string]any{"regexp": map[string]any{"data.organization.id": cdpOrgIDUUIDPattern}},
							map[string]any{"regexp": map[string]any{"data.organization.id": cdpOrgIDHexPattern}},
						},
						"minimum_should_match": 1,
					},
				},
			},
		},
	}

	seen := make(map[string]struct{})
	uids := make([]string, 0)
	var hitsTotal int64 = -1
	for from := 0; ; from += memberDiscoveryPageSize {
		page, total, err := searchMemberUIDPage(ctx, client, index, baseQuery, from, memberDiscoveryPageSize)
		if err != nil {
			return nil, err
		}
		if from == 0 && total >= 0 {
			hitsTotal = total
		}
		for _, uid := range page {
			if _, ok := seen[uid]; ok {
				continue
			}
			seen[uid] = struct{}{}
			uids = append(uids, uid)
		}
		if len(page) < memberDiscoveryPageSize {
			break
		}
	}
	if hitsTotal >= 0 {
		slog.InfoContext(ctx, "OpenSearch CDP org member discovery complete",
			"hits_total", hitsTotal,
			"retrieved", len(uids),
		)
		if int64(len(uids)) < hitsTotal {
			slog.WarnContext(ctx, "OpenSearch reported more CDP org members than were retrieved; discovery may be truncated",
				"hits_total", hitsTotal,
				"retrieved", len(uids),
			)
		}
	}
	return uids, nil
}

func searchMemberUIDPage(ctx context.Context, client *opensearchgo.Client, index string, baseQuery map[string]any, from, size int) ([]string, int64, error) {
	query := map[string]any{
		"from":    from,
		"size":    size,
		"query":   baseQuery,
		"sort":    []any{map[string]any{"object_id": "asc"}},
		"_source": []string{"object_id", "data.uid"},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, -1, errors.NewUnexpected("marshal OpenSearch member discovery query", err)
	}

	res, err := client.Search(
		client.Search.WithContext(ctx),
		client.Search.WithIndex(index),
		client.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, -1, errors.NewUnexpected("OpenSearch member discovery request failed", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, -1, errors.NewUnexpected(fmt.Sprintf("OpenSearch member discovery error %s: %s", res.Status(), raw))
	}

	var parsed struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source struct {
					ObjectID string `json:"object_id"`
					Data     struct {
						UID string `json:"uid"`
					} `json:"data"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, -1, errors.NewUnexpected("decode OpenSearch member discovery response", err)
	}

	uids := make([]string, 0, len(parsed.Hits.Hits))
	for _, hit := range parsed.Hits.Hits {
		uid := strings.TrimSpace(hit.Source.Data.UID)
		if uid == "" {
			uid = strings.TrimSpace(hit.Source.ObjectID)
		}
		if uid == "" {
			continue
		}
		uids = append(uids, uid)
	}
	return uids, parsed.Hits.Total.Value, nil
}

func (s *memberCDPOrgIDSubcommand) logSummary(ctx context.Context, stats *memberCDPOrgIDStats) {
	stats.Log(ctx, "sync member-cdp-org-id")
	slog.InfoContext(ctx, "SUMMARY",
		"total", stats.Total,
		"cdp_uuid_found", stats.CDPUUIDFound,
		"resolved_sfid", stats.Resolved,
		"cleared_id", stats.Cleared,
		"unresolved", stats.Unresolved,
		"updated", stats.Updated,
		"skipped", stats.Skipped,
		"failed", stats.Failed,
		"dry_run", stats.DryRun,
	)
}

// isCDPUUID reports whether id looks like a CDP organization UUID stored by
// self-serve (not a Salesforce Account SFID).
func isCDPUUID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if utils.IsSFIDShaped(id) {
		return false
	}
	if _, err := uuid.Parse(id); err == nil {
		return true
	}
	return false
}
