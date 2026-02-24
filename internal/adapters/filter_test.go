package adapters

import (
	"testing"
)

func TestIsOpenAIChatModel_ChatModelsAccepted(t *testing.T) {
	cases := []string{
		// GPT family — all covered by "gpt-" prefix
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4-turbo",
		"gpt-4-turbo-preview",
		"gpt-4.1",
		"gpt-4.5",
		"gpt-3.5-turbo",
		"gpt-5",        // hypothetical future model
		"gpt-5-turbo",  // hypothetical future model
		// o-series reasoning models
		"o1",
		"o1-mini",
		"o1-preview",
		"o2",
		"o3",
		"o3-mini",
		"o4-mini",
		"o5",           // hypothetical future model
		// chatgpt-* variants
		"chatgpt-4o-latest",
	}
	for _, id := range cases {
		if !isOpenAIChatModel(id) {
			t.Errorf("isOpenAIChatModel(%q) = false, want true", id)
		}
	}
}

func TestIsOpenAIChatModel_NonChatModelsRejected(t *testing.T) {
	cases := []string{
		"text-embedding-ada-002",
		"text-embedding-3-large",
		"whisper-1",
		"dall-e-3",
		"dall-e-2",
		"tts-1",
		"tts-1-hd",
		"babbage-002",
		"davinci-002",
		"omni-moderation-latest",
		"text-moderation-latest",
	}
	for _, id := range cases {
		if isOpenAIChatModel(id) {
			t.Errorf("isOpenAIChatModel(%q) = true, want false", id)
		}
	}
}
