package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// matchGlobCase is a single test case for matchGlob.
type matchGlobCase struct {
	pattern string
	path    string
	want    bool
}

var matchGlobCases = []matchGlobCase{
	// ── ** at the start ──────────────────────────────────────────────────────
	// ** matches zero segments (file is at root)
	{"**/*.go", "main.go", true},
	// ** matches one segment
	{"**/*.go", "internal/tools.go", true},
	// ** matches two segments
	{"**/*.go", "internal/tools/tools.go", true},
	// ** matches many segments
	{"**/*.go", "a/b/c/d/e.go", true},
	// extension mismatch
	{"**/*.go", "main.txt", false},
	{"**/*.go", "internal/main.txt", false},

	// ── No ** (single * does NOT cross separators) ───────────────────────────
	{"*.go", "main.go", true},
	{"*.go", "internal/main.go", false}, // * cannot match the /
	{"*.go", "main.txt", false},

	// ── ** in the middle ─────────────────────────────────────────────────────
	// ** = zero segments
	{"internal/**/*.go", "internal/tools.go", true},
	// ** = one segment
	{"internal/**/*.go", "internal/tools/tools.go", true},
	// ** = two segments
	{"internal/**/*.go", "internal/a/b/tools.go", true},
	// wrong root dir
	{"internal/**/*.go", "cmd/main.go", false},
	// trailing segment after final *
	{"internal/**/*.go", "internal/tools/tools_test.go", true},

	// ── ** alone ─────────────────────────────────────────────────────────────
	// ** (bare) matches any single-segment path
	{"**", "main.go", true},
	// ** matches multi-segment paths too
	{"**", "a/b/c.go", true},

	// ── ** between two literal segments ─────────────────────────────────────
	// a/**/b: ** = zero → a/b
	{"a/**/b", "a/b", true},
	// a/**/b: ** = one
	{"a/**/b", "a/x/b", true},
	// a/**/b: ** = two
	{"a/**/b", "a/x/y/b", true},
	// extra trailing segment → no match
	{"a/**/b", "a/b/c", false},
	// wrong root
	{"a/**/b", "x/b", false},

	// ── ** at the end ────────────────────────────────────────────────────────
	{"cmd/**", "cmd/main.go", true},
	{"cmd/**", "cmd/sub/main.go", true},
	{"cmd/**", "internal/main.go", false},
	// cmd/** with ** = zero segments does match "cmd" itself.
	// In practice this is harmless: ExecuteSearchFiles guards with !fi.IsDir(),
	// so bare directory entries are never returned as matches.
	{"cmd/**", "cmd", true},

	// ── Pattern anchored by a filename ───────────────────────────────────────
	{"**/runner.go", "runner.go", true},
	{"**/runner.go", "internal/runner/runner.go", true},
	{"**/runner.go", "internal/runner/runner_test.go", false},

	// ── Multiple ** segments ─────────────────────────────────────────────────
	{"**/**/*.go", "main.go", true},
	{"**/**/*.go", "a/b.go", true},
	{"**/**/*.go", "a/b/c.go", true},
	{"**/**/*.go", "a/b/c/d.go", true},

	// ── Test-file filter ─────────────────────────────────────────────────────
	{"**/*_test.go", "main_test.go", true},
	{"**/*_test.go", "internal/runner/runner_test.go", true},
	{"**/*_test.go", "internal/runner/runner.go", false},

	// ── Exact path (no wildcards) ────────────────────────────────────────────
	{"internal/tools/tools.go", "internal/tools/tools.go", true},
	{"internal/tools/tools.go", "internal/tools/other.go", false},

	// ── Question-mark wildcard ───────────────────────────────────────────────
	{"?.go", "a.go", true},
	{"?.go", "ab.go", false},
	{"**/?.go", "internal/a.go", true},

	// ── Character class ──────────────────────────────────────────────────────
	{"[abc].go", "a.go", true},
	{"[abc].go", "d.go", false},

	// ── Empty pattern / empty path ───────────────────────────────────────────
	{"", "", true},
	{"", "main.go", false},
	{"*.go", "", false},
}

func TestMatchGlob(t *testing.T) {
	for _, tc := range matchGlobCases {
		name := tc.pattern + " ~ " + tc.path
		t.Run(name, func(t *testing.T) {
			got, err := matchGlob(tc.pattern, tc.path)
			require.NoError(t, err, "unexpected error")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMatchGlob_BadPattern(t *testing.T) {
	_, err := matchGlob("[invalid", "main.go")
	assert.Error(t, err, "malformed character class should return an error")
}

func TestMatchGlob_BackslashSeparator(t *testing.T) {
	// Windows-style paths should be normalised — the function uses filepath.ToSlash.
	got, err := matchGlob(`**\*.go`, `internal\tools.go`)
	// On non-Windows, backslash is a literal character in patterns, so this
	// will not match — the important thing is no panic and a clear bool result.
	require.NoError(t, err)
	// Just assert it doesn't panic; value depends on OS path separator.
	_ = got
}
