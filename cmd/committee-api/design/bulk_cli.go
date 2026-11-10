// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package design

import (
	"fmt"
	"strings"

	"goa.design/goa/v3/codegen"
	"goa.design/goa/v3/codegen/cli"
	"goa.design/goa/v3/eval"
)

func init() {
	codegen.RegisterPlugin("committee-bulk-cli", "gen", nil, bulkCLI)
}

// bulkCLI fixes the bulk CLI's input boundary at generation time. Goa v3.22.6
// validates value-string enums before applying defaults and skips nil array
// elements that its converter subsequently dereferences.
func bulkCLI(_ string, _ []eval.Root, files []*codegen.File) ([]*codegen.File, error) {
	found := false
	for _, file := range files {
		if file.Path != "gen/http/committee_service/client/cli.go" {
			continue
		}
		for _, section := range file.SectionTemplates {
			build, ok := section.Data.(*cli.BuildFunctionData)
			if !ok || build.Name != "BuildBulkCreateCommitteeMembersPayload" {
				continue
			}
			for _, field := range build.Fields {
				if field.VarName != "body" {
					continue
				}
				const marker = "if body.Members == nil {"
				if strings.Count(field.Init, marker) != 1 || len(build.FormalParams) == 0 {
					return nil, fmt.Errorf("bulk CLI body validation changed; update the input customization")
				}
				field.Init = strings.Replace(field.Init, marker, fmt.Sprintf(bulkCLIDefaults, build.FormalParams[0])+marker, 1)
				found = true
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("bulk CLI payload builder not found; update the input customization")
	}
	return files, nil
}

const bulkCLIDefaults = `
	// Preserve omission separately from explicit empty/invalid enum values.
	var input struct {
		Members []*struct {
			AppointedBy *string "json:\"appointed_by\""
			Status *string "json:\"status\""
			Role *struct { Name *string "json:\"name\"" } "json:\"role\""
			Voting *struct { Status *string "json:\"status\"" } "json:\"voting\""
		} "json:\"members\""
	}
	if err := json.Unmarshal([]byte(%s), &input); err != nil { return nil, err }
	for i, row := range body.Members {
		if row == nil { return nil, fmt.Errorf("members[%%d] must be an object", i) }
		fields := input.Members[i]
		if fields.AppointedBy == nil { row.AppointedBy = "None" }
		if fields.Status == nil { row.Status = "Active" }
		if row.Role != nil && (fields.Role == nil || fields.Role.Name == nil) { row.Role.Name = "None" }
		if row.Voting != nil && (fields.Voting == nil || fields.Voting.Status == nil) { row.Voting.Status = "None" }
	}
`
