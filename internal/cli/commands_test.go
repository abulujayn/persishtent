package cli

import (
	"testing"
)

func TestFindNextAutoName(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		expected string
	}{
		{"Empty", []string{}, "0"},
		{"SimpleSequence", []string{"0"}, "1"},
		{"Gap", []string{"0", "2"}, "1"}, // Should fill gap
		{"Unordered", []string{"2", "0", "1"}, "3"},
		{"NonNumericIgnored", []string{"0", "foo", "1"}, "2"}, // "foo" shouldn't block "2"
		{"Mix", []string{"0", "1", "2", "5"}, "3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindNextAutoName(tt.existing)
			if got != tt.expected {
				t.Errorf("FindNextAutoName(%v) = %s, want %s", tt.existing, got, tt.expected)
			}
		})
	}
}