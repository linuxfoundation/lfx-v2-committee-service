// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package utils

import "strings"

// sfidSuffixAlphabet is the base-32 alphabet Salesforce uses to encode the 15→18 char checksum suffix.
const sfidSuffixAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"

// NormalizeAccountSFID canonicalizes a Salesforce Account id to its 18-character, case-safe form so
// secondary indexes and lookups key on a single stable value. The function keys on length only: any
// 15-character input is upgraded to 18 chars via the standard Salesforce checksum (it does not validate
// that the input is a real SFID); input of any other length (already 18 chars, empty, etc.) is returned
// trimmed and unchanged. Org Lens routes always carry the 18-char form (ORG_ACCOUNT_ID_PATTERN), so
// normalizing the stored id keeps the read filter and the index in the same value space.
//
// In production the stored committee_member.organization.id is already 18-char ~98.5% of the time and
// 0% 15-char, so this is correctness insurance rather than a hot path.
func NormalizeAccountSFID(id string) string {
	s := strings.TrimSpace(id)
	if len(s) != 15 {
		return s
	}
	var suffix strings.Builder
	for chunk := 0; chunk < 3; chunk++ {
		bits := 0
		for i := 0; i < 5; i++ {
			c := s[chunk*5+i]
			if c >= 'A' && c <= 'Z' {
				bits |= 1 << uint(i)
			}
		}
		suffix.WriteByte(sfidSuffixAlphabet[bits])
	}
	return s + suffix.String()
}
