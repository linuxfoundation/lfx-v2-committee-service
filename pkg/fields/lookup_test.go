// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fields

import (
	"reflect"
	"testing"
)

// Test struct for testing purposes
type TestStruct struct {
	Name       string `json:"name" xml:"fullname"`
	Age        int    `json:"age"`
	Email      string `json:"email"`
	IsActive   bool   `json:"is_active"`
	unexported string `custom:"unexported_tag"`
}

// Another test struct
type SimpleStruct struct {
	Value string `custom:"test_value"`
}

// Struct without tags
type NoTagStruct struct {
	Field1 string
	Field2 int
}

func TestLookupByTag(t *testing.T) {
	tests := []struct {
		name        string
		obj         any
		tagType     string
		tagValue    string
		expected    any
		shouldFind  bool
		description string
	}{
		{
			name:        "ValidJSONTag",
			obj:         TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true},
			tagType:     "json",
			tagValue:    "name",
			expected:    "John",
			shouldFind:  true,
			description: "Should find field with valid JSON tag",
		},
		{
			name:        "ValidJSONTagInt",
			obj:         TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true},
			tagType:     "json",
			tagValue:    "age",
			expected:    30,
			shouldFind:  true,
			description: "Should find integer field with valid JSON tag",
		},
		{
			name:        "ValidJSONTagBool",
			obj:         TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true},
			tagType:     "json",
			tagValue:    "is_active",
			expected:    true,
			shouldFind:  true,
			description: "Should find boolean field with valid JSON tag",
		},
		{
			name:        "ValidXMLTag",
			obj:         TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true},
			tagType:     "xml",
			tagValue:    "fullname",
			expected:    "John",
			shouldFind:  true,
			description: "Should find field with valid XML tag",
		},
		{
			name:        "ValidCustomTag",
			obj:         SimpleStruct{Value: "test"},
			tagType:     "custom",
			tagValue:    "test_value",
			expected:    "test",
			shouldFind:  true,
			description: "Should find field with custom tag",
		},
		{
			name:        "NonExistentTag",
			obj:         TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true},
			tagType:     "json",
			tagValue:    "nonexistent",
			expected:    nil,
			shouldFind:  false,
			description: "Should not find field with non-existent tag value",
		},
		{
			name:        "NonExistentTagType",
			obj:         TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true},
			tagType:     "nonexistent",
			tagValue:    "name",
			expected:    nil,
			shouldFind:  false,
			description: "Should not find field with non-existent tag type",
		},
		{
			name:        "EmptyTagValue",
			obj:         TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true},
			tagType:     "json",
			tagValue:    "",
			expected:    nil,
			shouldFind:  false,
			description: "Should not find field with empty tag value",
		},
		{
			name:        "EmptyTagType",
			obj:         TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true},
			tagType:     "",
			tagValue:    "name",
			expected:    nil,
			shouldFind:  false,
			description: "Should not find field with empty tag type",
		},
		{
			name:        "StructWithNoTags",
			obj:         NoTagStruct{Field1: "test", Field2: 42},
			tagType:     "json",
			tagValue:    "field1",
			expected:    nil,
			shouldFind:  false,
			description: "Should not find field in struct with no tags",
		},
		{
			name:        "UnexportedField",
			obj:         TestStruct{unexported: "secret"},
			tagType:     "custom",
			tagValue:    "unexported_tag",
			expected:    nil,
			shouldFind:  false,
			description: "Should not return value for unexported field even if tag matches",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, found := LookupByTag(tt.obj, tt.tagType, tt.tagValue)

			if found != tt.shouldFind {
				t.Errorf("LookupByTag() found = %v, want %v", found, tt.shouldFind)
			}

			if tt.shouldFind && !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("LookupByTag() result = %v, want %v", result, tt.expected)
			}

			if !tt.shouldFind && result != nil {
				t.Errorf("LookupByTag() result should be nil when not found, got %v", result)
			}
		})
	}
}

