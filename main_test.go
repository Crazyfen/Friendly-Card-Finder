package main

import (
	"strings"
	"testing"
)

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		limit    int
		expected []string
	}{
		{
			name:     "message under limit",
			input:    "hello world",
			limit:    20,
			expected: []string{"hello world"},
		},
		{
			name:     "message exactly at limit",
			input:    "hello",
			limit:    5,
			expected: []string{"hello"},
		},
		{
			name:     "simple split without newlines",
			input:    "hello world this is a test",
			limit:    10,
			expected: []string{"hello worl", "d this is ", "a test"},
		},
		{
			name:     "split with newline before limit",
			input:    "line1\nline2\nline3",
			limit:    10,
			expected: []string{"line1\n", "line2\n", "line3"},
		},
		{
			name:     "split respects newline at limit boundary",
			input:    "hello\nworld",
			limit:    6,
			expected: []string{"hello\n", "world"},
		},
		{
			name:     "empty string",
			input:    "",
			limit:    10,
			expected: []string{""},
		},
		{
			name:     "single character",
			input:    "a",
			limit:    10,
			expected: []string{"a"},
		},
		{
			name:     "multiple newlines in sequence",
			input:    "a\n\nb",
			limit:    10,
			expected: []string{"a\n\nb"},
		},
		{
			name:     "utf8 characters",
			input:    "привет мир тест",
			limit:    10,
			expected: []string{"приве", "т мир ", "тест"},
		},
		{
			name:     "utf8 with newlines",
			input:    "привет\nмир\nтест",
			limit:    15,
			expected: []string{"привет\n", "мир\nтест"},
		},
		{
			name:     "very small limit",
			input:    "abcdef",
			limit:    2,
			expected: []string{"ab", "cd", "ef"},
		},
		{
			name:     "newline at exact cut point",
			input:    "hello\nworld",
			limit:    5,
			expected: []string{"hello", "\nworl", "d"},
		},
		{
			name:     "telegram message scenario",
			input:    strings.Repeat("x", 5000),
			limit:    4096,
			expected: []string{strings.Repeat("x", 4096), strings.Repeat("x", 904)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitMessage(tt.input, tt.limit)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d parts, got %d", len(tt.expected), len(result))
			}

			for i, part := range result {
				if i < len(tt.expected) && part != tt.expected[i] {
					t.Errorf("part %d: expected %q, got %q", i, tt.expected[i], part)
				}
				if len(part) > tt.limit {
					t.Errorf("part %d exceeds limit: %d > %d", i, len(part), tt.limit)
				}
			}

			// Verify all parts concatenate to original
			joined := strings.Join(result, "")
			if joined != tt.input {
				t.Errorf("concatenated parts don't match input")
			}
		})
	}
}

func BenchmarkSplitMessage(b *testing.B) {
	largeMessage := strings.Repeat("This is a sample message to benchmark the splitMessage function. ", 1000)
	limit := 4096

	for b.Loop() {
		splitMessage(largeMessage, limit)
	}
}
