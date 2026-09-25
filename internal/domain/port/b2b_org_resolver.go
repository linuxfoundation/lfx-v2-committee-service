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
