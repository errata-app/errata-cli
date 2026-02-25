package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMentions(t *testing.T) {
	adapters := []string{"claude-sonnet-4-6", "claude-opus-4-6", "gpt-4o", "gemini-2.0-flash"}

	tests := []struct {
		name       string
		input      string
		wantIDs    []string
		wantPrompt string
		wantErrors []string
	}{
		{
			name:       "no mentions",
			input:      "fix the bug",
			wantIDs:    nil,
			wantPrompt: "fix the bug",
		},
		{
			name:       "single exact match",
			input:      "@claude-sonnet-4-6 fix the bug",
			wantIDs:    []string{"claude-sonnet-4-6"},
			wantPrompt: "fix the bug",
		},
		{
			name:       "prefix matches multiple (group targeting)",
			input:      "@claude fix the bug",
			wantIDs:    []string{"claude-sonnet-4-6", "claude-opus-4-6"},
			wantPrompt: "fix the bug",
		},
		{
			name:       "multiple mentions",
			input:      "@gpt-4o @claude-sonnet-4-6 compare these",
			wantIDs:    []string{"gpt-4o", "claude-sonnet-4-6"},
			wantPrompt: "compare these",
		},
		{
			name:       "case insensitive",
			input:      "@GPT-4O fix bug",
			wantIDs:    []string{"gpt-4o"},
			wantPrompt: "fix bug",
		},
		{
			name:       "no match stops consuming",
			input:      "@unknown rest of prompt",
			wantIDs:    nil,
			wantPrompt: "@unknown rest of prompt",
			wantErrors: []string{"unknown"},
		},
		{
			name:       "only mentions no prompt",
			input:      "@gpt-4o",
			wantIDs:    []string{"gpt-4o"},
			wantPrompt: "",
		},
		{
			name:       "mid-string at-sign not a mention",
			input:      "email user@example.com please",
			wantIDs:    nil,
			wantPrompt: "email user@example.com please",
		},
		{
			name:       "empty input",
			input:      "",
			wantIDs:    nil,
			wantPrompt: "",
		},
		{
			name:       "bare @ not consumed",
			input:      "@ fix this",
			wantIDs:    nil,
			wantPrompt: "@ fix this",
		},
		{
			name:       "duplicate mentions deduped",
			input:      "@gpt-4o @gpt-4o do something",
			wantIDs:    []string{"gpt-4o"},
			wantPrompt: "do something",
		},
		{
			name:       "prefix unique match",
			input:      "@gem fix bug",
			wantIDs:    []string{"gemini-2.0-flash"},
			wantPrompt: "fix bug",
		},
		{
			name:       "valid then invalid stops at invalid",
			input:      "@gpt-4o @badmodel rest of prompt",
			wantIDs:    []string{"gpt-4o"},
			wantPrompt: "@badmodel rest of prompt",
			wantErrors: []string{"badmodel"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseMentions(tt.input, adapters)
			assert.Equal(t, tt.wantIDs, result.ModelIDs)
			assert.Equal(t, tt.wantPrompt, result.Prompt)
			if tt.wantErrors != nil {
				assert.Equal(t, tt.wantErrors, result.Errors)
			} else {
				assert.Empty(t, result.Errors)
			}
		})
	}
}
