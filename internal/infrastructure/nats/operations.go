// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

// filter is a generic function that filters a list of keys based on a model and a filter function.
// Example:
//
// keys := jetstream.KeyLister{Keys: []string{"key1", "key2", "key3"}}
//
// model := model.CommitteeMember{}
//
//	get := func(uid string, m *model.CommitteeMember) error {
//				_, errGet := s.get(ctx, constants.KVBucketNameCommitteeMembers, uid, m, false)
//				return errGet
//			},
//
//	filter := func(model model.CommitteeMember) bool {
//		return model.UID == "key1"
//	}
//
// members, err := filter(keys, model, get, filter)
//
//	if err != nil {
//		return nil, err
//	}
func filter[T any](
	keys jetstream.KeyLister,
	model T,
	get func(uid string, model T) error,
	filter func(T) bool) ([]T, error) {

	var output []T
	for key := range keys.Keys() {

		if strings.HasPrefix(key, "lookup/") {
			continue
		}

		errGet := get(key, model)
		if errGet != nil {
			return nil, errGet
		}

		if filter(model) {
			output = append(output, model)
		}

	}

	return output, nil
}
