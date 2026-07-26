package requestlog

import (
	"gpt-load/internal/pricing"
	"gpt-load/internal/telemetry"
)

// PriceTableProvider exposes the currently published immutable pricing table.
type PriceTableProvider interface {
	Load() *pricing.Table
}

type queuedEvent struct {
	Event  telemetry.RequestEvent
	Prices *pricing.Table
}
