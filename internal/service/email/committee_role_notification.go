// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package email

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
)

//go:embed templates/committee_role_notification.html templates/committee_role_notification.txt
//go:embed templates/committee_role_updated.html templates/committee_role_updated.txt
//go:embed templates/committee_role_removed.html templates/committee_role_removed.txt
var committeeNotificationTemplates embed.FS

var (
	committeeRoleHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_role_notification.html").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_notification.html"),
	)
	committeeRoleTextTemplate = texttemplate.Must(
		texttemplate.New("committee_role_notification.txt").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_notification.txt"),
	)

	committeeRoleUpdatedHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_role_updated.html").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_updated.html"),
	)
	committeeRoleUpdatedTextTemplate = texttemplate.Must(
		texttemplate.New("committee_role_updated.txt").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_updated.txt"),
	)

	committeeRoleRemovedHTMLTemplate = htmltemplate.Must(
		htmltemplate.New("committee_role_removed.html").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_removed.html"),
	)
	committeeRoleRemovedTextTemplate = texttemplate.Must(
		texttemplate.New("committee_role_removed.txt").
			ParseFS(committeeNotificationTemplates, "templates/committee_role_removed.txt"),
	)
)

// CapabilityGroup holds a role display name and the list of capability bullet points for that role.
type CapabilityGroup struct {
	Role  string
	Items []string
}

// CommitteeRoleNotificationData holds the template variables for a committee role notification email.
type CommitteeRoleNotificationData struct {
	RecipientName    string
	CommitteeName    string
	Role             string
	CommitteeURL     string
	InviterName      string
	CapabilityGroups []CapabilityGroup // populated by RenderCommitteeRoleNotification
}

// committeeRoleCapabilities returns the capability bullet points for a given display role name.
// Returns nil for unknown roles so templates can conditionally omit the section.
func committeeRoleCapabilities(displayRole string) []string {
	switch displayRole {
	case "Manage":
		return []string{
			"Update committee settings",
			"Manage committee members, invites & applications",
			"Manage committee links, folder, & documents",
			"Download committee documents",
			"Schedule a survey for a committee",
		}
	case "View":
		return []string{
			"View committee details",
			"View committee settings",
			"View committee members",
			"View committee invites & applications",
			"View committee links, folders and documents",
			"Download committee documents",
		}
	default:
		return nil
	}
}

// capabilityGroupsForRoles builds a CapabilityGroup slice from display role names,
// omitting any role whose capabilities are unknown (e.g. "Member").
func capabilityGroupsForRoles(displayRoles []string) []CapabilityGroup {
	groups := make([]CapabilityGroup, 0, len(displayRoles))
	for _, r := range displayRoles {
		if items := committeeRoleCapabilities(r); len(items) > 0 {
			groups = append(groups, CapabilityGroup{Role: r, Items: items})
		}
	}
	return groups
}

// RenderCommitteeRoleNotification renders the subject, HTML body, and plain-text body
// for a committee role notification email.
func RenderCommitteeRoleNotification(data CommitteeRoleNotificationData) (subject, html, text string, err error) {
	if len(data.CapabilityGroups) == 0 {
		data.CapabilityGroups = capabilityGroupsForRoles([]string{data.Role})
	}
	subject = sanitizeHeader(data.InviterName) + " added you as a " + data.Role + " on " + sanitizeHeader(data.CommitteeName)

	var htmlBuf bytes.Buffer
	if err = committeeRoleHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeRoleTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}

// CommitteeRoleUpdatedData holds the template variables for a committee role-updated email.
// OldRoles and NewRoles hold the internal role names (Writer, Auditor); the render function
// converts them to display names (Manage, View) and populates OldJoinedRoles / NewJoinedRoles.
type CommitteeRoleUpdatedData struct {
	RecipientName       string
	CommitteeName       string
	OldRoles            []string
	NewRoles            []string
	OldJoinedRoles      string // computed by RenderCommitteeRoleUpdated
	NewJoinedRoles      string // computed by RenderCommitteeRoleUpdated
	CommitteeURL        string
	InviterName         string
	NewCapabilityGroups []CapabilityGroup // computed by RenderCommitteeRoleUpdated
}

