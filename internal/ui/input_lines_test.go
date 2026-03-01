package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInputLines_Empty(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetWidth(80)
	assert.Equal(t, 1, a.inputLines())
}

func TestInputLines_SingleShortLine(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetWidth(80)
	a.input.SetValue("hello")
	assert.Equal(t, 1, a.inputLines())
}

func TestInputLines_MultipleNewlines(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetWidth(80)
	a.input.SetValue("line1\nline2\nline3")
	assert.Equal(t, 3, a.inputLines())
}

func TestInputLines_SoftWrap(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetWidth(10)
	// SetWidth(10) → Width() = 8 (prompt subtracted). 25 chars → ceil(25/8) = 4.
	a.input.SetValue(strings.Repeat("a", 25))
	got := a.inputLines()
	assert.Greater(t, got, 1, "long line should wrap to more than 1 line")
}

func TestInputLines_CappedAtMaxHeight(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetWidth(80)
	a.input.MaxHeight = 8
	// 20 newlines = 21 lines, should be capped at MaxHeight (8).
	a.input.SetValue(strings.Repeat("x\n", 20) + "x")
	assert.Equal(t, 8, a.inputLines())
}

func TestInputLines_EmptyLinesCount(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetWidth(80)
	// Two empty lines (three newline-separated segments).
	a.input.SetValue("\n\n")
	assert.Equal(t, 3, a.inputLines())
}

func TestInputLines_SoftWrapPlusNewlines(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetWidth(10)
	// Long first line wraps; second short line adds 1. Total > line count from newlines alone.
	a.input.SetValue(strings.Repeat("b", 25) + "\nhello")
	got := a.inputLines()
	assert.Greater(t, got, 2, "wrapped line + newline should exceed 2 lines")
}
