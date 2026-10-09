// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package opensearch

import (
	opensearchgo "github.com/opensearch-project/opensearch-go/v2"

	"github.com/linuxfoundation/lfx-v2-committee-service/pkg/errors"
)

// NewClient creates an OpenSearch client for the given base URL.
func NewClient(openSearchURL string) (*opensearchgo.Client, error) {
	client, err := opensearchgo.NewClient(opensearchgo.Config{
		Addresses: []string{openSearchURL},
	})
	if err != nil {
		return nil, errors.NewUnexpected("failed to create OpenSearch client", err)
	}
	return client, nil
}
