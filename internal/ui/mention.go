package ui

import "strings"

// MentionResult holds the result of parsing @mentions from prompt text.
type MentionResult struct {
	ModelIDs []string // resolved full model IDs (deduped)
	Prompt   string   // remaining text after stripping leading @mentions
	Errors   []string // unresolved @tokens (no matching adapter)
}

// ParseMentions extracts leading @model tokens from a prompt string and
// resolves them against the known adapter IDs using case-insensitive prefix
// matching. If a prefix matches multiple adapters, all are included (group
// targeting). Consumption stops at the first non-@ word or an unresolved @token.
func ParseMentions(input string, adapterIDs []string) MentionResult {
	words := strings.Fields(input)
	if len(words) == 0 {
		return MentionResult{Prompt: input}
	}

	seen := make(map[string]bool)
	var modelIDs []string
	var errors []string
	consumed := 0

	for _, word := range words {
		if !strings.HasPrefix(word, "@") || len(word) < 2 {
			break
		}
		prefix := strings.ToLower(word[1:])
		var matched []string
		for _, id := range adapterIDs {
			if strings.HasPrefix(strings.ToLower(id), prefix) {
				matched = append(matched, id)
			}
		}
		if len(matched) == 0 {
			errors = append(errors, word[1:])
			break
		}
		for _, id := range matched {
			if !seen[id] {
				seen[id] = true
				modelIDs = append(modelIDs, id)
			}
		}
		consumed++
	}

	prompt := strings.TrimSpace(strings.Join(words[consumed:], " "))
	return MentionResult{
		ModelIDs: modelIDs,
		Prompt:   prompt,
		Errors:   errors,
	}
}
