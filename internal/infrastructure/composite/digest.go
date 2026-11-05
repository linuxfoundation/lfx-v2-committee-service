// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package composite

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// Digest is the SHA-256 of the record in a form that does not depend on how a store holds it: the JSON of the
// model without its revision (which is compared on its own, and is not in the JSON), with every time.Time in UTC and
// to the microsecond PostgreSQL keeps. Only the times of the model are changed: a text field that looks like a time,
// as the dates the models keep as strings do, is compared as it is, and so is an empty list against a missing one, as
// the PostgreSQL store keeps them.
func Digest(record any) ([sha256.Size]byte, error) {
	encoded, err := json.Marshal(withNormalizedTimes(reflect.ValueOf(record)).Interface())
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encoding a record: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

var timeType = reflect.TypeOf(time.Time{})

// withNormalizedTimes returns a copy of v in which every time.Time is in UTC and truncated to the microsecond. The
// record itself is not changed. Unexported fields are copied as they are.
func withNormalizedTimes(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(withNormalizedTimes(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(withNormalizedTimes(v.Elem()))
		return out
	case reflect.Struct:
		if v.Type() == timeType {
			return reflect.ValueOf(v.Interface().(time.Time).UTC().Truncate(time.Microsecond))
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if out.Field(i).CanSet() {
				out.Field(i).Set(withNormalizedTimes(v.Field(i)))
			}
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(withNormalizedTimes(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := range v.Len() {
			out.Index(i).Set(withNormalizedTimes(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for iter := v.MapRange(); iter.Next(); {
			out.SetMapIndex(iter.Key(), withNormalizedTimes(iter.Value()))
		}
		return out
	default:
		return v
	}
}
