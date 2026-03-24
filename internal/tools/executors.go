package tools

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/errata-app/errata-cli/internal/sandbox"
)

// bashTimeoutKey is the context key for the bash tool timeout override.
type bashTimeoutKey struct{}

// WithBashTimeout returns a context carrying the given bash timeout.
// When set, ExecuteBash uses this instead of the default 2-minute timeout.
func WithBashTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, bashTimeoutKey{}, d)
}

// bashTimeoutFromContext returns the bash timeout from ctx,
// falling back to defaultBashTimeout when none is set.
func bashTimeoutFromContext(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(bashTimeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return defaultBashTimeout
}

// matchBashPrefix reports whether command matches the given prefix pattern.
// A trailing "*" (with optional spaces) is stripped from pattern; the remaining
// text must be a prefix of command (case-sensitive, no leading-space trim).
func matchBashPrefix(command, pattern string) bool {
	prefix := strings.TrimRight(pattern, "* ")
	return strings.HasPrefix(command, prefix)
}

// validatePathIn is the pure core of path validation: resolves path against root
// and rejects paths that escape it. No os.Getwd() — fully testable in isolation.
// Returns (absolutePath, root, "") on success or ("", "", errorMessage) on failure.
func validatePathIn(path, root string) (abs, resolvedRoot, errMsg string) {
	root = filepath.Clean(root)
	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else {
		resolved = filepath.Clean(filepath.Join(root, path))
	}
	rootPrefix := root + string(filepath.Separator)
	if !strings.HasPrefix(resolved+string(filepath.Separator), rootPrefix) {
		return "", "", fmt.Sprintf("[error: path %q is outside the working directory]", path)
	}
	return resolved, root, ""
}

// validatePath resolves path relative to cwd and rejects paths that escape it.
// Returns (absolutePath, cwd, "") on success or ("", "", errorMessage) on failure.
// Used by TUI-only functions: ApplyWrites, SnapshotFiles, RestoreSnapshots.
func validatePath(path string) (abs, cwd, errMsg string) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Sprintf("[error: cannot determine working directory: %v]", err)
	}
	return validatePathIn(path, cwd)
}

// validatePathCtx reads WorkDirFromContext, falling back to os.Getwd().
// Used by all executor functions called from DispatchTool.
func validatePathCtx(ctx context.Context, path string) (abs, root, errMsg string) {
	root = WorkDirFromContext(ctx)
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Sprintf("[error: cannot determine working directory: %v]", err)
		}
	}
	return validatePathIn(path, root)
}

// ExecuteRead reads a file relative to the working directory (from context or cwd).
// offset is 1-indexed (0 or 1 both mean "start at line 1").
// limit is the max lines to return (0 means use maxReadLines).
// Returns the file content, or an error string the model can see.
// Refuses paths that escape the working directory.
func ExecuteRead(ctx context.Context, path string, offset, limit int) string {
	abs, _, errMsg := validatePathCtx(ctx, path)
	if errMsg != "" {
		return errMsg
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("[error: file not found: %q]", path)
		}
		return fmt.Sprintf("[error: %v]", err)
	}

	// Normalize offset and limit.
	if offset <= 0 {
		offset = 1
	}
	if limit <= 0 || limit > maxReadLines {
		limit = maxReadLines
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)

	start := offset - 1 // convert to 0-indexed
	if start >= total {
		return fmt.Sprintf("[error: offset %d exceeds file length (%d lines)]", offset, total)
	}

	end := min(start+limit, total)

	result := strings.Join(lines[start:end], "\n")

	// Count remaining real lines (ignore the trailing empty element produced by a
	// trailing newline when strings.Split is applied to it).
	remaining := total - end
	if remaining > 0 && lines[total-1] == "" {
		remaining--
	}
	if remaining > 0 {
		result += fmt.Sprintf("\n[... %d lines omitted. Use offset=%d to continue reading.]", remaining, end+1)
	}

	return result
}

// ExecuteEditFile reads path, replaces exactly one occurrence of oldString with newString,
// and returns (newContent, ""). Returns ("", errorMessage) on failure.
// The caller is responsible for queuing the result as a ProposedWrite or writing directly.
// Refuses paths that escape the working directory.
func ExecuteEditFile(ctx context.Context, path, oldString, newString string) (string, string) {
	abs, _, errMsg := validatePathCtx(ctx, path)
	if errMsg != "" {
		return "", errMsg
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Sprintf("[error: file not found: %q]", path)
		}
		return "", fmt.Sprintf("[error: %v]", err)
	}

	content := string(data)
	count := strings.Count(content, oldString)
	switch count {
	case 0:
		return "", fmt.Sprintf("[error: old_string not found in %q]", path)
	case 1:
		return strings.Replace(content, oldString, newString, 1), ""
	default:
		return "", fmt.Sprintf("[error: old_string is ambiguous (%d matches) in %q — add more surrounding context]", count, path)
	}
}

