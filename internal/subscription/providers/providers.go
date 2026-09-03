// Package providers assembles the subscription provider implementations used
// by the application composition root. Provider-specific behavior stays in the
// corresponding child package; the generic runtime remains independent.
package providers

import (
	"gpt-load/internal/subscription/providers/antigravity"
	"gpt-load/internal/subscription/providers/claude"
	"gpt-load/internal/subscription/providers/codex"
	"gpt-load/internal/subscription/providers/grok"
	"gpt-load/internal/subscription/providers/kiro"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func Implementations() []subscriptionruntime.Implementations {
	return []subscriptionruntime.Implementations{
		codex.Implementations(),
		claude.Implementations(),
		antigravity.Implementations(),
		grok.Implementations(),
		kiro.Implementations(),
	}
}
