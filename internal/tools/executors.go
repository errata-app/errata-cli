package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/suarezc/errata/internal/sandbox"
)

// bashTimeoutOverride, when > 0, overrides the default bash timeout.
// Set via SetBashTimeout (recipe ## Constraints bash_timeout:).
var bashTimeoutOverride time.Duration

// SetBashTimeout overrides the default bash tool timeout.
// Pass 0 to reset to the default.
func SetBashTimeout(d time.Duration) { bashTimeoutOverride = d }

// bashTimeout returns the effective bash timeout.
func bashTimeout() time.Duration {
	if bashTimeoutOverride > 0 {
		return bashTimeoutOverride
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

// validatePath resolves path relative to cwd and rejects paths that escape it.
// Returns (absolutePath, cwd, "") on success or ("", "", errorMessage) on failure.
func validatePath(path string) (abs, cwd, errMsg string) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Sprintf("[error: cannot determine working directory: %v]", err)
	}
	abs, err = filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Sprintf("[error: invalid path %q: %v]", path, err)
	}
	cwdClean := filepath.Clean(cwd) + string(filepath.Separator)
	absClean := filepath.Clean(abs)
	if !strings.HasPrefix(absClean+string(filepath.Separator), cwdClean) {
		return "", "", fmt.Sprintf("[error: path %q is outside the working directory]", path)
	}
	return abs, cwd, ""
}

// ExecuteRead reads a file relative to cwd.
// offset is 1-indexed (0 or 1 both mean "start at line 1").
// limit is the max lines to return (0 means use maxReadLines).
// Returns the file content, or an error string the model can see.
// Refuses paths that escape the working directory.
func ExecuteRead(path string, offset, limit int) string {
	abs, _, errMsg := validatePath(path)
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
// The caller is responsible for queuing the result as a ProposedWrite.
// Refuses paths that escape the working directory.
func ExecuteEditFile(path, oldString, newString string) (string, string) {
	abs, _, errMsg := validatePath(path)
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
func SnapshotFiles(writes []FileWrite) ([]FileSnapshot, error) {
	var snaps []FileSnapshot
	for _, fw := range writes {
		abs, _, errMsg := validatePath(fw.Path)
		if errMsg != "" {
			return nil, fmt.Errorf("%s", errMsg)
		}
		data, err := os.ReadFile(abs)
		if os.IsNotExist(err) {
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
// All paths are validated against the current working directory; writes that
// would escape it via ".." sequences are rejected with an error.
func ApplyWrites(writes []FileWrite) error {
	for _, fw := range writes {
		abs, _, errMsg := validatePath(fw.Path)
		if errMsg != "" {
			return fmt.Errorf("%s", errMsg)
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
// path is relative to cwd. Returns an indented tree string, or an error message.
// Directories are suffixed with /. depth is clamped to [1, 5].
// File entries include a human-readable size hint (e.g. "handlers.go  (12 KB)").
//
// DERIVED: BFS indented-tree design from codex list_dir.rs
func ExecuteListDirectory(path string, depth int) string {
	if depth <= 0 {
		depth = 2
	}
	if depth > 5 {
		depth = 5
	}

	abs, _, errMsg := validatePath(path)
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
// basePath is relative to cwd. Returns newline-separated matching paths, or an error message.
func ExecuteSearchFiles(pattern, basePath string) string {
	if basePath == "" {
		basePath = "."
	}
	absBase, cwd, errMsg := validatePath(basePath)
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

	timeout := bashTimeout()
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

// ExecuteSearchCode searches file contents for pattern using grep.
// path and fileGlob are optional; path defaults to ".".
// contextLines adds N lines of context before and after each match (grep -C N).
// Returns grep output (path:line:content format) or an error message.
//
// DERIVED: subprocess + 30s timeout pattern from codex grep_files.rs
func ExecuteSearchCode(pattern, path, fileGlob string, contextLines int) string {
	if path == "" {
		path = "."
	}
	absPath, _, errMsg := validatePath(path)
	if errMsg != "" {
		return errMsg
	}

	args := []string{"-rn"}
	if fileGlob != "" {
		args = append(args, "--include="+fileGlob)
	}
	if contextLines > 0 {
		args = append(args, fmt.Sprintf("-C%d", contextLines))
	}
	args = append(args, "--", pattern, absPath)

	ctx, cancel := context.WithTimeout(context.Background(), searchCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "grep", args...)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if runErr := cmd.Run(); runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "[error: search timed out after 30s]"
		}
		// grep exit code 1 means no matches — not an error.
		if out.Len() == 0 {
			return "(no matches)"
		}
	}

	output := out.String()
	// Make paths relative to cwd for cleaner output
	output = strings.ReplaceAll(output, absPath+string(filepath.Separator), "")
	output = strings.ReplaceAll(output, absPath, "")

	if output == "" {
		return "(no matches)"
	}
	return strings.TrimRight(output, "\n")
}