// FileSnapshot captures the on-disk state of a file before a write operation.
// Used by /rewind to restore files to their previous state.
type FileSnapshot struct {
	Path        string // absolute path
	Content     string // original content (empty for new files)
	DidNotExist bool   // true = file was created by write; rewind should delete it
}

// SnapshotFiles reads the current on-disk content for each path in writes.
// Files that don't exist get DidNotExist: true. Paths are validated against
// the current working directory.
// For deletion entries (fw.Delete), the file's current content is captured so
// /rewind can restore it. If the file already doesn't exist, it is skipped.
func SnapshotFiles(writes []FileWrite) ([]FileSnapshot, error) {
	var snaps []FileSnapshot
	for _, fw := range writes {
		abs, _, errMsg := validatePath(fw.Path)
		if errMsg != "" {
			return nil, fmt.Errorf("%s", errMsg)
		}
		data, err := os.ReadFile(abs)
		if os.IsNotExist(err) {
			if fw.Delete {
				// File already gone — nothing to snapshot for rewind.
				continue
			}
			snaps = append(snaps, FileSnapshot{Path: abs, DidNotExist: true})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", fw.Path, err)
		}
		snaps = append(snaps, FileSnapshot{Path: abs, Content: string(data)})
	}
	return snaps, nil
}

// RestoreSnapshots restores files to their snapshotted state.
// DidNotExist entries are removed. Best-effort: continues on individual errors
// and returns the first error encountered.
func RestoreSnapshots(snaps []FileSnapshot) error {
	var firstErr error
	for _, s := range snaps {
		if s.DidNotExist {
			if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
				if firstErr == nil {
					firstErr = fmt.Errorf("remove %q: %w", s.Path, err)
				}
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(s.Path), 0o750); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("mkdir for %q: %w", s.Path, err)
			}
			continue
		}
		if err := os.WriteFile(s.Path, []byte(s.Content), 0o600); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("write %q: %w", s.Path, err)
			}
		}
	}
	return firstErr
}

// ApplyWrites writes each FileWrite to disk, creating parent directories as needed.
// Deletion entries (fw.Delete) remove the file instead of writing it.
// All paths are validated against the current working directory; writes that
// would escape it via ".." sequences are rejected with an error.
func ApplyWrites(writes []FileWrite) error {
	for _, fw := range writes {
		abs, _, errMsg := validatePath(fw.Path)
		if errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		if fw.Delete {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("delete %q: %w", fw.Path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			return fmt.Errorf("mkdir for %q: %w", fw.Path, err)
		}
		if err := os.WriteFile(abs, []byte(fw.Content), 0o644); err != nil { //nolint:gosec // G306: user code files should be world-readable
			return fmt.Errorf("write %q: %w", fw.Path, err)
		}
	}
	return nil
}

// ExecuteListDirectory lists a directory tree up to depth levels deep.
// path is relative to the working directory (from context or cwd).
// Returns an indented tree string, or an error message.
// Directories are suffixed with /. depth is clamped to [1, 5].
// File entries include a human-readable size hint (e.g. "handlers.go  (12 KB)").
//
// DERIVED: BFS indented-tree design from codex list_dir.rs
func ExecuteListDirectory(ctx context.Context, path string, depth int) string {
	if depth <= 0 {
		depth = 2
	}
	if depth > 5 {
		depth = 5
	}

	abs, _, errMsg := validatePathCtx(ctx, path)
	if errMsg != "" {
		return errMsg
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("[error: path not found: %q]", path)
		}
		return fmt.Sprintf("[error: %v]", err)
	}
	if !info.IsDir() {
		return fmt.Sprintf("[error: %q is not a directory]", path)
	}

	var lines []string
	collectDirEntries(abs, 0, depth, &lines)
	if len(lines) == 0 {
		return "(empty directory)"
	}
	return strings.Join(lines, "\n")
}

// collectDirEntries recursively collects directory entries into lines.
// File entries include a size hint; directory entries do not.
func collectDirEntries(dir string, currentDepth, maxDepth int, lines *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	indent := strings.Repeat("  ", currentDepth)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			*lines = append(*lines, indent+name+"/")
			if currentDepth+1 < maxDepth {
				collectDirEntries(filepath.Join(dir, name), currentDepth+1, maxDepth, lines)
			}
		} else {
			info, infoErr := entry.Info()
			if infoErr == nil {
				*lines = append(*lines, indent+name+"  ("+formatFileSize(info.Size())+")")
			} else {
				*lines = append(*lines, indent+name)
			}
		}
	}
}

