// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

// UserEmails holds the primary and alternate email addresses for a user.
type UserEmails struct {
	PrimaryEmail    string
	AlternateEmails []AlternateEmail
}

// AlternateEmail represents a single alternate email with its verification status.
type AlternateEmail struct {
	Email    string
	Verified bool
}

// UserMetadata holds profile information for a user returned by the auth service.
type UserMetadata struct {
	Picture       string
	Zoneinfo      string
	Name          string
	GivenName     string
	FamilyName    string
	JobTitle      string
	Organization  string
	Country       string
	StateProvince string
	City          string
	Address       string
	PostalCode    string
	PhoneNumber   string
	TShirtSize    string
}
