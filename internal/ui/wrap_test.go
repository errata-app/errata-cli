package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
)

func TestWrapText(t *testing.T) {
	// Use an unstyled style so output is plain text for easy assertion.
	plain := lipgloss.NewStyle()

	tests := []struct {
		name   string
		text   string
		width  int
		indent int
		check  func(t *testing.T, result string)
	}{
		{
			name:   "short text no wrap",
			text:   "hello world",
			width:  80,
			indent: 2,
			check: func(t *testing.T, result string) {
				assert.Equal(t, "  hello world", result)
				assert.Equal(t, 1, strings.Count(result, "\n")+1, "should be single line")
			},
		},
		{
			name:   "zero width passthrough",
			text:   "hello world",
			width:  0,
			indent: 2,
			check: func(t *testing.T, result string) {
				assert.Equal(t, "  hello world", result)
			},
		},
		{
			name:   "negative width passthrough",
			text:   "some text here",
			width:  -1,
			indent: 4,
			check: func(t *testing.T, result string) {
				assert.Equal(t, "    some text here", result)
			},
		},
		{
			name:   "long text wraps at word boundary",
			text:   "the quick brown fox jumps over the lazy dog",
			width:  20,
			indent: 2,
			check: func(t *testing.T, result string) {
				lines := strings.Split(result, "\n")
				assert.Greater(t, len(lines), 1, "should wrap to multiple lines")
				for _, line := range lines {
					assert.True(t, strings.HasPrefix(line, "  "), "each line should be indented: %q", line)
				}
			},
		},
		{
			name:   "continuation lines indented",
			text:   "word1 word2 word3 word4 word5",
			width:  14,
			indent: 2,
			check: func(t *testing.T, result string) {
				lines := strings.Split(result, "\n")
				for i, line := range lines {
					assert.True(t, strings.HasPrefix(line, "  "),
						"line %d should start with indent: %q", i, line)
				}
			},
		},
		{
			name:   "embedded newlines handled per line",
			text:   "line one\nline two\nline three",
			width:  80,
			indent: 2,
			check: func(t *testing.T, result string) {
				lines := strings.Split(result, "\n")
				assert.Len(t, lines, 3)
				assert.Equal(t, "  line one", lines[0])
				assert.Equal(t, "  line two", lines[1])
				assert.Equal(t, "  line three", lines[2])
			},
		},
		{
			name:   "very narrow width still works",
			text:   "hello world",
			width:  5,
			indent: 2,
			check: func(t *testing.T, result string) {
				lines := strings.Split(result, "\n")
				assert.Greater(t, len(lines), 1, "should wrap")
				for _, line := range lines {
					assert.True(t, strings.HasPrefix(line, "  "), "indent preserved: %q", line)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapText(tt.text, tt.width, tt.indent, plain)
			tt.check(t, result)
		})
	}
}