// formatFileSize returns a compact human-readable file size string.
func formatFileSize(bytes int64) string {
	if bytes < 1024 {
		return "< 1 KB"
	}
	kb := (bytes + 512) / 1024 // round to nearest KB
	if kb < 1024 {
		return fmt.Sprintf("%d KB", kb)
	}
	mb := (bytes + 512*1024) / (1024 * 1024)
	return fmt.Sprintf("%d MB", mb)
}

// ExecuteSearchFiles finds files matching a glob pattern relative to basePath.
// basePath is relative to the working directory (from context or cwd).
// Returns newline-separated matching paths, or an error message.
func ExecuteSearchFiles(ctx context.Context, pattern, basePath string) string {
	if basePath == "" {
		basePath = "."
	}
	absBase, cwd, errMsg := validatePathCtx(ctx, basePath)
	if errMsg != "" {
		return errMsg
	}

	info, err := os.Stat(absBase)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("[error: base_path %q is not a directory]", basePath)
	}

	var matches []string
	err = filepath.Walk(absBase, func(fullPath string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // intentional: skip unreadable entries, continue walking
		}
		rel, _ := filepath.Rel(absBase, fullPath)
		rel = filepath.ToSlash(rel)
		matched, matchErr := matchGlob(pattern, rel)
		if matchErr != nil {
			return matchErr
		}
		if matched && !fi.IsDir() {
			// Return path relative to cwd for consistent output
			cwdRel, _ := filepath.Rel(cwd, fullPath)
			matches = append(matches, cwdRel)
		}
		return nil
	})
	if err != nil {
		return fmt.Sprintf("[error: invalid pattern %q: %v]", pattern, err)
	}

	if len(matches) == 0 {
		return "(no matches)"
	}
	return strings.Join(matches, "\n")
}

// ExecuteBash runs command via the system shell (sh -c) with a 2-minute timeout.
// stdout and stderr are combined; output is capped at bashOutputLimit bytes.
// If ctx carries a bash prefix allowlist (via WithBashPrefixes), the command must
// match one of the allowed prefixes or an error string is returned instead.
// If ctx carries a sandbox Config (via sandbox.WithConfig), the subprocess is
// wrapped with OS-level sandboxing appropriate for the current platform.
func ExecuteBash(ctx context.Context, command string) string {
	if prefixes := BashPrefixesFromContext(ctx); len(prefixes) > 0 {
		allowed := false
		for _, p := range prefixes {
			if matchBashPrefix(command, p) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Sprintf("[bash: command not allowed by recipe tools restriction: %q]", command)
		}
	}

	timeout := bashTimeoutFromContext(ctx)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Build subprocess, wrapping with OS-level sandbox when configured.
	var cmd *exec.Cmd
	if sbCfg, ok := sandbox.ConfigFromContext(ctx); ok && sbCfg.Active() {
		if sbCfg.ProjectRoot == "" {
			sbCfg.ProjectRoot, _ = os.Getwd()
		}
		cmd = sandbox.BuildCmd(timeoutCtx, sbCfg, "sh", "-c", command)
	} else {
		cmd = exec.CommandContext(timeoutCtx, "sh", "-c", command)
	}

	if dir := WorkDirFromContext(ctx); dir != "" {
		cmd.Dir = dir
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if runErr := cmd.Run(); runErr != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			output := out.String()
			if output == "" {
				return fmt.Sprintf("[error: command timed out after %s]", timeout)
			}
			return capOutput(output) + fmt.Sprintf("\n[error: command timed out after %s]", timeout)
		}
		// Non-zero exit is normal (e.g. test failures); return output + exit info.
		output := out.String()
		if output == "" {
			return fmt.Sprintf("[exit: %v]", runErr)
		}
		return capOutput(output) + fmt.Sprintf("\n[exit: %v]", runErr)
	}

	output := out.String()
	if output == "" {
		return "(no output)"
	}
	return capOutput(output)
}

// capOutput truncates output at bashOutputLimit bytes with a notice.
func capOutput(s string) string {
	if len(s) <= bashOutputLimit {
		return strings.TrimRight(s, "\n")
	}
	return strings.TrimRight(s[:bashOutputLimit], "\n") +
		fmt.Sprintf("\n[truncated: output exceeded %d bytes]", bashOutputLimit)
}

// matchGlob matches a slash-separated path against a glob pattern that may
// contain ** to match zero or more path segments. Single-segment wildcards
// (*, ?, [...]) use filepath.Match rules. ** must occupy a full path segment.
func matchGlob(pattern, path string) (bool, error) {
	p := filepath.ToSlash(pattern)
	f := filepath.ToSlash(path)
	return matchParts(strings.Split(p, "/"), strings.Split(f, "/"))
}

// matchParts is the recursive core of matchGlob.
func matchParts(pat, fp []string) (bool, error) {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			// ** matches zero or more segments: try every possible split point.
			for i := 0; i <= len(fp); i++ {
				if ok, err := matchParts(rest, fp[i:]); err != nil || ok {
					return ok, err
				}
			}
			return false, nil
		}
		if len(fp) == 0 {
			return false, nil
		}
		ok, err := filepath.Match(pat[0], fp[0])
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		pat, fp = pat[1:], fp[1:]
	}
	return len(fp) == 0, nil
}