// RenderCommitteeRoleUpdated renders the subject, HTML body, and plain-text body for an
// email notifying an LF user that their effective role on a committee changed.
func RenderCommitteeRoleUpdated(data CommitteeRoleUpdatedData) (subject, html, text string, err error) {
	data.OldJoinedRoles = JoinCommitteeRoles(CommitteeRolesForDisplay(data.OldRoles))
	data.NewJoinedRoles = JoinCommitteeRoles(CommitteeRolesForDisplay(data.NewRoles))
	if len(data.NewCapabilityGroups) == 0 {
		data.NewCapabilityGroups = capabilityGroupsForRoles(CommitteeRolesForDisplay(data.NewRoles))
	}

	subject = sanitizeHeader(data.InviterName) + " updated your role on " + sanitizeHeader(data.CommitteeName)

	var htmlBuf bytes.Buffer
	if err = committeeRoleUpdatedHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeRoleUpdatedTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}

// CommitteeRoleRemovedData holds the template variables for a committee removal email.
// OldRoles holds the internal role names the user held before removal; the render function
// converts them to display names and populates OldJoinedRoles.
type CommitteeRoleRemovedData struct {
	RecipientName  string
	CommitteeName  string
	OldRoles       []string
	OldJoinedRoles string // computed by RenderCommitteeRoleRemoved
	InviterName    string
}

// RenderCommitteeRoleRemoved renders the subject, HTML body, and plain-text body for an
// email notifying an LF user that they were fully removed from a committee.
func RenderCommitteeRoleRemoved(data CommitteeRoleRemovedData) (subject, html, text string, err error) {
	data.OldJoinedRoles = JoinCommitteeRoles(CommitteeRolesForDisplay(data.OldRoles))

	subject = sanitizeHeader(data.InviterName) + " removed you from " + sanitizeHeader(data.CommitteeName)

	var htmlBuf bytes.Buffer
	if err = committeeRoleRemovedHTMLTemplate.Execute(&htmlBuf, data); err != nil {
		return
	}
	html = htmlBuf.String()

	var textBuf bytes.Buffer
	if err = committeeRoleRemovedTextTemplate.Execute(&textBuf, data); err != nil {
		return
	}
	text = textBuf.String()
	return
}

// CommitteeRoleDisplayName maps an internal role name to its user-facing display name.
// Writer → "Manage", Auditor → "View". Unknown roles pass through unchanged.
func CommitteeRoleDisplayName(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "writer":
		return "Manage"
	case "auditor":
		return "View"
	default:
		return role
	}
}

// CommitteeRolesForDisplay converts internal role names to deduplicated display names and
// collapses them: if "Manage" (Writer) is present it supersedes "View" (Auditor), so only
// ["Manage"] is returned. Deduplication preserves the first occurrence order.
func CommitteeRolesForDisplay(roles []string) []string {
	seen := make(map[string]bool, len(roles))
	result := make([]string, 0, len(roles))
	for _, r := range roles {
		d := CommitteeRoleDisplayName(r)
		if !seen[d] {
			seen[d] = true
			result = append(result, d)
		}
	}
	if seen["Manage"] {
		return []string{"Manage"}
	}
	return result
}

// JoinCommitteeRoles returns a grammatically-joined string of role display names.
// [] → "", ["Manage"] → "Manage", ["Manage", "View"] → "Manage and View".
func JoinCommitteeRoles(roles []string) string {
	switch len(roles) {
	case 0:
		return ""
	case 1:
		return roles[0]
	case 2:
		return roles[0] + " and " + roles[1]
	default:
		return strings.Join(roles[:len(roles)-1], ", ") + ", and " + roles[len(roles)-1]
	}
}

// sanitizeHeader strips ASCII control characters (including CR/LF) from a string
// to prevent header injection if the value is ever used directly in an email header.
func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
