package paths_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/errata-app/errata-cli/internal/paths"
)

func TestDefault_RootIsData(t *testing.T) {
	l := paths.Default()
	assert.Equal(t, "data", l.Root)
	assert.Equal(t, "data/preferences.jsonl", l.Preferences)
	assert.Equal(t, "data/pricing_cache.json", l.PricingCache)
	assert.Equal(t, "data/prompt_history.jsonl", l.PromptHistory)
	assert.Equal(t, "data/configs.json", l.ConfigStore)
	assert.Equal(t, "data/outputs", l.Outputs)
	assert.Equal(t, "data/sessions", l.Sessions)
	assert.Equal(t, "data/checkpoint.json", l.Checkpoint)
}

func TestNew_CustomRoot(t *testing.T) {
	l := paths.New("/tmp/errata-test")
	assert.Equal(t, "/tmp/errata-test", l.Root)
	assert.Equal(t, "/tmp/errata-test/preferences.jsonl", l.Preferences)
	assert.Equal(t, "/tmp/errata-test/pricing_cache.json", l.PricingCache)
	assert.Equal(t, "/tmp/errata-test/prompt_history.jsonl", l.PromptHistory)
	assert.Equal(t, "/tmp/errata-test/configs.json", l.ConfigStore)
	assert.Equal(t, "/tmp/errata-test/outputs", l.Outputs)
	assert.Equal(t, "/tmp/errata-test/sessions", l.Sessions)
	assert.Equal(t, "/tmp/errata-test/checkpoint.json", l.Checkpoint)
}

func TestDefault_EqualsNewData(t *testing.T) {
	assert.Equal(t, paths.New("data"), paths.Default())
}
