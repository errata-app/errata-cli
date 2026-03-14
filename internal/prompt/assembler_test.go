package prompt_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/errata-app/errata-cli/internal/prompt"
)

func TestResolveSummarizationPrompt_Custom(t *testing.T) {
	ctx := prompt.WithSummarizationPrompt(context.Background(), "custom summary prompt")
	got := prompt.ResolveSummarizationPrompt(ctx)
	assert.Equal(t, "custom summary prompt", got)
}

func TestResolveSummarizationPrompt_FallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	got := prompt.ResolveSummarizationPrompt(ctx)
	assert.Equal(t, prompt.DefaultSummarizationPrompt, got)
}

func TestResolveSummarizationPrompt_EmptyStringFallsBackToDefault(t *testing.T) {
	ctx := prompt.WithSummarizationPrompt(context.Background(), "")
	got := prompt.ResolveSummarizationPrompt(ctx)
	assert.Equal(t, prompt.DefaultSummarizationPrompt, got)
}
