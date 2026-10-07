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
// It is consulted when B2BOrgResolver.ResolveByUID cannot find a b2b_org for the
// stored id — e.g. when the id is a legacy, non-SFID identifier for an
// organization that does have a b2b_org record under a different id.
type B2BOrgFallbackResolver interface {
	ResolveSFID(ctx context.Context, name, website string) (sfid string, found bool, err error)
}