func TestLookupByTagWithPointer(t *testing.T) {
	tests := []struct {
		name        string
		obj         any
		tagType     string
		tagValue    string
		expected    any
		shouldFind  bool
		description string
	}{
		{
			name:        "PointerToStruct",
			obj:         &TestStruct{Name: "Jane", Age: 25, Email: "jane@example.com", IsActive: false},
			tagType:     "json",
			tagValue:    "name",
			expected:    "Jane",
			shouldFind:  true,
			description: "Should find field in pointer to struct",
		},
		{
			name:        "PointerToStructAge",
			obj:         &TestStruct{Name: "Jane", Age: 25, Email: "jane@example.com", IsActive: false},
			tagType:     "json",
			tagValue:    "age",
			expected:    25,
			shouldFind:  true,
			description: "Should find integer field in pointer to struct",
		},
		{
			name:        "PointerToStructNonExistent",
			obj:         &TestStruct{Name: "Jane", Age: 25, Email: "jane@example.com", IsActive: false},
			tagType:     "json",
			tagValue:    "nonexistent",
			expected:    nil,
			shouldFind:  false,
			description: "Should not find non-existent field in pointer to struct",
		},
		{
			name:        "NilPointer",
			obj:         (*TestStruct)(nil),
			tagType:     "json",
			tagValue:    "name",
			expected:    nil,
			shouldFind:  false,
			description: "Should handle nil pointer gracefully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, found := LookupByTag(tt.obj, tt.tagType, tt.tagValue)

			if found != tt.shouldFind {
				t.Errorf("LookupByTag() found = %v, want %v", found, tt.shouldFind)
			}

			if tt.shouldFind && !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("LookupByTag() result = %v, want %v", result, tt.expected)
			}

			if !tt.shouldFind && result != nil {
				t.Errorf("LookupByTag() result should be nil when not found, got %v", result)
			}
		})
	}
}

func TestLookupByTagWithNonStruct(t *testing.T) {
	tests := []struct {
		name        string
		obj         any
		tagType     string
		tagValue    string
		description string
	}{
		{
			name:        "StringInput",
			obj:         "not a struct",
			tagType:     "json",
			tagValue:    "name",
			description: "Should return false for string input",
		},
		{
			name:        "IntInput",
			obj:         42,
			tagType:     "json",
			tagValue:    "name",
			description: "Should return false for int input",
		},
		{
			name:        "SliceInput",
			obj:         []string{"a", "b", "c"},
			tagType:     "json",
			tagValue:    "name",
			description: "Should return false for slice input",
		},
		{
			name:        "MapInput",
			obj:         map[string]string{"key": "value"},
			tagType:     "json",
			tagValue:    "name",
			description: "Should return false for map input",
		},
		{
			name:        "NilInput",
			obj:         nil,
			tagType:     "json",
			tagValue:    "name",
			description: "Should return false for nil input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, found := LookupByTag(tt.obj, tt.tagType, tt.tagValue)

			if found {
				t.Errorf("LookupByTag() found = %v, want false for non-struct input", found)
			}

			if result != nil {
				t.Errorf("LookupByTag() result = %v, want nil for non-struct input", result)
			}
		})
	}
}

func TestLookupByTagEdgeCases(t *testing.T) {
	// Test with struct containing zero values
	t.Run("ZeroValues", func(t *testing.T) {
		obj := TestStruct{} // All fields will have zero values
		result, found := LookupByTag(obj, "json", "name")

		if !found {
			t.Errorf("LookupByTag() should find field even if it has zero value")
		}

		if result != "" {
			t.Errorf("LookupByTag() result = %v, want empty string for zero value", result)
		}
	})

	// Test with struct containing field that has zero value but is found
	t.Run("ZeroValueInt", func(t *testing.T) {
		obj := TestStruct{Age: 0} // Age is explicitly set to 0
		result, found := LookupByTag(obj, "json", "age")

		if !found {
			t.Errorf("LookupByTag() should find field even if it has zero value")
		}

		if result != 0 {
			t.Errorf("LookupByTag() result = %v, want 0 for zero value int", result)
		}
	})

	// Test with struct containing boolean false value
	t.Run("ZeroValueBool", func(t *testing.T) {
		obj := TestStruct{IsActive: false} // IsActive is explicitly set to false
		result, found := LookupByTag(obj, "json", "is_active")

		if !found {
			t.Errorf("LookupByTag() should find field even if it has zero value")
		}

		if result != false {
			t.Errorf("LookupByTag() result = %v, want false for zero value bool", result)
		}
	})
}

// Benchmark test to measure performance
func BenchmarkLookupByTag(b *testing.B) {
	obj := TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LookupByTag(obj, "json", "email")
	}
}

func BenchmarkLookupByTagPointer(b *testing.B) {
	obj := &TestStruct{Name: "John", Age: 30, Email: "john@example.com", IsActive: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LookupByTag(obj, "json", "email")
	}
}
