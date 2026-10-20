// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	committeeservicesvr "github.com/linuxfoundation/lfx-v2-committee-service/gen/http/committee_service/server"
	goahttp "goa.design/goa/v3/http"
)

// requestDecoder preserves Goa decoding except for committee creation, whose
// project_uid must have the same interpretation as Heimdall's case-sensitive
// body lookup. DisallowUnknownFields alone does not prevent case-insensitive
// struct field binding or duplicate keys in encoding/json.
func requestDecoder(r *http.Request) goahttp.Decoder {
	decoder := goahttp.RequestDecoder(r)
	return goahttp.EncodingFunc(func(v any) error {
		if _, ok := v.(*committeeservicesvr.CreateCommitteeRequestBody); !ok {
			return decoder.Decode(v)
		}
		// Creation is authorized using Heimdall's JSON body, so do not allow
		// an alternate encoding to bypass the JSON key validation.
		if _, ok := decoder.(*json.Decoder); !ok {
			return fmt.Errorf("committee creation requires application/json")
		}
		var body json.RawMessage
		if err := decoder.Decode(&body); err != nil {
			return err
		}
		var trailing json.RawMessage
		if err := decoder.Decode(&trailing); err != io.EOF {
			return fmt.Errorf("committee creation requires a single JSON object")
		}
		if err := validateCreateProjectKey(body); err != nil {
			return err
		}
		return json.Unmarshal(body, v)
	})
}

// validateCreateProjectKey requires exactly one canonical top-level project_uid.
// Token decoding unescapes property names, including Unicode-escaped variants;
// decoding each value as RawMessage skips nested objects without treating their
// keys as top-level authorization inputs.
func validateCreateProjectKey(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("committee creation requires a JSON object")
	}
	seen := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("invalid JSON object key")
		}
		if strings.EqualFold(key, "project_uid") {
			if key != "project_uid" || seen {
				return fmt.Errorf("project_uid must appear exactly once with its canonical spelling")
			}
			seen = true
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if !seen {
		return fmt.Errorf("project_uid is required")
	}
	return nil
}
