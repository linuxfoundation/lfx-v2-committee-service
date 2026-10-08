// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package port

import "context"

// B2BOrgResolver checks whether an organization id resolves to a b2b_org.
type B2BOrgResolver interface {
	// ResolveByUID reports whether uid resolves to a b2b_org and, when found,
	// returns the canonical 18-char Salesforce Account SFID.
	ResolveByUID(ctx context.Context, uid string) (sfid string, found bool, err error)
}

// B2BOrgFallbackResolver resolves a b2b_org SFID by organization name/website.
// It is consulted only for legacy, non-SFID-shaped organization ids, before
// B2BOrgResolver.ResolveByUID is attempted. A SFID-shaped id that misses
// B2BOrgResolver.ResolveByUID is not retried here — see sanitizeMemberOrganization.
type B2BOrgFallbackResolver interface {
	ResolveSFID(ctx context.Context, name, website string) (sfid string, found bool, err error)
}