// isBinaryFile reports whether data looks like a binary file by checking
// the first 512 bytes for a null byte.
func isBinaryFile(data []byte) bool {
	return bytes.ContainsRune(data[:min(len(data), 512)], 0)
}

// matchFileGlob reports whether name matches glob (base-name only, like
// grep --include). Returns true when glob is empty (no filter).
func matchFileGlob(name, glob string) bool {
	if glob == "" {
		return true
	}
	ok, _ := filepath.Match(glob, name)
	return ok
}

// searchFileLines appends grep-style output lines to results for every line
// in lines that matches re. relPath is the display path prefix.
//
// When contextLines <= 0, only matching lines are emitted (file:N:content).
// When contextLines > 0, before/after context is included with "-" separators
// for context lines and ":" for match lines, and "--" between non-adjacent groups.
func searchFileLines(re *regexp.Regexp, relPath string, lines []string, contextLines int, results *[]string) {
	if contextLines <= 0 {
		for i, line := range lines {
			if re.MatchString(line) {
				*results = append(*results, fmt.Sprintf("%s:%d:%s", relPath, i+1, line))
			}
		}
		return
	}

	// Collect matching indices.
	var matchIdx []int
	for i, line := range lines {
		if re.MatchString(line) {
			matchIdx = append(matchIdx, i)
		}
	}
	if len(matchIdx) == 0 {
		return
	}

	// Build context groups, merging overlapping ranges.
	type span struct{ lo, hi int } // inclusive line indices
	var groups []span
	for _, idx := range matchIdx {
		lo := max(idx-contextLines, 0)
		hi := idx + contextLines
		if hi >= len(lines) {
			hi = len(lines) - 1
		}
		if len(groups) > 0 && lo <= groups[len(groups)-1].hi+1 {
			groups[len(groups)-1].hi = hi
		} else {
			groups = append(groups, span{lo, hi})
		}
	}

	// Build a set of match line indices for O(1) lookup.
	matchSet := make(map[int]bool, len(matchIdx))
	for _, idx := range matchIdx {
		matchSet[idx] = true
	}

	for gi, g := range groups {
		if gi > 0 {
			*results = append(*results, "--")
		}
		for i := g.lo; i <= g.hi; i++ {
			if matchSet[i] {
				*results = append(*results, fmt.Sprintf("%s:%d:%s", relPath, i+1, lines[i]))
			} else {
				*results = append(*results, fmt.Sprintf("%s-%d-%s", relPath, i+1, lines[i]))
			}
		}
	}
}

// ExecuteSearchCode searches file contents for pattern using pure Go
// (filepath.WalkDir + regexp). Skips .git/ directories and binary files.
// path and fileGlob are optional; path defaults to ".".
// contextLines adds N lines of context before and after each match.
// Returns grep-compatible output (path:line:content format) or an error message.
func ExecuteSearchCode(ctx context.Context, pattern, path, fileGlob string, contextLines int) string {
	if path == "" {
		path = "."
	}
	absPath, root, errMsg := validatePathCtx(ctx, path)
	if errMsg != "" {
		return errMsg
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Sprintf("[error: invalid regex %q: %v]", pattern, err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, searchCommandTimeout)
	defer cancel()

	var results []string
	walkErr := filepath.WalkDir(absPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries
		}
		if timeoutCtx.Err() != nil {
			return timeoutCtx.Err()
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !matchFileGlob(d.Name(), fileGlob) {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil //nolint:nilerr // skip unreadable files
		}
		if isBinaryFile(data) {
			return nil
		}

		relPath, _ := filepath.Rel(root, p)
		lines := strings.Split(string(data), "\n")
		// Remove trailing empty element from final newline.
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		searchFileLines(re, relPath, lines, contextLines, &results)
		return nil
	})
	if walkErr != nil && timeoutCtx.Err() == context.DeadlineExceeded {
		return "[error: search timed out after 30s]"
	}

	if len(results) == 0 {
		return "(no matches)"
	}
	return strings.Join(results, "\n")
}

// WriteFileDirect writes content to path, resolving against WorkDirFromContext.
// Creates intermediate directories as needed. Returns "" on success, "[error: ...]" on failure.
func WriteFileDirect(ctx context.Context, path, content string) string {
	abs, _, errMsg := validatePathCtx(ctx, path)
	if errMsg != "" {
		return errMsg
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return fmt.Sprintf("[error: mkdir for %q: %v]", path, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil { //nolint:gosec // G306: user code files should be world-readable
		return fmt.Sprintf("[error: write %q: %v]", path, err)
	}
	return ""
}
