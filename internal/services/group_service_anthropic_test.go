package services

import (
	"testing"

	"gpt-load/internal/models"
)

func TestNormalizeAnthropicSystemPromptCount(t *testing.T) {
	tests := []struct {
		name        string
		channelType string
		count       int
		want        int
	}{
		{name: "non-anthropic forced zero", channelType: "openai", count: 5, want: 0},
		{name: "negative clamped to zero", channelType: "anthropic", count: -1, want: 0},
		{name: "within range kept", channelType: "anthropic", count: 2, want: 2},
		{name: "upper bound clamped", channelType: "anthropic", count: models.MaxAnthropicSystemPromptCount + 1, want: models.MaxAnthropicSystemPromptCount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeAnthropicSystemPromptCount(tt.channelType, tt.count)
			if got != tt.want {
				t.Fatalf("normalizeAnthropicSystemPromptCount(%q, %d) = %d, want %d", tt.channelType, tt.count, got, tt.want)
			}
		})
	}
}
