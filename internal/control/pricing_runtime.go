package control

import (
	"sync/atomic"

	"gpt-load/internal/pricing"
)

// PriceRuntime owns the current immutable pricing table.
type PriceRuntime struct {
	current atomic.Pointer[pricing.Table]
}

func NewPriceRuntime() *PriceRuntime {
	return &PriceRuntime{}
}

func (runtime *PriceRuntime) Load() *pricing.Table {
	if runtime == nil {
		return nil
	}
	return runtime.current.Load()
}

func (runtime *PriceRuntime) Publish(table *pricing.Table) {
	if runtime == nil || table == nil {
		return
	}
	runtime.current.Store(table)
}
