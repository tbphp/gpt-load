package requestlog

import "gpt-load/internal/pricing"

// PriceTableProvider exposes the currently published immutable pricing table.
type PriceTableProvider interface {
	Load() *pricing.Table
}
